/**
 * BetterDesk Web Remote Client - Main Client Orchestrator
 * Ties together all rdclient modules: connection, protocol, crypto,
 * video, audio, renderer, and input.
 *
 * Usage:
 *   const client = new RDClient(canvas, { deviceId: 'ABC123' });
 *   client.on('state', (state) => updateUI(state));
 *   await client.connect();
 *   // user enters password...
 *   await client.authenticate(password);
 *   // ...session runs...
 *   client.disconnect();
 */
import { RDAudio } from './audio.ts';
import { RDClipboard } from './clipboard.ts';
import { RDCliprdr } from './cliprdr.ts';
import { RDConnection } from './connection.ts';
import { RDConnectionParams, type RDConnectionSettings } from './connection-params.ts';
import { RDCrypto } from './crypto.ts';
import { RDFileConnection } from './file-connection.ts';
import { RDFileTransfer } from './filetransfer.ts';
import { RDInput } from './input.ts';
import { RDKeyboardEncoder } from './keyboard-encoder.ts';
import { debugLog } from './log.ts';
import { page } from './page.ts';
import { RDProtocol } from './protocol.ts';
import { RDPunchHoleResponse } from './punchhole-response.ts';
import { RDRenderer } from './renderer.ts';
import { RDVideo } from './video.ts';
import type {
	AudioFrame,
	Clipboard,
	Cliprdr,
	FileAction,
	FileResponse,
	IClipboard,
	LoginResponse,
	Message,
	MessageInit,
	Misc,
	MultiClipboards,
	PeerInfo,
	PublicKey,
	SignedId,
	SupportedEncoding,
	TestDelay,
	VideoFrame
} from './generated/protos.ts';

/** An event listener; each event has its own arguments (see the _emit calls) */
export type RDClientListener = (...args: never[]) => void;

export type RDClientState =
	| 'idle'
	| 'connecting'
	| 'waiting_password'
	| 'waiting_2fa'
	| 'authenticating'
	| 'streaming'
	| 'disconnected'
	| 'error';

/** What the viewer passes (ClientOptions in ../rdclient.ts); only deviceId is required */
export interface RDClientOptions {
	/** Target device ID */
	deviceId: string;
	/** The server's public key (base64 or hex): the device's identity is checked against it */
	serverPubKey?: string;
	/** The server's connection policy (/api/health), sizes the rendezvous waits */
	connection?: Record<string, unknown> | null;
	myName?: string;
	/** 'fit' | 'fill' | '1:1' | 'stretch' (default 'fit') */
	scaleMode?: string;
	/** Default 60, at most 30 without WebCodecs or on a tablet */
	fps?: number;
	/** '30' | '60' | 'adaptive' */
	fpsMode?: string;
	imageQuality?: string;
	qualityPreset?: string;
	adaptiveQuality?: boolean;
	preferCodec?: string;
	disableAudio?: boolean;
	/** A watch-only guest link: view only from the start, and it stays on */
	viewOnly?: boolean;
	rendezvousTimeoutMs?: number;
	signalRelayTimeoutMs?: number;
}

/** A session error as the 'diagnostics' event and getStats() report it */
export interface RDClientErrorInfo {
	message: string;
	at: number;
	state: RDClientState;
	codec: RDVideo['currentCodec'] | null;
}

/** A session end as the 'diagnostics' and 'disconnected' events and getStats() report it */
export interface RDClientDisconnectInfo {
	type: 'disconnect';
	reason: string;
	source: string;
	stateBefore: RDClientState;
	at: number;
	durationMs: number | null;
	transport: 'rd';
	secureContext: boolean | null;
	codec: RDVideo['currentCodec'] | null;
	closeCode: number | null;
	closeReason: string;
	peerReason: string;
	error: string;
}

/** Where a disconnect came from (the relay socket, the device) */
export interface RDClientDisconnectMeta {
	source?: string;
	closeCode?: number;
	closeReason?: string;
	closeInfo?: unknown;
	peerReason?: string;
	error?: string;
}

export interface RDClientStats {
	state: RDClientState;
	video: ReturnType<RDVideo['getStats']>;
	audio: ReturnType<RDAudio['getStats']>;
	renderer: ReturnType<RDRenderer['getStats']>;
	connection: RDConnection['state'];
	diagnostics: {
		lastClose: RDConnection['_lastClose'] | null;
		lastDisconnect: RDClientDisconnectInfo | null;
		lastError: RDClientErrorInfo | null;
	};
}

export interface RDClientMonitor {
	idx: number;
	name: string;
	osName: string;
	width: number;
	height: number;
	primary: boolean;
	current: boolean;
}

export interface RDClientWindowsSessions {
	sessions: { sid: number; name: string }[];
	currentSid: number;
}

export interface RDClientVirtualDisplaySupport {
	supported: boolean;
	impl: string;
	rustdeskDisplays: number[];
	amyuniCount: number;
}

/** Clipboard content to send: text, and HTML or a PNG for the rich formats */
export interface RDClientClipboardEntry {
	text?: string;
	html?: string;
	pngBlob?: Blob | null;
}

/** What the rendezvous server answered: the relay to use, or why there is none */
interface RendezvousAnswer {
	error?: string;
	relayServer?: string;
	uuid?: string;
	pk?: Uint8Array | null;
	natType?: number;
	id?: string;
}

interface QualityTier {
	id: number;
	quality: string;
	fps: number;
}

/** err.message or err.stack as the JS read them: undefined when the caught value has none */
function errorField(err: unknown, key: 'message' | 'stack'): unknown {
	return typeof err === 'object' && err !== null ? Reflect.get(err, key) : undefined;
}

export class RDClient {
	deviceId: string;
	opts: RDClientOptions;
	_rendezvousTimeoutMs: number;
	_signalRelayTimeoutMs: number;
	_connectionParams: RDConnectionSettings;
	conn: RDConnection;
	proto: RDProtocol;
	crypto: RDCrypto;
	video: RDVideo;
	audio: RDAudio;
	renderer: RDRenderer;
	input: RDInput;
	_fileConnection: RDFileConnection | null;
	_sessionPassword: string;
	fileTransfer: RDFileTransfer;
	_state: RDClientState;
	_listeners: Record<string, RDClientListener[]>;
	_peerInfo: PeerInfo | null;
	_loginChallenge: string | null;
	_loginSalt?: string;
	_pingInterval: ReturnType<typeof setInterval> | null;
	_statsInterval: ReturnType<typeof setInterval> | null;
	_stallCheckInterval?: ReturnType<typeof setInterval> | null;
	_adaptiveInterval?: ReturnType<typeof setInterval> | null;
	_rendezvousDecoder: ReturnType<RDProtocol['createStreamDecoder']> | null;
	_codecAbilities: Record<string, boolean> | null;
	_peerEncoding: SupportedEncoding | null;
	_windowsSessions: RDClientWindowsSessions;
	_preferCodec: string;
	_mobileRuntime: boolean;
	_adaptivePaused: boolean;
	_codecFallbackDone: boolean;
	_relayFrameIdx: number;
	_relayConfirmReceived: boolean;
	_peerEncryptionConfirmed: boolean;
	_keyExchangeDone: boolean;
	_peerSignedPk?: Uint8Array | null;
	_sessionStartedAt: number | null;
	_lastDisconnect: RDClientDisconnectInfo | null;
	_lastError: RDClientErrorInfo | null;
	_connectRun: number;
	_sessionActive: boolean;
	_clipboardToLocalEnabled: boolean;
	_clipboardDisabled: boolean;
	_savedActiveFps: number | null;
	_backgroundFps: number;
	_streamThrottledActive: boolean | null;
	_viewOnlyLocked: boolean;
	_viewOnly: boolean;
	_peerFrameCount?: number;
	_lastVideoFrameTime?: number;
	_lastDecodedCount?: number;
	_lastDecodeProgressTime?: number;
	_currentDisplay?: number;
	_virtualDisplay?: RDClientVirtualDisplaySupport;
	_recorder?: MediaRecorder | null;
	_recordedChunks?: Blob[];
	_recordingStartTime?: number;

	constructor(canvas: HTMLCanvasElement, opts: RDClientOptions = { deviceId: '' }) {
		if (!canvas) throw new Error('Canvas element required');
		if (!opts.deviceId) throw new Error('deviceId required');

		this.deviceId = opts.deviceId;
		this.opts = opts;

		const resolvedTimeouts = RDConnectionParams.resolveConnectTimeouts(opts.connection);
		this._rendezvousTimeoutMs =
			Number(opts.rendezvousTimeoutMs) > 0 ? Number(opts.rendezvousTimeoutMs) : resolvedTimeouts.rendezvousMs;
		this._signalRelayTimeoutMs =
			Number(opts.signalRelayTimeoutMs) > 0
				? Number(opts.signalRelayTimeoutMs)
				: resolvedTimeouts.signalRelayMs;
		this._connectionParams = resolvedTimeouts.connection;

		// Sub-modules
		this.conn = new RDConnection({ deviceId: this.deviceId });
		this.proto = new RDProtocol();
		this.crypto = new RDCrypto();
		this.video = new RDVideo();
		this.audio = new RDAudio();
		this.renderer = new RDRenderer(canvas);
		this.input = new RDInput(canvas, this.renderer, (msg) => this._sendPeerMessage(msg));
		this._fileConnection = null;
		this._sessionPassword = '';
		this.fileTransfer = new RDFileTransfer({
			proto: this.proto,
			sendMessage: (msg) => this._sendFileTransferMessage(msg),
			emit: (event, ...args) => this._emit(event, ...args),
			ensureConnected: () => this.ensureFileConnection(),
			isConnected: () => this.isFileConnectionReady()
		});

		// State
		this._state = 'idle'; // idle | connecting | waiting_password | waiting_2fa | authenticating | streaming | disconnected | error
		this._listeners = {};
		this._peerInfo = null;
		this._loginChallenge = null;
		this._pingInterval = null;
		this._statsInterval = null;

		// Rendezvous is bridged to TCP and needs BytesCodec reassembly.
		// Relay WebSocket messages are already complete payloads.
		this._rendezvousDecoder = null;

		// Codec / quality control
		this._codecAbilities = null; // probed VideoDecoder support map
		this._peerEncoding = null; // peer SupportedEncoding from handshake
		this._windowsSessions = { sessions: [], currentSid: 0 };
		this._preferCodec = opts.preferCodec || 'Auto';
		this._mobileRuntime = RDClient.isMobileRuntime();
		if (this._mobileRuntime && this._preferCodec === 'Auto') {
			// H.264 is the most consistently hardware-accelerated codec on
			// tablet browsers and remains available in the HTTP fallback.
			this._preferCodec = 'H264';
		}
		this._adaptivePaused = false; // true once the user picks codec/quality manually
		this._codecFallbackDone = false; // one automatic downgrade per session

		// Relay state tracking
		this._relayFrameIdx = 0; // Counter for relay frames (debugging)
		this._relayConfirmReceived = false; // Whether hbbr's RelayResponse confirmation was consumed
		this._peerEncryptionConfirmed = false; // Whether peer has started encrypting
		this._keyExchangeDone = false; // True after the SignedId checked out, PublicKey was sent and crypto enabled
		this._sessionStartedAt = null;
		this._lastDisconnect = null;
		this._lastError = null;
		// Bumped by disconnect(): a connect still on its way stops at its next step
		this._connectRun = 0;

		// Settings
		this.renderer.setScaleMode(opts.scaleMode || 'fit');

		// Multi-session viewer: the viewer toggles these when switching tabs
		this._sessionActive = true;
		this._clipboardToLocalEnabled = true;
		// Disable clipboard (viewer toggle): also kept here, a device may ignore the option
		this._clipboardDisabled = false;
		this._savedActiveFps = null;
		this._backgroundFps = 1;
		this._streamThrottledActive = null;
		this.video.getAudioContext = () => this.audio.audioCtx;

		// A watch-only guest link: view only from the start, and it stays on
		this._viewOnlyLocked = !!opts.viewOnly;
		this._viewOnly = this._viewOnlyLocked;
	}

