/**
 * BetterDesk Web Remote — dedicated FILE_TRANSFER relay connection.
 * RustDesk peers only handle FileAction on ConnType::FILE_TRANSFER sessions.
 */
import { RDConnection } from './connection.ts';
import { RDConnectionParams } from './connection-params.ts';
import { RDCrypto } from './crypto.ts';
import { RDProtocol } from './protocol.ts';
import { RDPunchHoleResponse } from './punchhole-response.ts';
import type { LoginResponse, Message, MessageInit, SignedId } from './generated/protos.ts';

/** An event listener; each event has its own arguments (see the _emit calls) */
export type RDFileConnectionListener = (...args: never[]) => void;

export type RDFileConnectionState =
	'idle' | 'connecting' | 'authenticating' | 'ready' | 'error' | 'disconnected';

export interface RDFileConnectionOptions {
	deviceId: string;
	serverPubKey?: string;
	myName?: string;
	/** The shared, loaded protocol instance */
	proto?: RDProtocol;
	/** The server's connection policy (/api/health), sizes the rendezvous waits */
	connection?: unknown;
	rendezvousTimeoutMs?: number;
	signalRelayTimeoutMs?: number;
}

/** What the rendezvous server answered: the relay to use, or why there is none */
interface RendezvousAnswer {
	error?: string;
	relayServer?: string;
	uuid?: string;
	pk?: Uint8Array | null;
}

/** err.message or err.stack as the JS read them: undefined when the caught value has none */
function errorField(err: unknown, key: 'message' | 'stack'): unknown {
	return typeof err === 'object' && err !== null ? Reflect.get(err, key) : undefined;
}

export class RDFileConnection {
	deviceId: string;
	opts: RDFileConnectionOptions;
	conn: RDConnection;
	proto: RDProtocol;
	crypto: RDCrypto;
	_state: RDFileConnectionState;
	_listeners: Record<string, RDFileConnectionListener[]>;
	_rendezvousDecoder: ReturnType<RDProtocol['createStreamDecoder']> | null;
	_relayConfirmReceived: boolean;
	_keyExchangeDone: boolean;
	_peerEncryptionConfirmed: boolean;
	_loginChallenge: string;
	_loginSalt: string;
	_connectPromise: Promise<void> | null;
	_loginResolve: (() => void) | null;
	_loginReject: ((err: unknown) => void) | null;
	_peerSignedPk: Uint8Array | null;
	_rendezvousTimeoutMs: number;
	_signalRelayTimeoutMs: number;
	_pendingPassword?: string;

	constructor(opts: RDFileConnectionOptions = { deviceId: '' }) {
		if (!opts.deviceId) throw new Error('deviceId required');
		this.deviceId = opts.deviceId;
		this.opts = opts;
		this.conn = new RDConnection({ deviceId: this.deviceId });
		this.proto = opts.proto || new RDProtocol();
		this.crypto = new RDCrypto();
		this._state = 'idle'; // idle | connecting | authenticating | ready | error | disconnected
		this._listeners = {};
		this._rendezvousDecoder = null;
		this._relayConfirmReceived = false;
		this._keyExchangeDone = false;
		this._peerEncryptionConfirmed = false;
		this._loginChallenge = '';
		this._loginSalt = '';
		this._connectPromise = null;
		this._loginResolve = null;
		this._loginReject = null;
		this._peerSignedPk = null;

		const resolvedTimeouts = RDConnectionParams.resolveConnectTimeouts(opts.connection);
		this._rendezvousTimeoutMs =
			Number(opts.rendezvousTimeoutMs) > 0 ? Number(opts.rendezvousTimeoutMs) : resolvedTimeouts.rendezvousMs;
		this._signalRelayTimeoutMs =
			Number(opts.signalRelayTimeoutMs) > 0
				? Number(opts.signalRelayTimeoutMs)
				: resolvedTimeouts.signalRelayMs;
	}

	get state(): RDFileConnectionState {
		return this._state;
	}

	on(event: string, fn: RDFileConnectionListener): this {
		if (!this._listeners[event]) this._listeners[event] = [];
		this._listeners[event].push(fn);
		return this;
	}