	get state(): RDClientState {
		return this._state;
	}
	get peerInfo(): PeerInfo | null {
		return this._peerInfo;
	}
	/** True once the device's identity checked out and it sent its first encrypted frame */
	get encrypted(): boolean {
		return this._keyExchangeDone && this._peerEncryptionConfirmed;
	}

	// ---- Event Emitter ----

	on(event: string, fn: RDClientListener): this {
		if (!this._listeners[event]) this._listeners[event] = [];
		this._listeners[event].push(fn);
		return this;
	}

	off(event: string, fn: RDClientListener): this {
		const arr = this._listeners[event];
		if (arr) this._listeners[event] = arr.filter((f) => f !== fn);
		return this;
	}

	_emit(event: string, ...args: unknown[]): void {
		const arr = this._listeners[event];
		if (arr)
			arr.forEach((fn) => {
				try {
					Reflect.apply(fn, undefined, args);
				} catch (e) {
					console.error(e);
				}
			});
	}

	/** Verbose relay/auth logging (window.BetterDesk.debugRelay = true) */
	static isRelayDebug(): boolean {
		return typeof window !== 'undefined' && page().BetterDesk?.debugRelay === true;
	}

	static isMobileRuntime(): boolean {
		try {
			const capabilities = typeof window !== 'undefined' ? page().DeviceCapabilities : null;
			return !!(capabilities && capabilities.isTouch() && !capabilities.isPhone());
		} catch {
			return false;
		}
	}

	_debugRelay(...args: unknown[]): void {
		if (RDClient.isRelayDebug()) debugLog(...args);
	}

	_getInitialStreamFps(): number {
		const requested = Number(this.opts.fps) > 0 ? Number(this.opts.fps) : 60;
		// WebCodecs is unavailable on the HTTP path, and JMuxer/MSE on
		// mobile browsers is not reliable at 60fps. The toolbar can still
		// explicitly raise FPS after the session is stable.
		if (!RDVideo.isSupported() || this._mobileRuntime) {
			return Math.min(requested, 30);
		}
		return requested;
	}

	// ---- Main Connection Flow ----

	/**
	 * Start connection to remote device
	 * Flow: load proto → rendezvous → punch hole → relay → wait for SignedId → key exchange → encrypted session
	 *
	 * RustDesk handshake (after relay pairing):
	 *   1. Target sends SignedId (unencrypted, signed with Ed25519)
	 *   2. We verify, extract target's ephemeral Curve25519 pk
	 *   3. We generate keypair + symmetric key, encrypt symkey with NaCl box
	 *   4. We send PublicKey { our_pk, encrypted_symkey }
	 *   5. Target decrypts symkey, enables encryption
	 *   6. Target sends Hash (encrypted) - password challenge
	 *   7. We decrypt, show password prompt
	 */
	async connect(): Promise<void> {
		const run = ++this._connectRun;
		const cancelled = () => run !== this._connectRun;
		try {
			this._setState('connecting');
			this._emit('log', 'Loading protocol definitions...');

			// Step 1: Load protobuf definitions
			await this.proto.load();
			if (cancelled()) return;

			// Step 2: Check WebCodecs support (non-blocking, fallback available)
			if (!RDVideo.isSupported()) {
				this._emit('log', 'WebCodecs unavailable, using software fallback');
			}

			// Step 3: Create stream decoders for TCP frame reassembly
			this._rendezvousDecoder = this.proto.createStreamDecoder();

			// Step 4: Connect to rendezvous server via WS proxy
			this._emit('log', 'Connecting to rendezvous server...');
			if (this._connectionParams) {
				this._debugRelay(
					'[RDClient] Connection policy:',
					JSON.stringify({
						p2p_first: this._connectionParams.p2p_first,
						always_use_relay: this._connectionParams.always_use_relay,
						p2p_fallback_ms: this._connectionParams.p2p_fallback_ms,
						same_nat_relay: this._connectionParams.same_nat_relay,
						relay_servers: this._connectionParams.relay_servers || '',
						forceRelay: true,
						rendezvousTimeoutMs: this._rendezvousTimeoutMs,
						signalRelayTimeoutMs: this._signalRelayTimeoutMs
					})
				);
			}
			await this.conn.connectRendezvous();
			if (cancelled()) return;

			// Step 5: Send PunchHoleRequest (with server public key for licence validation)
			// Browser Web Remote always force_relay=true (no UDP hole punch).
			this._emit('log', `Requesting connection to ${this.deviceId}...`);
			const punchHole = this.proto.buildPunchHoleRequest(this.deviceId, this.opts.serverPubKey);
			const punchData = this.proto.encodeRendezvous(punchHole);
			this.conn.sendRendezvous(punchData);

			// Step 6: Wait for PunchHoleResponse / RelayResponse from hbbs
			const rendezvousResponse = await this._waitForRendezvousResponse();
			if (cancelled()) return;

			if (rendezvousResponse.error) {
				throw new Error(`Connection refused: ${rendezvousResponse.error}`);
			}

			// Store peer's server-signed pk for SignedId verification (from RelayResponse.pk)
			this._peerSignedPk = rendezvousResponse.pk || null;

			// Step 7: Determine relay UUID.
			//
			// PunchHoleResponse does NOT contain a UUID — only natType and relayServer.
			// The signal server expects us to send RequestRelay{uuid} back on the SAME
			// rendezvous connection so it can forward the UUID to the target device.
			// Both sides then connect to hbbr with the same UUID → relay pairs them.
			//
			// If we already received a RelayResponse (which has a UUID), skip this step.
			let relayUUID = rendezvousResponse.uuid || '';
			let relayServer = rendezvousResponse.relayServer || '';

			if (!relayUUID) {
				relayUUID =
					window.crypto && typeof window.crypto.randomUUID === 'function'
						? window.crypto.randomUUID()
						: (() => {
								const bytes = new Uint8Array(16);
								if (window.crypto && window.crypto.getRandomValues) {
									window.crypto.getRandomValues(bytes);
								} else {
									for (let i = 0; i < bytes.length; i++) bytes[i] = Math.floor(Math.random() * 256);
								}
								bytes[6] = ((bytes[6] ?? 0) & 0x0f) | 0x40;
								bytes[8] = ((bytes[8] ?? 0) & 0x3f) | 0x80;
								const hex = Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('');
								return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
							})();

				// Step 8: Send RequestRelay back to hbbs (signal server) via rendezvous
				// so it can tell the target device to connect to relay with our UUID.
				this._emit('log', `Requesting relay (uuid: ${relayUUID.substring(0, 8)}...)...`);
				const requestRelaySignal = this.proto.buildRequestRelay(
					this.deviceId,
					relayUUID,
					relayServer,
					this.opts.serverPubKey
				);
				const signalData = this.proto.encodeRendezvous(requestRelaySignal);
				this.conn.sendRendezvous(signalData);

				// Step 9: Wait for RelayResponse from hbbs confirming the relay setup
				const relayConfirm = await this._waitForSignalRelayResponse();
				if (cancelled()) return;
				if (relayConfirm.error) {
					throw new Error(`Relay refused: ${relayConfirm.error}`);
				}
				// Use the confirmed UUID and relay server from hbbs
				relayUUID = relayConfirm.uuid || relayUUID;
				relayServer = relayConfirm.relayServer || relayServer;
				if (relayConfirm.pk) {
					this._peerSignedPk = relayConfirm.pk;
				}
				this._debugRelay(
					`[RDClient] RelayResponse confirmed: uuid=${relayUUID.substring(0, 8)}... relay=${relayServer}`
				);
			}

			// Step 10: Close rendezvous, connect to relay
			this.conn.closeRendezvous();

			this._emit('log', 'Connecting to relay server...');
			await this.conn.connectRelay();
			if (cancelled()) return;

			// Step 11: Setup relay message handler BEFORE sending anything
			this.conn.on('relay:message', (data) => {
				// Binary messages are ArrayBuffers (the socket's binaryType)
				if (data instanceof ArrayBuffer) this._handleRelayData(data);
			});
			this.conn.on('relay:close', (code, wsReason, closeInfo) => {
				if (this._state !== 'disconnected' && this._state !== 'error') {
					const suffix =
						code || wsReason ? ` (WebSocket ${code || 0}${wsReason ? `: ${wsReason}` : ''})` : '';
					this._handleDisconnect('Relay connection closed' + suffix, {
						source: 'relay',
						closeCode: code || 0,
						closeReason: wsReason || '',
						closeInfo: closeInfo || null
					});
				}
			});
			this.conn.on('relay:error', (e) => {
				// A WebSocket error event carries no message
				const message = String(errorField(e, 'message') || 'the relay connection failed');
				this._handleDisconnect('Relay error: ' + message, {
					source: 'relay-error',
					error: message
				});
			});

			// Step 12: Send RequestRelay to hbbr (relay expects this as first message for pairing)
			this._emit('log', `Connecting to relay (uuid: ${relayUUID.substring(0, 8)}...)...`);
			const requestRelay = this.proto.buildRequestRelay(
				this.deviceId,
				relayUUID,
				relayServer,
				this.opts.serverPubKey
			);
			const relayData = this.proto.serializeRendezvous(requestRelay);
			this.conn.sendRelay(relayData);

			// Step 13: Wait for target's SignedId (first message from relay)
			// Target sends SignedId FIRST (unencrypted, signed with their Ed25519 key).
			// We do NOT send anything until we process SignedId and perform key exchange.
			this._emit('log', 'Waiting for peer handshake...');
			this._setState('waiting_password');
		} catch (err) {
			// Cancel or a closed tab ended it: no error to show
			if (cancelled()) return;
			this._handleError(err);
		}
	}

	/**
	 * Whether the dedicated FILE_TRANSFER relay (opened lazily, see
	 * ensureFileConnection) is signed in and ready.
	 */
	isFileConnectionReady(): boolean {
		return !!(this._fileConnection && this._fileConnection.state === 'ready');
	}

	/** compress and file-connection are imported modules: nothing to load at run time */
	_loadFileTransferRuntime(): Promise<void> {
		return Promise.resolve();
	}

	/**
	 * Open dedicated FILE_TRANSFER relay (lazy). Reuses desktop session password.
	 */
	async ensureFileConnection(): Promise<void> {
		await this._loadFileTransferRuntime();
		if (!this.proto.loaded) await this.proto.load();
		if (!this._fileConnection) {
			this._fileConnection = new RDFileConnection({
				deviceId: this.deviceId,
				serverPubKey: this.opts.serverPubKey || '',
				myName: this.opts.myName || 'BetterDesk Web',
				proto: this.proto,
				connection: this._connectionParams || this.opts.connection || null,
				rendezvousTimeoutMs: this._rendezvousTimeoutMs,
				signalRelayTimeoutMs: this._signalRelayTimeoutMs
			});
			this._fileConnection.on('file_response', (resp: FileResponse) => {
				this.fileTransfer.handleFileResponse(resp);
			});
			this._fileConnection.on('file_action', (action: FileAction) => {
				if (action.sendConfirm) {
					this.fileTransfer.handleSendConfirm(action.sendConfirm);
				}
			});
			// The file transfer session signs in on its own: its questions go to the file sheet
			this._fileConnection.on('2fa_required', () => this._emit('file_2fa_required'));
			this._fileConnection.on('2fa_error', (err: unknown) => this._emit('file_2fa_error', err));
			this._fileConnection.on('login_error', (err: unknown) => this._emit('file_login_error', err));
			this._fileConnection.on('disconnected', () => {
				if (this._fileConnection && this._fileConnection.state !== 'ready') {
					this._fileConnection = null;
				}
			});
		}
		if (this._fileConnection.state === 'ready') return;
		try {
			await this._fileConnection.connect(this._sessionPassword || '');
		} catch (err) {
			this.disconnectFileConnection();
			throw err;
		}
	}

	_sendFileTransferMessage(msgObj: MessageInit): void {
		if (!this._fileConnection || this._fileConnection.state !== 'ready') {
			throw new Error('File transfer session is not connected');
		}
		this._fileConnection.sendMessage(msgObj);
	}

	/**
	 * The second factor the file transfer session asked for (file_2fa_required)
	 * @param code 6-digit TOTP code
	 */
	submitFile2FA(code: string): void {
		if (this._fileConnection) this._fileConnection.submit2FA(String(code || '').trim());
	}

	disconnectFileConnection(): void {
		if (this._fileConnection) {
			this._fileConnection.disconnect();
			this._fileConnection = null;
		}
	}

	/**
	 * Authenticate with password
	 */
	async authenticate(password: string): Promise<void> {
		try {
			// The password hash never goes out unencrypted
			if (!this._keyExchangeDone) {
				throw new Error('Handshake failed: the connection is not encrypted');
			}
			this._setState('authenticating');
			this._emit('log', 'Authenticating...');
			this._sessionPassword = password != null ? String(password) : '';

			// Hash the password
			const challenge = this._loginChallenge || '';
			const salt = this._loginSalt || '';
			this._debugRelay(
				'[RDClient] Auth: challenge=' +
					JSON.stringify(challenge).substring(0, 80) +
					' salt=' +
					JSON.stringify(salt) +
					' passLen=' +
					password.length
			);

			const hash = await this.crypto.hashPassword(password, salt, challenge);
			this._debugRelay(
				'[RDClient] Auth: hash=' +
					Array.from(hash.slice(0, 8))
						.map((b) => b.toString(16).padStart(2, '0'))
						.join('') +
					'... (' +
					hash.length +
					' bytes)'
			);

			// Probe which codecs this browser's VideoDecoder can actually decode so we
			// only advertise real abilities (avoids the peer sending a codec we can't decode).
			if (!this._codecAbilities) {
				try {
					this._codecAbilities = await RDVideo.getSupportedCodecs();
				} catch {
					this._codecAbilities = null;
				}
			}

			// Build and send LoginRequest
			// username must be set to target device ID (RustDesk validates: is_ip || is_domain_port || == Config::get_id())
			const loginReq = this.proto.buildLoginRequest(hash, {
				username: this.deviceId,
				myId: 'betterdesk-web-' + Date.now().toString(36),
				myName: this.opts.myName || this.opts.myName || 'BetterDesk Web',
				disableAudio: this.opts.disableAudio || false,
				fps: this._getInitialStreamFps(),
				imageQuality: this.opts.imageQuality || 'Best',
				codecAbilities: this._codecAbilities,
				preferCodec: this._preferCodec,
				disableKeyboard: this._viewOnlyLocked
			});

			this._debugRelay(
				'[RDClient] Auth: sending LoginRequest, crypto.enabled=' +
					this.crypto.enabled +
					' sendSeq=' +
					this.crypto._sendSeq +
					' relayWsState=' +
					this.conn.relayWs?.readyState
			);
			this._sendPeerMessage(loginReq);
			this._debugRelay('[RDClient] Auth: LoginRequest sent, sendSeq now=' + this.crypto._sendSeq);

			// The response will be handled in _handleRelayMessage
		} catch (err) {
			this._handleError(err);
		}
	}

	/**
	 * Submit 2FA verification code (TOTP)
	 * @param code 6-digit TOTP code
	 */
	submit2FA(code: string): void {
		try {
			this._setState('authenticating');
			this._emit('log', 'Verifying 2FA code...');

			// Field must be auth_2fa — auth2Fa encodes as an empty Message (protobuf.js quirk).
			this._sendPeerMessage(this.proto.buildAuth2FA(code.trim()));
			this._debugRelay('[RDClient] Auth2FA sent');
		} catch (err) {
			this._handleError(err);
		}
	}

	/**
	 * Disconnect from remote device
	 */
	disconnect(): void {
		this._connectRun++;
		this._cleanup();
		this._setState('disconnected');
		this._emit('log', 'Disconnected');
	}

	// ---- Message Handling ----

	/**
	 * Wait for rendezvous server response (PunchHoleResponse or RelayResponse)
	 *
	 * Flow: After PunchHoleRequest, hbbs either:
	 * - Sends PunchHoleResponse(failure) immediately if target not found/offline
	 * - Forwards PunchHole to target peer, then later forwards RelayResponse
	 *   (from target peer) back to us through the same TCP connection
	 */
	_waitForRendezvousResponse(): Promise<RendezvousAnswer> {
		return new Promise((resolve, reject) => {
			const waitMs = this._rendezvousTimeoutMs || 30000;
			const timeout = setTimeout(() => {
				this.conn.off('rendezvous:message', handler);
				reject(
					new Error(
						`Rendezvous response timeout (${Math.round(waitMs / 1000)}s) - target device may be offline`
					)
				);
			}, waitMs);

			const handler = (rawData: unknown) => {
				// Decode frames from raw TCP data via stream decoder (binary messages are ArrayBuffers)
				if (!(rawData instanceof ArrayBuffer)) return;
				const frames = this._rendezvousDecoder?.feed(rawData) ?? [];
				if (frames.length === 0) return; // Incomplete frame, wait for more data

				// Process ALL decoded frames — the first may be a KeyExchange
				// from the server's secure TCP handshake; we skip it and wait
				// for the actual PunchHoleResponse or RelayResponse.
				for (const frame of frames) {
					try {
						const msg = this.proto.decodeRendezvous(frame);

						// Skip KeyExchange from the Go signal server's NaCl
						// secure TCP negotiation — the WS proxy bridges raw TCP
						// bytes so we see the server's greeting before our response.
						if (msg.keyExchange) {
							this._debugRelay('[RDClient] Skipping server KeyExchange (secure TCP greeting)');
							continue;
						}

						// Skip HealthCheck and other housekeeping messages
						if (msg.hc) {
							continue;
						}

						if (msg.punchHoleResponse) {
							clearTimeout(timeout);
							this.conn.off('rendezvous:message', handler);
							const resp = msg.punchHoleResponse;
							this._debugRelay(
								'[RDClient] PunchHoleResponse:',
								JSON.stringify({
									failure: resp.failure,
									relayServer: resp.relayServer,
									otherFailure: resp.otherFailure,
									hasSocketAddr: !!(resp.socketAddr && resp.socketAddr.length),
									hasPk: !!(resp.pk && resp.pk.length),
									natType: resp.natType
								})
							);
							// Proto3 default failure=0 (ID_NOT_EXIST); #405 may set OFFLINE
							// while still filling relay_server — honor explicit failures.
							resolve(RDPunchHoleResponse.interpretPunchHoleResponse(resp));
							return;
						}

						if (msg.relayResponse) {
							clearTimeout(timeout);
							this.conn.off('rendezvous:message', handler);
							const rr = msg.relayResponse;
							this._debugRelay(
								'[RDClient] RelayResponse from hbbs:',
								JSON.stringify({
									relayServer: rr.relayServer || '',
									uuid: (rr.uuid || '').substring(0, 8) + '...',
									id: rr.id || '',
									hasPk: !!(rr.pk && rr.pk.length),
									refuseReason: rr.refuseReason || ''
								})
							);
							if (rr.refuseReason && rr.refuseReason.length > 0) {
								resolve({ error: 'Relay refused: ' + rr.refuseReason });
							} else {
								resolve({
									relayServer: rr.relayServer || '',
									uuid: rr.uuid || '',
									pk: rr.pk || null,
									id: rr.id || ''
								});
							}
							return;
						}

						// Unknown message type — log and skip, keep waiting
						const fieldNames = Object.keys(msg).filter((k) => Reflect.get(msg, k) != null && k !== 'union');
						this._debugRelay('[RDClient] Skipping rendezvous message:', fieldNames.join(', ') || 'empty');
					} catch (err) {
						// Protobuf decode error — skip this frame and continue
						console.warn(
							'[RDClient] Failed to decode rendezvous frame, skipping:',
							errorField(err, 'message')
						);
					}
				}
			};

			this.conn.on('rendezvous:message', handler);
		});
	}

	/**
	 * Wait for RelayResponse from signal server (hbbs) after sending RequestRelay.
	 *
	 * After PunchHoleResponse (natType=SYMMETRIC), we send RequestRelay{uuid} back
	 * to hbbs on the same rendezvous connection. hbbs forwards the request to the
	 * target device (tells it to connect to relay with our UUID) and sends back a
	 * RelayResponse confirming the UUID and relay server.
	 */
	_waitForSignalRelayResponse(): Promise<RendezvousAnswer> {
		return new Promise((resolve, reject) => {
			const waitMs = this._signalRelayTimeoutMs || 15000;
			const timeout = setTimeout(() => {
				this.conn.off('rendezvous:message', handler);
				reject(
					new Error(
						`RelayResponse timeout (${Math.round(waitMs / 1000)}s) — target device may be unreachable`
					)
				);
			}, waitMs);

			const handler = (rawData: unknown) => {
				if (!(rawData instanceof ArrayBuffer)) return;
				const frames = this._rendezvousDecoder?.feed(rawData) ?? [];
				if (frames.length === 0) return;

				for (const frame of frames) {
					try {
						const msg = this.proto.decodeRendezvous(frame);

						// Skip KeyExchange, HealthCheck, and other housekeeping
						if (msg.keyExchange || msg.hc) continue;

						if (msg.relayResponse) {
							clearTimeout(timeout);
							this.conn.off('rendezvous:message', handler);
							const rr = msg.relayResponse;
							this._debugRelay(
								'[RDClient] Signal RelayResponse:',
								JSON.stringify({
									relayServer: rr.relayServer || '',
									uuid: (rr.uuid || '').substring(0, 8) + '...',
									hasPk: !!(rr.pk && rr.pk.length),
									refuseReason: rr.refuseReason || ''
								})
							);
							if (rr.refuseReason && rr.refuseReason.length > 0) {
								resolve({ error: rr.refuseReason });
							} else {
								resolve({
									uuid: rr.uuid || '',
									relayServer: rr.relayServer || '',
									pk: rr.pk || null
								});
							}
							return;
						}

						// PunchHoleSent may arrive from target — it's just an update,
						// not what we are waiting for. Log and continue.
						if (msg.punchHoleResponse || msg.punchHoleSent) {
							this._debugRelay(
								'[RDClient] Skipping late PunchHoleResponse/Sent while waiting for RelayResponse'
							);
							continue;
						}

						const fieldNames = Object.keys(msg).filter((k) => Reflect.get(msg, k) != null && k !== 'union');
						this._debugRelay(
							'[RDClient] Skipping signal message while waiting for RelayResponse:',
							fieldNames.join(', ')
						);
					} catch (err) {
						console.warn('[RDClient] Failed to decode signal frame:', errorField(err, 'message'));
					}
				}
			};

			this.conn.on('rendezvous:message', handler);
		});
	}