	off(event: string, fn: RDFileConnectionListener): this {
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

	_setState(s: RDFileConnectionState): void {
		if (this._state !== s) {
			this._state = s;
			this._emit('state', s);
		}
	}

	_connType() {
		return this.proto.enums.ConnType.values.FILE_TRANSFER;
	}

	_randomUUID(): string {
		if (window.crypto && typeof window.crypto.randomUUID === 'function') {
			return window.crypto.randomUUID();
		}
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
	}

	/**
	 * Open FILE_TRANSFER relay and authenticate (reuse desktop session password).
	 */
	connect(password?: string): Promise<void> {
		if (this._state === 'ready') return Promise.resolve();
		if (this._connectPromise) return this._connectPromise;

		this._connectPromise = this._doConnect(password || '').finally(() => {
			this._connectPromise = null;
		});
		return this._connectPromise;
	}

	async _doConnect(password: string): Promise<void> {
		try {
			if (this.conn) {
				try {
					this.conn.close();
				} catch {
					/* ignore */
				}
			}
			this.conn = new RDConnection({ deviceId: this.deviceId });
			this.crypto = new RDCrypto();
			this._setState('connecting');
			this._emit('log', 'Opening file transfer session…');

			if (!this.proto.loaded) await this.proto.load();

			this._rendezvousDecoder = this.proto.createStreamDecoder();
			this._relayConfirmReceived = false;
			this._keyExchangeDone = false;
			this._peerEncryptionConfirmed = false;
			this._loginChallenge = '';
			this._loginSalt = '';
			this._pendingPassword = password;

			const ct = this._connType();
			await this.conn.connectRendezvous();

			const punchData = this.proto.encodeRendezvous(
				this.proto.buildPunchHoleRequest(this.deviceId, this.opts.serverPubKey, ct)
			);
			this.conn.sendRendezvous(punchData);

			const rendezvousResponse = await this._waitForRendezvousResponse();
			if (rendezvousResponse.error) {
				throw new Error(rendezvousResponse.error);
			}
			this._peerSignedPk = rendezvousResponse.pk || null;

			let relayUUID = rendezvousResponse.uuid || '';
			let relayServer = rendezvousResponse.relayServer || '';

			if (!relayUUID) {
				relayUUID = this._randomUUID();
				const signalData = this.proto.encodeRendezvous(
					this.proto.buildRequestRelay(this.deviceId, relayUUID, relayServer, this.opts.serverPubKey, ct)
				);
				this.conn.sendRendezvous(signalData);
				const relayConfirm = await this._waitForSignalRelayResponse();
				if (relayConfirm.error) throw new Error(relayConfirm.error);
				relayUUID = relayConfirm.uuid || relayUUID;
				relayServer = relayConfirm.relayServer || relayServer;
				if (relayConfirm.pk) {
					this._peerSignedPk = relayConfirm.pk;
				}
			}

			this.conn.closeRendezvous();
			await this.conn.connectRelay();

			this.conn.on('relay:message', (data) => {
				// Binary messages are ArrayBuffers (the socket's binaryType)
				if (data instanceof ArrayBuffer) this._handleRelayData(data);
			});
			this.conn.on('relay:close', () => {
				if (this._state !== 'disconnected' && this._state !== 'error') {
					this._setState('disconnected');
					this._emit('disconnected', 'File transfer relay closed');
				}
			});

			const relayData = this.proto.serializeRendezvous(
				this.proto.buildRequestRelay(this.deviceId, relayUUID, relayServer, this.opts.serverPubKey, ct)
			);
			this.conn.sendRelay(relayData);

			await this._waitForLogin(password);
			this._setState('ready');
			this._emit('ready');
			this._emit('log', 'File transfer session ready');
		} catch (err) {
			this._setState('error');
			this._emit('error', errorField(err, 'message') || String(err));
			this.disconnect();
			throw err;
		}
	}

	_waitForLogin(password: string): Promise<void> {
		return new Promise((resolve, reject) => {
			this._loginResolve = resolve;
			this._loginReject = reject;
			const timeout = setTimeout(() => {
				reject(new Error('File transfer login timeout (30s)'));
			}, 30000);

			const done = () => {
				clearTimeout(timeout);
				this._loginResolve = null;
				this._loginReject = null;
			};

			const origResolve = resolve;
			const origReject = reject;
			this._loginResolve = () => {
				done();
				origResolve();
			};
			this._loginReject = (e) => {
				done();
				origReject(e);
			};

			// If password provided upfront, authenticate when hash arrives or immediately after signedId
			this._pendingPassword = password;
		});
	}

	async _authenticate(password: string): Promise<void> {
		// The password hash never goes out unencrypted
		if (!this._keyExchangeDone) throw new Error('Handshake failed: the connection is not encrypted');
		this._setState('authenticating');
		const hash = await this.crypto.hashPassword(password, this._loginSalt || '', this._loginChallenge || '');
		const loginReq = this.proto.buildFileTransferLoginRequest(hash, {
			username: this.deviceId,
			myId: 'betterdesk-web-ft-' + Date.now().toString(36),
			myName: this.opts.myName || 'BetterDesk Web',
			showHidden: false
		});
		this._sendPeerMessageRaw(loginReq);
	}

	submit2FA(code: string): void {
		this._sendPeerMessageRaw(this.proto.buildAuth2FA(String(code || '').trim()));
	}

	sendMessage(msgObj: MessageInit): void {
		this._sendPeerMessage(msgObj);
	}

	/**
	 * One relay socket message per peer message: the console frames it for
	 * the device itself (/ws/relay?transport=message), as for the desktop session
	 */
	_sendPeerMessage(msgObj: MessageInit): void {
		if (!this.proto.loaded || this._state !== 'ready') return;
		this._sendPeerMessageRaw(msgObj);
	}

	disconnect(): void {
		if (this._loginReject) {
			this._loginReject(new Error('Disconnected'));
		}
		this._connectPromise = null;
		try {
			this.conn.close();
		} catch {
			/* ignore */
		}
		this._setState('disconnected');
	}

	_waitForRendezvousResponse(): Promise<RendezvousAnswer> {
		return new Promise((resolve, reject) => {
			const waitMs = this._rendezvousTimeoutMs || 30000;
			const timeout = setTimeout(() => {
				this.conn.off('rendezvous:message', handler);
				reject(new Error('File transfer rendezvous timeout'));
			}, waitMs);

			const handler = (rawData: unknown) => {
				// Binary messages are ArrayBuffers (the socket's binaryType)
				if (!(rawData instanceof ArrayBuffer)) return;
				const frames = this._rendezvousDecoder?.feed(rawData) ?? [];
				for (const frame of frames) {
					try {
						const msg = this.proto.decodeRendezvous(frame);
						if (msg.keyExchange || msg.hc) continue;

						if (msg.punchHoleResponse) {
							clearTimeout(timeout);
							this.conn.off('rendezvous:message', handler);
							const resp = msg.punchHoleResponse;
							resolve(RDPunchHoleResponse.interpretPunchHoleResponse(resp));
							return;
						}

						if (msg.relayResponse) {
							clearTimeout(timeout);
							this.conn.off('rendezvous:message', handler);
							const rr = msg.relayResponse;
							if (rr.refuseReason && rr.refuseReason.length > 0) {
								resolve({ error: 'Relay refused: ' + rr.refuseReason });
							} else {
								resolve({
									relayServer: rr.relayServer || '',
									uuid: rr.uuid || '',
									pk: rr.pk || null
								});
							}
							return;
						}
					} catch (err) {
						console.warn('[RDFileConnection] rendezvous decode:', errorField(err, 'message'));
					}
				}
			};
			this.conn.on('rendezvous:message', handler);
		});
	}

	_waitForSignalRelayResponse(): Promise<RendezvousAnswer> {
		return new Promise((resolve, reject) => {
			const waitMs = this._signalRelayTimeoutMs || 15000;
			const timeout = setTimeout(() => {
				this.conn.off('rendezvous:message', handler);
				reject(new Error('File transfer RelayResponse timeout'));
			}, waitMs);

			const handler = (rawData: unknown) => {
				if (!(rawData instanceof ArrayBuffer)) return;
				const frames = this._rendezvousDecoder?.feed(rawData) ?? [];
				for (const frame of frames) {
					try {
						const msg = this.proto.decodeRendezvous(frame);
						if (msg.keyExchange || msg.hc) continue;
						if (msg.relayResponse) {
							clearTimeout(timeout);
							this.conn.off('rendezvous:message', handler);
							const rr = msg.relayResponse;
							if (rr.refuseReason && rr.refuseReason.length > 0) {
								resolve({ error: 'Relay refused: ' + rr.refuseReason });
							} else {
								resolve({
									relayServer: rr.relayServer || '',
									uuid: rr.uuid || '',
									pk: rr.pk || null
								});
							}
							return;
						}
					} catch (err) {
						console.warn('[RDFileConnection] signal relay decode:', errorField(err, 'message'));
					}
				}
			};
			this.conn.on('rendezvous:message', handler);
		});
	}

	_handleRelayData(rawData: ArrayBuffer | ArrayLike<number>): void {
		try {
			const payload = rawData instanceof ArrayBuffer ? new Uint8Array(rawData) : new Uint8Array(rawData);
			if (payload.length > 0) this._handleRelayMessage(payload);
		} catch (err) {
			console.warn('[RDFileConnection] relay decode:', errorField(err, 'message'));
		}
	}

	/**
	 * Same handshake as the desktop session (RDClient._handleRelayMessage):
	 * the device's SignedId first, checked, then only encrypted frames. Any
	 * gap ends the session before the login.
	 */
	_handleRelayMessage(frameData: Uint8Array): void {
		if (this._state === 'error' || this._state === 'disconnected') return;
		if (!this._relayConfirmReceived) {
			this._relayConfirmReceived = true;
			try {
				const rdvMsg = this.proto.decodeRendezvous(frameData);
				if (rdvMsg.relayResponse) return;
			} catch {
				// First frame is a peer Message, not relay confirmation
			}
		}

		if (!this._keyExchangeDone) {
			let first: Message | null;
			try {
				first = this.proto.decodeMessage(frameData);
			} catch {
				first = null;
			}
			if (!first || !first.signedId) {
				this._fail('Handshake failed: the device did not identify itself');
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
			this._peerEncryptionConfirmed = true;
		} else if (this._peerEncryptionConfirmed) {
			return;
		} else {
			this._fail('Handshake failed: the device did not encrypt the connection');
			return;
		}

		const msg = this.proto.decodeMessage(data);
		this._dispatchMessage(msg);
	}

	/** End the session: the pending login fails with the reason */
	_fail(reason: string): void {
		const err = new Error(reason);
		if (this._loginReject) {
			this._loginReject(err);
			return;
		}
		this._setState('error');
		this._emit('error', reason);
		this.disconnect();
	}

	_dispatchMessage(msg: Message): void {
		if (msg.hash) {
			this._loginChallenge = msg.hash.challenge || '';
			this._loginSalt = msg.hash.salt || '';
			if (this._pendingPassword != null && this._pendingPassword !== undefined) {
				this._authenticate(this._pendingPassword).catch((e: unknown) => {
					if (this._loginReject) this._loginReject(e);
				});
			}
			return;
		}
		if (msg.loginResponse) {
			this._handleLoginResponse(msg.loginResponse);
			return;
		}
		if (msg.fileResponse) {
			this._emit('file_response', msg.fileResponse);
			return;
		}
		if (msg.fileAction) {
			this._emit('file_action', msg.fileAction);
			return;
		}
		if (msg.peerInfo) {
			// Some peers send peerInfo before login completes on FT sessions
			return;
		}
	}

	_handleSignedId(signedId: SignedId): void {
		const checked = RDCrypto.checkPeer(
			signedId.id,
			this._peerSignedPk,
			this.opts.serverPubKey || '',
			this.deviceId,
			this.proto.types.IdPk
		);
		if ('error' in checked) {
			this._fail(checked.error);
			return;
		}

		this.crypto.generateKeyPair();
		this.crypto.generateSymmetricKey();
		this.crypto.setPeerPublicKey(checked.peerPk);
		this._completeKeyExchange();

		// Unattended / no-password peers may send loginResponse without Hash
		if (this._pendingPassword === '' && this._loginResolve) {
			this._authenticate('').catch((e: unknown) => {
				if (this._loginReject) this._loginReject(e);
			});
		}
	}

	_completeKeyExchange(): void {
		if (this._keyExchangeDone) return;
		// Set by _handleSignedId just before
		const peerPk = this.crypto.peerPk;
		if (!peerPk) return;
		const keyMsg = this.crypto.createSymmetricKeyMsg(peerPk);
		const pkMsg = this.proto.buildPublicKey(keyMsg.asymmetricValue, keyMsg.symmetricValue);
		this._sendPeerMessageRaw(pkMsg);
		this.crypto.enabled = true;
		this.crypto._sendSeq = 0;
		this.crypto._recvSeq = 0;
		this._keyExchangeDone = true;
	}

	_sendPeerMessageRaw(msgObj: MessageInit): void {
		let data = this.proto.serializeMessage(msgObj);
		if (this.crypto.enabled) data = this.crypto.processOutgoing(data);
		this.conn.sendRelay(data);
	}

	_handleLoginResponse(resp: LoginResponse): void {
		if (resp.error && resp.error.length > 0) {
			const errLower = (resp.error || '').toLowerCase();
			if (errLower.includes('wrong 2fa') || errLower.includes('invalid 2fa')) {
				this._emit('2fa_error', resp.error);
				return;
			}
			// The same answers the desktop session takes as a request for the code (RDClient._is2faRequiredError)
			if (
				errLower.includes('2fa required') ||
				errLower.includes('totp') ||
				errLower.includes('verification code')
			) {
				this._emit('2fa_required');
				return;
			}
			if (this._loginReject) this._loginReject(new Error(resp.error));
			this._emit('login_error', resp.error);
			return;
		}
		if (this._loginResolve) this._loginResolve();
	}
}