	/**
	 * Handle one raw RustDesk payload from the native relay WebSocket.
	 *
	 * Mixed WebSocket/TCP sessions are translated by hbbr, so the browser
	 * must not add or decode a TCP length header here.
	 */
	_handleRelayData(rawData: ArrayBuffer | ArrayLike<number>): void {
		try {
			const payload = rawData instanceof ArrayBuffer ? new Uint8Array(rawData) : new Uint8Array(rawData);
			if (payload.length > 0) this._handleRelayMessage(payload);
		} catch (err) {
			console.warn('[RDClient] Error decoding relay data:', errorField(err, 'message'));
		}
	}

	/**
	 * Handle a single decoded relay frame (protobuf bytes).
	 *
	 * Frame sequence on a relay connection (RustDesk's secure_connection):
	 *   #1: the device's SignedId (Message, unencrypted). We check it against
	 *       the server key and the device ID, answer with PublicKey at once and
	 *       turn encryption on.
	 *   #2+: everything else, encrypted with the session key we sent.
	 *
	 * The connection fails closed: any other first message, a SignedId that
	 * does not check out, or a frame that does not decrypt before the device's
	 * first encrypted one ends it before the password prompt.
	 *
	 * @param frameData Raw protobuf bytes from one WS message
	 */
	_handleRelayMessage(frameData: Uint8Array): void {
		try {
			if (this._state === 'error' || this._state === 'disconnected') return;
			this._relayFrameIdx++;
			const idx = this._relayFrameIdx;
			if (RDClient.isRelayDebug()) {
				const hex20 = Array.from(frameData.slice(0, 20))
					.map((b) => b.toString(16).padStart(2, '0'))
					.join(' ');
				this._debugRelay(`[RDClient] Relay frame #${idx}: ${frameData.length} bytes [${hex20}]`);
			}

			// Tolerate older servers that injected a RelayResponse first.
			if (!this._relayConfirmReceived) {
				this._relayConfirmReceived = true;
				try {
					const rdvMsg = this.proto.decodeRendezvous(frameData);
					if (rdvMsg.relayResponse) {
						const uuid = (rdvMsg.relayResponse.uuid || '').substring(0, 8);
						this._debugRelay(`[RDClient] Relay confirmation received (UUID: ${uuid}...), skipping`);
						return;
					}
				} catch {
					this._debugRelay('[RDClient] First relay frame is not a relay confirmation');
				}
			}

			// Until the device has proved who it is, only its SignedId is accepted
			if (!this._keyExchangeDone) {
				let first: Message | null;
				try {
					first = this.proto.decodeMessage(frameData);
				} catch {
					first = null;
				}
				if (!first || !first.signedId) {
					this._handleError(new Error('Handshake failed: the device did not identify itself'));
					return;
				}
				this._handleSignedId(first.signedId);
				return;
			}

			let data = frameData;
			const spec = this.crypto.tryDecrypt(new Uint8Array(data));
			if (spec) {
				this.crypto.commitDecrypt(spec.seq);
				data = spec.plaintext;
				if (!this._peerEncryptionConfirmed) {
					this._peerEncryptionConfirmed = true;
					this._debugRelay(`[RDClient] Peer encryption confirmed at frame #${idx} (seq=${spec.seq})`);
				}
			} else if (this._peerEncryptionConfirmed) {
				console.warn(
					`[RDClient] Decryption FAILED (peer was encrypting)` +
						` nextSeq=${this.crypto._recvSeq + 1} frameLen=${frameData.length}`
				);
				return;
			} else {
				this._handleError(new Error('Handshake failed: the device did not encrypt the connection'));
				return;
			}

			const msg = this.proto.decodeMessage(data);
			const fields = Object.keys(msg).filter((k) => Reflect.get(msg, k) != null && k !== 'union');
			if (idx <= 10 || fields.length === 0) {
				this._debugRelay(`[RDClient] Frame #${idx} → ${fields.join(', ') || '(empty Message)'}`);
			}
			this._dispatchMessage(msg);
		} catch (err) {
			console.warn(
				'[RDClient] Error handling relay message:',
				errorField(err, 'message'),
				errorField(err, 'stack')
			);
		}
	}

	/**
	 * Dispatch decoded peer message to appropriate handler
	 */
	_dispatchMessage(msg: Message): void {
		// Hash challenge (before login)
		if (msg.hash) {
			this._loginChallenge = msg.hash.challenge || '';
			this._loginSalt = msg.hash.salt || '';
			this._emit('log', 'Password required');
			this._setState('waiting_password');
			this._emit('password_required');
			return;
		}

		// Login response
		if (msg.loginResponse) {
			this._handleLoginResponse(msg.loginResponse);
			return;
		}

		// Video frame
		if (msg.videoFrame) {
			this._handleVideoFrame(msg.videoFrame);
			return;
		}

		// Audio frame
		if (msg.audioFrame) {
			this._handleAudioFrame(msg.audioFrame);
			return;
		}

		// Cursor data (cursor image)
		if (msg.cursorData) {
			this.renderer.updateCursor(msg.cursorData).catch(() => {
				// Handled inside updateCursor — ignore unhandled promise rejection
			});
			return;
		}

		// Cursor position
		if (msg.cursorPosition) {
			this.renderer.updateCursorPosition(msg.cursorPosition);
			return;
		}

		// Cursor ID (predefined cursor)
		if (msg.cursorId) {
			this._emit('cursor_id', msg.cursorId);
			return;
		}

		// Clipboard (legacy single entry)
		if (msg.clipboard) {
			this._handleClipboard(msg.clipboard);
			return;
		}

		// Multi-format clipboard (RustDesk >= 1.3.0)
		if (msg.multiClipboards) {
			this._handleMultiClipboards(msg.multiClipboards);
			return;
		}

		// Test delay (ping/pong)
		if (msg.testDelay) {
			this._handleTestDelay(msg.testDelay);
			return;
		}

		// Misc messages
		if (msg.misc) {
			this._handleMisc(msg.misc);
			return;
		}

		// Peer info
		if (msg.peerInfo) {
			this._handlePeerInfo(msg.peerInfo);
			return;
		}

		// Public key from peer
		if (msg.publicKey) {
			this._handlePeerPublicKey(msg.publicKey);
			return;
		}

		// File response (directory listing, transfer blocks, digest, done, error)
		if (msg.fileResponse) {
			this.fileTransfer.handleFileResponse(msg.fileResponse);
			return;
		}

		// Cliprdr file clipboard (Explorer copy → remote paste on desktop)
		if (msg.cliprdr) {
			void this._handleCliprdr(msg.cliprdr);
			return;
		}
	}

	// ---- Specific Message Handlers ----

	_handlePeerPublicKey(_pk: PublicKey): void {
		// This handler is for the case where the peer sends PublicKey
		// (non-standard flow). In standard RustDesk flow, the target
		// sends SignedId first, and WE send PublicKey back.
		this._debugRelay('[RDClient] Received unexpected PublicKey from peer');
	}

	/**
	 * Handle the device's SignedId, the first message on the relay.
	 * SignedId.id = 64-byte Ed25519 signature + protobuf(IdPk{ id, pk })
	 * where pk is the device's ephemeral Curve25519 public key.
	 *
	 * The identity chain (RustDesk decode_id_pk) must hold, or the connection
	 * ends: RelayResponse.pk is IdPk{id, identityPk} signed by the server key,
	 * and the SignedId is IdPk{id, boxPk} signed by that identity key, both for
	 * the ID asked for. Then we send PublicKey with a fresh session key and
	 * encrypt everything after it.
	 */
	_handleSignedId(signedId: SignedId): void {
		const checked = RDCrypto.checkPeer(
			signedId.id,
			this._peerSignedPk,
			this.opts.serverPubKey || '',
			this.deviceId,
			this.proto.types.IdPk
		);
		if ('error' in checked) {
			this._handleError(new Error(checked.error));
			return;
		}
		this._emit('log', 'Peer identified: ' + this.deviceId);
		this._emit('log', 'Peer identity verified (Ed25519)');

		this.crypto.generateKeyPair();
		this.crypto.generateSymmetricKey();
		this.crypto.setPeerPublicKey(checked.peerPk);
		this._completeKeyExchange();
	}

	/**
	 * Send PublicKey (our ephemeral key and the session key sealed for the
	 * device) and turn on encryption for everything after it.
	 */
	_completeKeyExchange(): void {
		if (this._keyExchangeDone) return;
		// Set by _handleSignedId just before
		const peerPk = this.crypto.peerPk;
		if (!peerPk) return;
		const keyMsg = this.crypto.createSymmetricKeyMsg(peerPk);
		this._debugRelay(`[RDClient] Completing key exchange: sealed=${keyMsg.symmetricValue.length} bytes`);

		const pkMsg = this.proto.buildPublicKey(keyMsg.asymmetricValue, keyMsg.symmetricValue);
		this._sendPeerMessage(pkMsg);

		// Enable outgoing encryption (counters start at 0)
		this.crypto.enabled = true;
		this.crypto._sendSeq = 0;
		this.crypto._recvSeq = 0;

		this._keyExchangeDone = true;
		this._debugRelay('[RDClient] Key exchange completed: encryption enabled');
	}

	_is2faWrongError(error: string | null | undefined): boolean {
		const errLower = (error || '').toLowerCase();
		return errLower.includes('wrong 2fa') || errLower.includes('invalid 2fa');
	}

	_is2faRequiredError(error: string | null | undefined): boolean {
		if (this._is2faWrongError(error)) return false;
		const errLower = (error || '').toLowerCase();
		return (
			errLower.includes('2fa required') || errLower.includes('totp') || errLower.includes('verification code')
		);
	}

	_handleLoginResponse(resp: LoginResponse): void {
		this._debugRelay(
			'[RDClient] LoginResponse:',
			JSON.stringify(resp, (_k: string, v: unknown) => {
				if (v && typeof v === 'object' && 'type' in v && v.type === 'Buffer') return '<Buffer>';
				if (v instanceof Uint8Array) return '<bytes:' + v.length + '>';
				return v;
			}).substring(0, 500)
		);

		if (resp.error && resp.error.length > 0) {
			this._debugRelay('[RDClient] Login error: ' + resp.error);

			// RustDesk peer errors: REQUIRE_2FA = "2FA Required", LOGIN_MSG_2FA_WRONG = "Wrong 2FA Code"
			if (this._is2faWrongError(resp.error)) {
				this._debugRelay('[RDClient] 2FA code rejected: ' + resp.error);
				this._setState('waiting_2fa');
				this._emit('2fa_error', resp.error);
				return;
			}
			if (this._is2faRequiredError(resp.error)) {
				this._debugRelay('[RDClient] 2FA required by peer');
				this._setState('waiting_2fa');
				this._emit('2fa_required');
				return;
			}

			this._emit('login_error', resp.error);
			this._setState('waiting_password');
			return;
		}

		// Login successful
		this._peerInfo = resp.peerInfo || null;
		this._debugRelay('[RDClient] Login successful, peerInfo:', this._peerInfo ? 'present' : 'null');
		this._processPeerInfo(this._peerInfo);
		this._emit('log', 'Login successful');
		this._emit('login_success', resp);
		if (this._peerInfo) this._emit('peer_info', this._peerInfo);
		this._startSession();
	}

	_handlePeerInfo(info: PeerInfo): void {
		this._peerInfo = info;
		this._processPeerInfo(info);
		this._emit('peer_info', info);

		// If we got peer info without hash challenge, session can start
		if (this._state === 'waiting_password') {
			// Some devices don't require password
			this._emit('log', 'No password required');
			this._startSession();
		}
	}

	async _handleVideoFrame(videoFrame: VideoFrame): Promise<void> {
		// Send video_received ack IMMEDIATELY before any decoding
		// Without this, RustDesk peer throttles down to 1-5 FPS
		// Sending before decode ensures the ack goes out even if decode is slow
		this._sendPeerMessage(this.proto.buildMisc('videoReceived', true));

		// Track total video frames from peer for diagnostics
		this._peerFrameCount = (this._peerFrameCount || 0) + 1;
		this._lastVideoFrameTime = Date.now();
		if (this._peerFrameCount <= 3 || this._peerFrameCount % 300 === 0) {
			this._debugRelay('[RDClient] VideoFrame #' + this._peerFrameCount + ' from peer');
		}

		const codec = this.proto.detectVideoCodec(videoFrame);
		if (!codec || codec === 'rgb' || codec === 'yuv') return;

		// Initialize video decoder if needed
		if (!this.video.initialized || this.video.currentCodec !== codec) {
			try {
				await this.video.init(codec);
				this._emit('log', `Video codec: ${codec.toUpperCase()}`);
			} catch (err) {
				this._emit('log', `Video codec ${codec} not supported: ${errorField(err, 'message')}`);
				return;
			}
		}

		// Decode each encoded frame
		const frames = this.proto.getEncodedFrames(videoFrame);
		for (const frame of frames) {
			await this.video.decode(frame);
		}
	}

	_handleAudioFrame(audioFrame: AudioFrame): void {
		if (audioFrame.data) {
			this.audio.play({
				data: audioFrame.data,
				timestamp: 0
			});
		}
	}

	async _applyRemoteClipboard(clipboards: IClipboard[] | null | undefined): Promise<void> {
		// Decode only what will be written: the active tab, clipboard on
		if (this._viewOnly || this._clipboardDisabled || !this._clipboardToLocalEnabled) return;
		const list = clipboards || [];
		if (!list.length) return;

		const decoded = await RDClipboard.decodeEntries(list);
		const text = RDClipboard.pickBestText(decoded);
		const entry = {
			text,
			html: decoded.find((e) => e.format === 'html' && e.html)?.html || '',
			pngBlob: decoded.find((e) => e.pngBlob)?.pngBlob || null
		};
		const hub = typeof window !== 'undefined' && page().rdClipboardHub;
		if (hub) hub.setFromRemote(entry, this.deviceId);
		if (text) {
			this._emit('clipboard', text);
		}

		if (this._clipboardToLocalEnabled && !this._clipboardDisabled) {
			const status = await RDClipboard.writeLocalRich(entry);
			this._emit('clipboard_status', status, entry);
		}
	}

	_handleClipboard(clipboard: Clipboard): void {
		void this._applyRemoteClipboard([clipboard]);
	}

	_handleMultiClipboards(multiClipboards: MultiClipboards): void {
		const list = multiClipboards && multiClipboards.clipboards ? multiClipboards.clipboards : [];
		void this._applyRemoteClipboard(list);
	}

	_handleTestDelay(testDelay: TestDelay): void {
		if (!testDelay.fromClient) {
			// This is the controlled peer's QoS probe. RustDesk's video QoS
			// controller measures the round-trip delay of this exact message to
			// size the target bitrate AND framerate. We MUST echo it back
			// verbatim (same `time`, `from_client` stays false, original
			// last_delay/target_bitrate). Replying with a fresh timestamp makes
			// the peer compute a bogus delay and throttle the stream down to
			// ~1 fps. Echoing correctly keeps the full 24-30 fps stream.
			this._sendPeerMessage({
				testDelay: {
					time: testDelay.time,
					fromClient: false,
					lastDelay: testDelay.lastDelay || 0,
					targetBitrate: testDelay.targetBitrate || 0
				}
			});
		} else {
			// Our own ping came back - calculate RTT
			const rtt = Date.now() - (testDelay.time || 0);
			this._emit('latency', rtt);
		}
	}

	_handleMisc(misc: Misc): void {
		if (misc.closeReason) {
			this._handleDisconnect('Remote: ' + misc.closeReason, {
				source: 'peer',
				peerReason: String(misc.closeReason)
			});
			return;
		}
		if (misc.chatMessage) {
			this._emit('chat', misc.chatMessage.text || '');
			return;
		}
		if (misc.option) {
			this._emit('option', misc.option);
			return;
		}
		if (misc.permissionInfo) {
			this._emit('permission', misc.permissionInfo);
			return;
		}
		if (misc.switchDisplay) {
			if (typeof misc.switchDisplay.display === 'number') {
				this._currentDisplay = misc.switchDisplay.display;
			}
			this._emit('switch_display', misc.switchDisplay);
			return;
		}
		if (misc.supportedEncoding) {
			this._peerEncoding = misc.supportedEncoding;
			this._emit('peer_encoding', this._peerEncoding);
			return;
		}
		// The format of the device's sound, sent before its first audio frame
		if (misc.audioFormat) {
			if (!this.opts.disableAudio && RDAudio.isSupported()) {
				this.audio
					.configure({
						sampleRate: Number(misc.audioFormat.sampleRate) || undefined,
						channels: Number(misc.audioFormat.channels) || undefined
					})
					.catch(() => {
						this._emit('log', 'Audio init failed');
					});
			}
			return;
		}
	}

	// ---- Session Management ----

	/**
	 * Start the streaming session after successful login
	 */
	_startSession(): void {
		this._setState('streaming');
		this._sessionStartedAt = Date.now();
		this._lastDisconnect = null;
		this._lastError = null;
		this._codecFallbackDone = false;
		this.conn.setConnected();

		// Enable file transfer
		this.fileTransfer.enable();
		// File transfer is opened lazily by the file modal. Keeping it out of
		// the initial mobile handshake avoids a second rendezvous + relay burst
		// that can hit the per-IP proxy limit and is unnecessary for desktop
		// control sessions.

		// Initialize video decoder callbacks
		this.video.onFrame = (frame) => this.renderer.pushFrame(frame);
		this.video.onError = (err) => this._emit('log', 'Video error: ' + (errorField(err, 'message') || err));

		// The decoder asks for a keyframe whenever it (re)configures or recovers
		// from an error; forward that as a refresh_video request to the peer.
		this.video.onNeedKeyframe = () => {
			if (this._state === 'streaming') {
				this._sendPeerMessage(this.proto.buildMisc('refreshVideo', true));
			}
		};
		this.video.onCodecFailed = (failedCodec) => {
			this._handleCodecFallback(failedCodec);
		};

		// Request keyframe on resize/fullscreen to fix blur
		this.renderer.onResizeRefresh = () => {
			this._sendPeerMessage(this.proto.buildMisc('refreshVideo', true));
		};

		// Signal CSS when remote cursor data is available (hide local crosshair)
		this.renderer.onCursorReady = (ready) => {
			const container = this.renderer.canvas.parentElement;
			if (container) {
				container.classList.toggle('has-remote-cursor', !!ready);
			}
		};

		// Start render loop
		this.renderer.startRenderLoop();

		// Input capture is started by the viewer via setSessionActive() for the active tab only

		// Initialize audio (will actually start on first audio data)
		if (!this.opts.disableAudio && RDAudio.isSupported()) {
			this.audio.init().catch(() => {
				this._emit('log', 'Audio init failed');
			});
		}

		// Tell peer our desired FPS and image quality after session establishment
		const fps = this._getInitialStreamFps();
		this._savedActiveFps = fps;
		const quality = this.opts.imageQuality || 'Best';
		this._sendPeerMessage(
			this.proto.buildOptionMisc({
				customFps: fps,
				imageQuality: quality
			})
		);
		this._sendPeerMessage(
			this.proto.buildMisc(
				'autoAdjustFps',
				!RDVideo.isSupported() ? 0 : this.opts.fpsMode === 'adaptive' ? 60 : 0
			)
		);

		// Proactively request an initial keyframe so the decoder can start
		// immediately even if we joined an already-running stream on a delta.
		this._sendPeerMessage(this.proto.buildMisc('refreshVideo', true));

		this._advertiseSupportedEncoding();

		if (RDCliprdr.isSupported()) {
			void RDCliprdr.initClient(this);
		}

		// Start ping interval
		this._pingInterval = setInterval(() => {
			if (this._state === 'streaming') {
				const ping = this.proto.buildTestDelay();
				this._sendPeerMessage(ping);
			}
		}, 3000);

		// Start stats reporting
		this._statsInterval = setInterval(() => {
			if (this._state === 'streaming') {
				this._emit('stats', this.getStats());
			}
		}, 1000);

		// Stall recovery. Two cases trigger a keyframe request:
		//  1. No VideoFrame at all from the peer for 3s.
		//  2. The peer keeps sending frames but the decoder produces no output
		//     (e.g. we joined mid-stream on delta frames) for 2s.
		this._lastDecodedCount = 0;
		this._lastDecodeProgressTime = Date.now();
		this._stallCheckInterval = setInterval(() => {
			if (this._state !== 'streaming') return;
			const now = Date.now();

			const decoded = this.video ? this.video.frameCount : 0;
			if (decoded !== this._lastDecodedCount) {
				this._lastDecodedCount = decoded;
				this._lastDecodeProgressTime = now;
			}

			const lastFrame = this._lastVideoFrameTime || 0;
			const noPeerFrames = lastFrame > 0 && now - lastFrame > 3000;
			const peerFramesButNoDecode =
				(this._peerFrameCount || 0) > 0 &&
				decoded === 0 &&
				now - (this._lastDecodeProgressTime ?? now) > 2000;

			if (noPeerFrames || peerFramesButNoDecode) {
				this._emit('log', 'Video stall detected, requesting keyframe');
				this._sendPeerMessage(this.proto.buildMisc('refreshVideo', true));
				if (this.video) {
					this.video._needKeyframe = true;
				}
				this._lastVideoFrameTime = now; // prevent rapid retries
				this._lastDecodeProgressTime = now;
			}
		}, 1500);

		this._lastVideoFrameTime = Date.now();

		// ---- Adaptive Quality ----
		// Measures decoded FPS and drop rate. Demotes quality under load,
		// promotes back up when the pipeline is healthy. Avoids the 3-7 FPS
		// pin that occurred when Best@60 overwhelmed the JMuxer fallback path.
		if (this.opts.adaptiveQuality !== false) {
			this._startAdaptiveQuality();
		}

		this._emit('session_start');
		if (!this._sessionActive) {
			this._streamThrottledActive = null;
			this._syncStreamThrottle();
		}
	}

	/**
	 * Adaptive quality monitor.
	 * Tiers (low to high): Low@30 < Balanced@30 < Balanced@45 < Best@45 < Best@60
	 * Demotes after 2 consecutive bad samples, promotes after 3 good.
	 */
	_startAdaptiveQuality(): void {
		const tiers: QualityTier[] = !RDVideo.isSupported()
			? [
					{ id: 0, quality: 'Low', fps: 30 },
					{ id: 1, quality: 'Balanced', fps: 30 }
				]
			: [
					{ id: 0, quality: 'Low', fps: 30 },
					{ id: 1, quality: 'Balanced', fps: 30 },
					{ id: 2, quality: 'Balanced', fps: 45 },
					{ id: 3, quality: 'Best', fps: 45 },
					{ id: 4, quality: 'Best', fps: 60 }
				];
		// Start near tier 1 (Balanced@30) to match opts
		let current = 1;
		let badCount = 0;
		let goodCount = 0;
		let lastChange = Date.now();

		const apply = (tier: QualityTier | undefined) => {
			try {
				if (!tier) return;
				this._sendPeerMessage(
					this.proto.buildOptionMisc({
						imageQuality: tier.quality,
						customFps: tier.fps
					})
				);
				this._emit('log', '[Adaptive] ' + tier.quality + '@' + tier.fps + 'fps');
				this._emit('quality_changed', tier.quality + '@' + tier.fps);
			} catch {
				/* ignore */
			}
		};

		this._adaptiveInterval = setInterval(() => {
			if (this._state !== 'streaming') return;
			if (!this._sessionActive) return;
			if (this._adaptivePaused) return; // user took manual control of quality/codec
			const stats = this.video.getStats();
			const fps = stats.videoFps || 0;
			const tier = tiers[current];
			if (!tier) return;
			const target = tier.fps;
			const now = Date.now();

			// Grace period after a change
			if (now - lastChange < 5000) return;

			// Bad sample: measured FPS is less than 60% of target and above zero
			const bad = fps > 0 && fps < target * 0.6;
			// Good sample: measured FPS is within 85% of target
			const good = fps >= target * 0.85;

			if (bad) {
				badCount++;
				goodCount = 0;
				if (badCount >= 2 && current > 0) {
					current--;
					badCount = 0;
					lastChange = now;
					apply(tiers[current]);
				}
			} else if (good) {
				goodCount++;
				badCount = 0;
				if (goodCount >= 3 && current < tiers.length - 1) {
					current++;
					goodCount = 0;
					lastChange = now;
					apply(tiers[current]);
				}
			} else {
				// Neutral sample: decay counters
				if (badCount > 0) badCount--;
				if (goodCount > 0) goodCount--;
			}
		}, 2000);
	}

	// ---- Send Helpers ----

	/**
	 * Send a peer-to-peer message through the relay
	 * Order: serialize protobuf → encrypt (if enabled) → frame → send
	 * @param msgObj Message object (will be encoded as Message protobuf)
	 */
	_sendPeerMessage(msgObj: MessageInit): void {
		if (!this.proto.loaded) {
			if (msgObj && (msgObj.clipboard || msgObj.cliprdr)) {
				console.warn('[RDClient] Dropped clipboard/cliprdr message — proto not loaded yet');
			}
			return;
		}
		if (msgObj && (msgObj.clipboard || msgObj.cliprdr)) {
			this._debugRelay(
				'[RDClient] Sending',
				msgObj.clipboard ? 'clipboard' : 'cliprdr.' + Object.keys(msgObj.cliprdr ?? {})[0],
				msgObj
			);
		}

		// Step 1: Serialize to raw protobuf bytes (no frame header)
		let data = this.proto.serializeMessage(msgObj);

		// Step 2: Encrypt if enabled (encrypts the raw protobuf)
		if (this.crypto.enabled) {
			data = this.crypto.processOutgoing(data);
		}

		// Native relay WebSocket framing carries one raw payload per message.
		this.conn.sendRelay(data);
	}

	// ---- State & Cleanup ----

	_setState(state: RDClientState): void {
		if (this._state !== state) {
			const prev = this._state;
			this._state = state;
			this._emit('state', state, prev);
		}
	}

	_handleError(err: unknown): void {
		console.error('[RDClient]', err);
		const message = errorField(err, 'message');
		const msg = err && message ? String(message) : String(err);
		this._lastError = {
			message: msg,
			at: Date.now(),
			state: this._state,
			codec: this.video ? this.video.currentCodec : null
		};
		this._debugRelay('[RDClient] Session error:', JSON.stringify(this._lastError));
		this._emit('diagnostics', { type: 'error', ...this._lastError });
		// Detect peer-offline scenarios where the agent is reachable through
		// bd-signal/CDAP but not through the RustDesk relay (no peer registration).
		// In that case, signal the UI that a CDAP fallback viewer is available.
		const offlineHint = /target offline|relay refused|peer.*offline|not online|not registered/i.test(msg);
		this._emit('error', msg, { cdapFallback: offlineHint });
		if (offlineHint) {
			this._emit('cdap_fallback_available', this.deviceId);
		}
		this._cleanup();
		this._setState('error');
	}

	_handleDisconnect(reason: string, meta: RDClientDisconnectMeta = {}): void {
		const stateBefore = this._state;
		const info: RDClientDisconnectInfo = {
			type: 'disconnect',
			reason: String(reason || 'Connection closed'),
			source: meta.source || 'unknown',
			stateBefore,
			at: Date.now(),
			durationMs: this._sessionStartedAt ? Math.max(0, Date.now() - this._sessionStartedAt) : null,
			transport: 'rd',
			secureContext: typeof window !== 'undefined' ? window.isSecureContext === true : null,
			codec: this.video ? this.video.currentCodec : null,
			closeCode: meta.closeCode || null,
			closeReason: meta.closeReason || '',
			peerReason: meta.peerReason || '',
			error: meta.error || ''
		};
		this._lastDisconnect = info;
		this._debugRelay('[RDClient] Session disconnect:', JSON.stringify(info));
		this._emit('diagnostics', info);
		this._emit('log', `Disconnected: ${reason}`);
		this._cleanup();
		this._setState('disconnected');
		this._emit('disconnected', reason, info);
	}

	_cleanup(): void {
		if (this._pingInterval) {
			clearInterval(this._pingInterval);
			this._pingInterval = null;
		}
		if (this._statsInterval) {
			clearInterval(this._statsInterval);
			this._statsInterval = null;
		}
		if (this._stallCheckInterval) {
			clearInterval(this._stallCheckInterval);
			this._stallCheckInterval = null;
		}
		if (this._adaptiveInterval) {
			clearInterval(this._adaptiveInterval);
			this._adaptiveInterval = null;
		}

		this.input.stop();
		this.renderer.stopRenderLoop();
		this.video.close();
		this.audio.close();
		this.fileTransfer.disable();
		this.disconnectFileConnection();
		this._sessionPassword = '';
		this.conn.close();
		this._codecFallbackDone = false;
		if (this._rendezvousDecoder) {
			this._rendezvousDecoder.reset();
		}
		if (RDCliprdr.isSupported()) {
			RDCliprdr.stopPolling(this);
			void RDCliprdr.clearLocalCache();
		}
	}

	// ---- Public Utility Methods ----

	/**
	 * Send clipboard text or a rich clipboard entry to remote
	 */
	sendClipboard(value: string | RDClientClipboardEntry): void {
		if (this._state !== 'streaming') return;
		if (this._viewOnly) return;
		void this._sendClipboard(value);
	}

	async _sendClipboard(value: string | RDClientClipboardEntry | null | undefined): Promise<void> {
		const entry: RDClientClipboardEntry = typeof value === 'string' ? { text: value } : value || {};
		try {
			const msg =
				entry.html || entry.pngBlob
					? await this.proto.buildMultiClipboards(entry)
					: await this.proto.buildClipboard(entry.text || '');
			this._sendPeerMessage(msg);
		} catch (err) {
			const message = errorField(err, 'message');
			this._emit(
				'clipboard_status',
				{
					ok: false,
					reason: err && message ? message : 'clipboard_send_failed'
				},
				entry
			);
		}
	}

	/**
	 * Type text on the remote as keystrokes (KeyEvent.seq).
	 * Works on Winlogon / password fields where clipboard paste does not.
	 * @returns false when not streaming / view-only / empty
	 */
	sendText(text: string | null | undefined): boolean {
		if (this._state !== 'streaming') return false;
		if (this._viewOnly) return false;
		const str = text == null ? '' : String(text);
		if (!str) return false;
		void this._sendTextSeq(str);
		return true;
	}

	async _sendTextSeq(text: string): Promise<void> {
		if (typeof this.resetKeyboard === 'function') {
			this.resetKeyboard();
		}
		const CHUNK = 64;
		const DELAY_MS = 12;
		const encode = (chunk: string) => RDKeyboardEncoder.encodeTextSeq(chunk);

		for (let i = 0; i < text.length; i += CHUNK) {
			if (this._state !== 'streaming' || this._viewOnly) return;
			const chunk = text.slice(i, i + CHUNK);
			const keyEvent = encode(chunk);
			if (keyEvent) {
				this._sendPeerMessage({ keyEvent });
			}
			if (i + CHUNK < text.length) {
				await new Promise((resolve) => setTimeout(resolve, DELAY_MS));
			}
		}
	}

	/**
	 * Sync local file clipboard to remote (desktop Cliprdr).
	 */
	syncCliprdrFiles(): ReturnType<typeof RDCliprdr.syncLocalFiles> {
		if (!RDCliprdr.isSupported()) {
			return Promise.resolve({ hasFiles: false, signature: '', busy: false });
		}
		return RDCliprdr.syncLocalFiles(this);
	}

	syncCliprdrPaths(paths: string[], position?: { x?: number; y?: number } | null): void {
		if (!RDCliprdr.isSupported()) return;
		void RDCliprdr.syncPaths(this, paths, position || null);
	}

	_handleCliprdr(cliprdr: Cliprdr): void {
		if (!RDCliprdr.isSupported()) return;
		void RDCliprdr.handleMessage(this, cliprdr);
	}

	/**
	 * Send Ctrl+Alt+Delete to remote
	 */
	sendCtrlAltDel(): void {
		if (this._state !== 'streaming' || this._viewOnlyLocked) return;
		this._sendPeerMessage({
			keyEvent: { controlKey: 'CtrlAltDel', down: true, press: true, modifiers: [], mode: 'Legacy' }
		});
	}

	/**
	 * Send Lock Screen command to remote
	 */
	sendLockScreen(): void {
		if (this._state !== 'streaming' || this._viewOnlyLocked) return;
		this._sendPeerMessage({
			keyEvent: { controlKey: 'LockScreen', down: true, press: true, modifiers: [], mode: 'Legacy' }
		});
	}

	/**
	 * Request screen refresh (force new keyframe)
	 */
	sendRefreshScreen(): void {
		if (this._state !== 'streaming') return;
		this._sendPeerMessage(this.proto.buildMisc('refreshVideo', true));
	}

	// ---- Session Recording (WebM via MediaRecorder) ----

	/**
	 * Start recording the remote session as WebM video.
	 * @returns True if recording started
	 */
	startRecording(): boolean {
		if (this._recorder) return false;
		if (this._state !== 'streaming') return false;

		try {
			const canvas = this.renderer.canvas;
			const stream = canvas.captureStream(15); // 15fps recording

			// The device's sound: the volume node plays into the recording as well
			const audioCtx = this.audio.audioCtx;
			if (audioCtx && audioCtx.state === 'running' && this.audio.gainNode) {
				try {
					const sound = audioCtx.createMediaStreamDestination();
					this.audio.gainNode.connect(sound);
					const track = sound.stream.getAudioTracks()[0];
					if (track) stream.addTrack(track);
				} catch {
					// The recording goes on without sound
				}
			}

			let mimeType = 'video/webm;codecs=vp9,opus';
			if (!MediaRecorder.isTypeSupported(mimeType)) {
				mimeType = 'video/webm;codecs=vp8,opus';
			}
			if (!MediaRecorder.isTypeSupported(mimeType)) {
				mimeType = 'video/webm';
			}

			this._recordedChunks = [];
			this._recorder = new MediaRecorder(stream, {
				mimeType: mimeType,
				videoBitsPerSecond: 2500000 // 2.5 Mbps
			});

			this._recorder.ondataavailable = (e) => {
				if (e.data && e.data.size > 0) {
					this._recordedChunks?.push(e.data);
				}
			};

			this._recorder.onstop = () => {
				this._emit('recording_stopped', this._recordedChunks);
			};

			this._recorder.start(1000); // 1 second chunks
			this._recordingStartTime = Date.now();
			this._emit('recording_started');
			return true;
		} catch (err) {
			console.warn('[RDClient] Recording failed:', errorField(err, 'message'));
			return false;
		}
	}

	/**
	 * Stop recording and return the WebM blob.
	 */
	stopRecording(): Promise<Blob | null> {
		return new Promise((resolve) => {
			const recorder = this._recorder;
			if (!recorder || recorder.state === 'inactive') {
				resolve(null);
				return;
			}

			recorder.onstop = () => {
				const blob = new Blob(this._recordedChunks, { type: recorder.mimeType });
				this._recordedChunks = [];
				this._recorder = null;
				this._emit('recording_stopped');
				resolve(blob);
			};

			recorder.stop();
		});
	}

	/**
	 * Download recorded session as a file.
	 */
	async downloadRecording(): Promise<void> {
		const blob = await this.stopRecording();
		if (!blob) return;

		const ts = new Date().toISOString().replace(/[:.]/g, '-');
		const filename = 'session_' + this.deviceId + '_' + ts + '.webm';

		const url = URL.createObjectURL(blob);
		const a = document.createElement('a');
		a.href = url;
		a.download = filename;
		a.click();
		URL.revokeObjectURL(url);
	}

	/** Whether recording is active */
	get isRecording() {
		return this._recorder && this._recorder.state === 'recording';
	}

	/** Recording duration in seconds */
	get recordingDuration(): number {
		if (!this._recordingStartTime || !this.isRecording) return 0;
		return Math.floor((Date.now() - this._recordingStartTime) / 1000);
	}

	// ---- Monitor Switching ----

	/**
	 * Get list of available remote monitors.
	 */
	getMonitors(): RDClientMonitor[] {
		if (!this._peerInfo || !this._peerInfo.displays) return [];
		const current = this.getCurrentDisplay();
		return this._peerInfo.displays.map((d, i) => {
			return {
				idx: i,
				name: d.name || 'Monitor ' + (i + 1),
				osName: d.name || '',
				width: d.width || 0,
				height: d.height || 0,
				// A display positioned at the origin is the primary one.
				primary: d.x === 0 && d.y === 0,
				current: i === current
			};
		});
	}

	/**
	 * Index of the currently captured remote display.
	 */
	getCurrentDisplay(): number {
		if (typeof this._currentDisplay === 'number') return this._currentDisplay;
		if (this._peerInfo && typeof this._peerInfo.currentDisplay === 'number') {
			return this._peerInfo.currentDisplay;
		}
		return 0;
	}

	/**
	 * Switch to a specific monitor (RustDesk-compatible).
	 * Sends a SwitchDisplay message followed by a CaptureDisplays message
	 * (matching the desktop client's software-render switch path), then
	 * forces a fresh keyframe.
	 * @param monitorIdx Monitor index
	 */
	switchMonitor(monitorIdx: number): void {
		if (this._state !== 'streaming') return;
		this._currentDisplay = monitorIdx;
		this._sendPeerMessage(
			this.proto.buildMisc('switchDisplay', {
				display: monitorIdx,
				width: 0,
				height: 0
			})
		);
		this._sendPeerMessage(
			this.proto.buildMisc('captureDisplays', {
				add: [],
				sub: [],
				set: [monitorIdx]
			})
		);
		if (this.video) this.video._needKeyframe = true;
		this.sendRefreshScreen();
		this._emit('display_switched', monitorIdx);
	}

	// ---- Virtual Displays (RustDesk IDD / Amyuni IDD) ----

	/**
	 * Parse peer info into cached current-display and virtual-display state.
	 * @param info PeerInfo from login response or peer_info message
	 */
	_processPeerInfo(info: PeerInfo | null): void {
		if (!info) return;
		if (typeof info.currentDisplay === 'number') {
			this._currentDisplay = info.currentDisplay;
		} else if (typeof this._currentDisplay !== 'number') {
			this._currentDisplay = 0;
		}
		this._virtualDisplay = this._parseVirtualDisplaySupport(info);
		if (info.encoding) {
			this._peerEncoding = info.encoding;
		}
		this._windowsSessions = this._parseWindowsSessions(info);
		if (this.input && info.platform) {
			if (typeof this.input.setPeerPlatform === 'function') {
				this.input.setPeerPlatform(info.platform);
			}
		}
	}

	_parseWindowsSessions(info: PeerInfo): RDClientWindowsSessions {
		const raw = info.windowsSessions;
		if (!raw) return { sessions: [], currentSid: 0 };
		const list = Array.isArray(raw.sessions) ? raw.sessions : [];
		const sessions = list.map((s) => {
			return {
				sid: s.sid,
				name: s.name || 'Session ' + s.sid
			};
		});
		const currentSid = raw.currentSid != null ? raw.currentSid : 0;
		return { sessions: sessions, currentSid: currentSid };
	}

	/**
	 * Windows session list reported by the peer (multi-session hosts).
	 */
	getWindowsSessions(): RDClientWindowsSessions {
		return this._windowsSessions || { sessions: [], currentSid: 0 };
	}

	/**
	 * Switch the controlled Windows session (RustDesk selected_sid).
	 */
	selectWindowsSession(sid: number): void {
		if (this._state !== 'streaming') return;
		const n = Number(sid);
		if (!Number.isFinite(n) || n < 0) return;
		this._sendPeerMessage(this.proto.buildMisc('selectedSid', n));
		if (this._windowsSessions) this._windowsSessions.currentSid = n;
		this._emit('windows_session_selected', n);
	}

	/**
	 * Advertise operator decode capabilities to the peer (Misc.supported_encoding).
	 */
	_advertiseSupportedEncoding(): void {
		if (!this.proto || typeof this.proto.buildSupportedEncoding !== 'function') return;
		const enc = this.proto.buildSupportedEncoding(this._codecAbilities);
		this._sendPeerMessage(this.proto.buildMisc('supportedEncoding', enc));
	}

	/**
	 * Release stuck remote modifiers / keys (toolbar recovery).
	 */
	resetKeyboard(): void {
		if (this.input && typeof this.input.resetKeyboard === 'function') {
			this.input.resetKeyboard();
		}
	}

	/**
	 * @param mode 'Legacy' | 'Map' | 'Auto'
	 */
	setKeyboardMode(mode: string): void {
		if (this.input && typeof this.input.setKeyboardMode === 'function') {
			this.input.setKeyboardMode(mode);
		}
	}

	/**
	 * Derive virtual-display support from PeerInfo.platformAdditions (JSON).
	 */
	_parseVirtualDisplaySupport(info: PeerInfo | null): RDClientVirtualDisplaySupport {
		const result: RDClientVirtualDisplaySupport = {
			supported: false,
			impl: '',
			rustdeskDisplays: [],
			amyuniCount: 0
		};
		if (!info) return result;
		const platform = info.platform || '';
		let additions: unknown = info.platformAdditions || '';
		if (typeof additions === 'string') {
			if (!additions) return result;
			try {
				additions = JSON.parse(additions);
			} catch {
				return result;
			}
		}
		if (!additions || typeof additions !== 'object') return result;
		const isInstalled = Reflect.get(additions, 'is_installed') === true;
		const impl: unknown = Reflect.get(additions, 'idd_impl') || '';
		if (platform !== 'Windows' || !isInstalled) return result;
		if (impl === 'rustdesk_idd') {
			result.supported = true;
			result.impl = impl;
			const list: unknown = Reflect.get(additions, 'rustdesk_virtual_displays');
			if (Array.isArray(list)) result.rustdeskDisplays = list.slice();
		} else if (impl === 'amyuni_idd') {
			result.supported = true;
			result.impl = impl;
			const count: unknown = Reflect.get(additions, 'amyuni_virtual_displays');
			result.amyuniCount = typeof count === 'number' ? count : 0;
		}
		return result;
	}

	/**
	 * Virtual-display capability of the connected peer.
	 */
	getVirtualDisplaySupport(): RDClientVirtualDisplaySupport {
		return this._virtualDisplay || { supported: false, impl: '', rustdeskDisplays: [], amyuniCount: 0 };
	}

	/**
	 * Plug in / plug out a virtual display (RustDesk-compatible).
	 * @param index Display index. Use -1 to plug out all.
	 * @param on True to plug in, false to plug out
	 */
	toggleVirtualDisplay(index: number, on: boolean): void {
		if (this._state !== 'streaming') return;
		this._sendPeerMessage(
			this.proto.buildMisc('toggleVirtualDisplay', {
				display: index,
				on: !!on
			})
		);
		this._emit('virtual_display_toggled', { index: index, on: !!on });
	}

	// ---- Image Quality Control ----

	/**
	 * Set image quality preset.
	 * @param preset 'speed' | 'balanced' | 'quality' | 'best'
	 */
	setQualityPreset(preset: string): void {
		if (this._state !== 'streaming') return;

		const balanced = { imageQuality: 'Balanced', customFps: 30 };
		const config: Record<string, { imageQuality: string; customFps: number } | undefined> = {
			speed: { imageQuality: 'Low', customFps: 60 },
			balanced,
			quality: { imageQuality: 'Best', customFps: 30 },
			best: { imageQuality: 'Best', customFps: 60 }
		};

		const c = config[preset] || balanced;
		const mode = this.opts.fpsMode || '30';
		const customFps = !RDVideo.isSupported()
			? 30
			: mode === '60' || mode === 'adaptive'
				? 60
				: mode === '30'
					? 30
					: c.customFps;
		this._adaptivePaused = mode !== 'adaptive';
		this._savedActiveFps = customFps;
		this.opts.qualityPreset = preset;
		this._sendPeerMessage(this.proto.buildOptionMisc({ imageQuality: c.imageQuality, customFps: customFps }));
		this._emit('quality_changed', preset);
	}

	/**
	 * Set the stream FPS mode.
	 * @param mode '30' | '60' | 'adaptive'
	 */
	setFpsMode(mode: string): void {
		const value = ['30', '60', 'adaptive'].includes(String(mode)) ? String(mode) : '30';
		const fps = !RDVideo.isSupported() ? 30 : value === '30' ? 30 : 60;
		this.opts.fpsMode = value;
		this.opts.fps = fps;
		this.opts.adaptiveQuality = value === 'adaptive';
		this._savedActiveFps = fps;
		this._adaptivePaused = value !== 'adaptive';
		if (this._state !== 'streaming') return;

		this._sendPeerMessage(this.proto.buildOptionMisc({ customFps: fps }));
		this._sendPeerMessage(
			this.proto.buildMisc('autoAdjustFps', !RDVideo.isSupported() ? 0 : value === 'adaptive' ? 60 : 0)
		);
		if (value === 'adaptive') {
			if (!this._adaptiveInterval) this._startAdaptiveQuality();
		} else if (this._adaptiveInterval) {
			clearInterval(this._adaptiveInterval);
			this._adaptiveInterval = null;
		}
	}

	// ---- Codec Control ----

	/**
	 * Request the remote peer to (re)encode using a specific codec.
	 * @param codec 'Auto' | 'VP9' | 'AV1' | 'H264' | 'H265' | 'VP8'
	 */
	setCodec(codec: string): void {
		if (this._state !== 'streaming') return;
		const name = codec || 'Auto';
		this._preferCodec = name;
		this._adaptivePaused = true; // explicit user choice
		// Re-advertise abilities with the new preferred codec so the peer switches encoder.
		this._sendPeerMessage(
			this.proto.buildOptionMisc({
				supportedDecoding: { abilities: this._codecAbilities, prefer: name }
			})
		);
		// The encoder restarts with a fresh keyframe; force our decoder to wait for it.
		if (this.video) this.video._needKeyframe = true;
		this._sendPeerMessage(this.proto.buildMisc('refreshVideo', true));
		this.opts.preferCodec = name;
		this._emit('codec_changed', name);
	}

	/**
	 * Downgrade to the next working codec when WebCodecs fails at runtime.
	 */
	_handleCodecFallback(failedCodec: string | null | undefined): void {
		const failed = String(failedCodec || '').toLowerCase();
		if (!failed || this._codecFallbackDone || this._state !== 'streaming') return;
		this._codecFallbackDone = true;

		if (!this._codecAbilities) this._codecAbilities = {};
		this._codecAbilities[failed] = false;

		const order = ['vp9', 'h264', 'vp8'];
		let next = 'H264';
		for (let i = 0; i < order.length; i++) {
			const candidate = order[i];
			if (candidate === undefined || candidate === failed) continue;
			if (this._codecAbilities[candidate] !== false) {
				next = candidate === 'h264' ? 'H264' : candidate.toUpperCase();
				break;
			}
		}

		this._emit('log', 'Codec ' + failed.toUpperCase() + ' failed — switching to ' + next);
		this.setCodec(next);
	}

	/**
	 * Request remote device restart
	 */
	sendRestartRemoteDevice(): void {
		if (this._state !== 'streaming' || this._viewOnlyLocked) return;
		this._sendPeerMessage(this.proto.buildMisc('restartRemoteDevice', true));
	}

	/**
	 * Send chat message to remote peer
	 */
	sendChat(text: string): void {
		if (this._state !== 'streaming') return;
		this._sendPeerMessage(this.proto.buildChatMessage(text));
	}

	/**
	 * Change image quality during session
	 * @param quality 'Best' | 'Balanced' | 'Low'
	 */
	setImageQuality(quality: string): void {
		if (this._state !== 'streaming') return;
		this._adaptivePaused = true; // explicit user choice
		this._sendPeerMessage(this.proto.buildOptionMisc({ imageQuality: quality }));
	}

	/**
	 * Change custom FPS during session
	 */
	setCustomFps(fps: number): void {
		if (this._state !== 'streaming') return;
		this._sendPeerMessage(this.proto.buildOptionMisc({ customFps: fps }));
	}

	/**
	 * Toggle remote cursor visibility
	 */
	setShowRemoteCursor(show: boolean): void {
		if (this._state !== 'streaming') return;
		this._sendPeerMessage(this.proto.buildOptionMisc({ showRemoteCursor: show }));
	}

	/**
	 * Toggle input blocking on remote side
	 */
	setBlockInput(block: boolean): void {
		if (this._state !== 'streaming' || this._viewOnlyLocked) return;
		this._sendPeerMessage(this.proto.buildOptionMisc({ blockInput: block }));
	}

	/**
	 * Toggle lock after session end
	 */
	setLockAfterSession(lock: boolean): void {
		if (this._state !== 'streaming') return;
		this._sendPeerMessage(this.proto.buildOptionMisc({ lockAfterSessionEnd: lock }));
	}

	/**
	 * Toggle privacy mode on remote
	 */
	setPrivacyMode(on: boolean): void {
		if (this._state !== 'streaming') return;
		this._sendPeerMessage(this.proto.buildTogglePrivacyMode(on));
	}

	/**
	 * Toggle clipboard sharing
	 */
	setDisableClipboard(disable: boolean): void {
		this._clipboardDisabled = !!disable;
		if (this._state !== 'streaming') return;
		this._sendPeerMessage(this.proto.buildOptionMisc({ disableClipboard: disable }));
	}

	/**
	 * Toggle audio on remote side
	 */
	setDisableAudio(disable: boolean): void {
		if (this._state !== 'streaming') return;
		this._sendPeerMessage(this.proto.buildOptionMisc({ disableAudio: disable }));
	}

	/**
	 * Toggle view-only mode (local only: disables input capture)
	 */
	setViewOnly(on: boolean): void {
		this._viewOnly = this._viewOnlyLocked || !!on;
		this._syncInputCapture();
		this._emit('view_only', on);
	}

	/**
	 * Mark whether this client is the active tab in the multi-session viewer.
	 * Gates keyboard/mouse capture, inbound clipboard, and audio playback.
	 */
	setSessionActive(active: boolean): void {
		const next = !!active;
		const changed = next !== this._sessionActive;
		this._sessionActive = next;
		this._clipboardToLocalEnabled = next;
		if (this.audio.setSessionActive) {
			this.audio.setSessionActive(next);
		}
		this._syncInputCapture();
		if (this._state === 'streaming') {
			this._syncStreamThrottle();
		} else if (changed) {
			this._syncStreamThrottle();
		}
	}

	/**
	 * Background FPS for inactive viewer tabs (RustDesk customFps option).
	 */
	setBackgroundFps(fps: number): void {
		const n = Number(fps);
		if (Number.isFinite(n) && n >= 1 && n <= 5) {
			this._backgroundFps = Math.round(n);
		}
	}

	/** Target FPS for the active tab based on quality preset */
	_getActiveStreamFps(): number {
		if (this._savedActiveFps) return this._savedActiveFps;
		const preset = this.opts.qualityPreset || 'best';
		const map: Record<string, number | undefined> = { speed: 60, balanced: 30, quality: 30, best: 60 };
		return map[preset] || this.opts.fps || 60;
	}

	/** Throttle peer encode rate and pause local decode when tab is hidden */
	_syncStreamThrottle(): void {
		if (this._state !== 'streaming') return;
		const wantActive = this._sessionActive;
		if (this._streamThrottledActive === wantActive) return;
		this._streamThrottledActive = wantActive;
		if (wantActive) {
			this._resumeActiveStream();
		} else {
			this._throttleBackgroundStream();
		}
	}

	_throttleBackgroundStream(): void {
		if (!this._savedActiveFps) {
			this._savedActiveFps = this._getActiveStreamFps();
		}
		this.renderer.stopRenderLoop();
		if (this.video.setBackgroundMode) {
			this.video.setBackgroundMode(true);
		}
		this.setCustomFps(this._backgroundFps || 1);
	}

	_resumeActiveStream(): void {
		if (this.video.setBackgroundMode) {
			this.video.setBackgroundMode(false);
		}
		this.renderer.startRenderLoop();
		const fps = this._getActiveStreamFps();
		this.setCustomFps(fps);
		this._sendPeerMessage(this.proto.buildMisc('refreshVideo', true));
	}

	/** Sync input listeners with session/tab and view-only state */
	_syncInputCapture(): void {
		const shouldCapture = this._sessionActive && !this._viewOnly && this._state === 'streaming';
		if (shouldCapture) {
			this.input.start();
		} else {
			this.input.stop();
		}
	}

	/** Whether view-only mode is active */
	get viewOnly(): boolean {
		return this._viewOnly || false;
	}

	/**
	 * Toggle fullscreen
	 */
	async toggleFullscreen(container: HTMLElement): Promise<void> {
		if (document.fullscreenElement) {
			await document.exitFullscreen();
		} else {
			await container.requestFullscreen();
		}
		// Resize after fullscreen change
		setTimeout(() => this.renderer.resize(), 100);
	}

	/**
	 * Set scale mode
	 * @param mode 'fit' | 'fill' | '1:1' | 'stretch'
	 */
	setScaleMode(mode: string): void {
		this.renderer.setScaleMode(mode);
		this.opts.scaleMode = mode;
	}

	/**
	 * Set audio volume
	 * @param volume 0 to 1
	 */
	setVolume(volume: number): void {
		this.audio.setVolume(volume);
	}

	/**
	 * Toggle audio mute
	 */
	setAudioMuted(muted: boolean): void {
		this.audio.setMuted(muted);
	}

	/**
	 * Get aggregated statistics
	 */
	getStats(): RDClientStats {
		return {
			state: this._state,
			video: this.video.getStats(),
			audio: this.audio.getStats(),
			renderer: this.renderer.getStats(),
			connection: this.conn.state,
			diagnostics: {
				lastClose: this.conn._lastClose || null,
				lastDisconnect: this._lastDisconnect,
				lastError: this._lastError
			}
		};
	}
}
