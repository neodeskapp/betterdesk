/**
 * BetterDesk Web Remote Client — CDAP Transport Adapter
 *
 * Drop-in replacement for `RDClient` when the target device is a
 * BetterDesk OS-agent (CDAP transport). Exposes the same public surface
 * (`connect`, `disconnect`, `authenticate`, `verify2fa`, event emitter
 * with `state`/`log`/`session_start`/`disconnected`/`error`/`stats`)
 * so the viewer can swap implementations without branching.
 *
 * The CDAP path:
 *   - opens `/api/cdap/devices/:id/desktop` (subprotocol `cdap-desktop`)
 *   - receives raw JPEG binary frames + JSON control messages
 *   - sends mouse / keyboard / text input as JSON over the same socket
 *
 * No password challenge (Go server gates the WS upgrade with the
 * operator session), so we transition straight from `connecting` →
 * `streaming` on `ready`.
 */
import JMuxer from 'jmuxer';
import { CDAPAudio } from './cdap-audio.ts';
import { CDAPFileTransfer } from './cdap-filetransfer.ts';
import * as log from './log.ts';
import { page } from './page.ts';
import { RDVideo, type RDDecodedFrame } from './video.ts';

function debugLog(...args: unknown[]): void {
	if (page().BetterDesk?.debugRemote === true) {
		log.debugLog(...args);
	}
}

// Mouse encoding (MUST match cdap server expectations + cdap-desktop.js)
const MOUSE_TYPE_MOVE = 0;
const MOUSE_TYPE_DOWN = 1;
const MOUSE_TYPE_UP = 2;
const MOUSE_TYPE_WHEEL = 3;
const MOUSE_BUTTON_LEFT = 1;
const MOUSE_BUTTON_RIGHT = 2;
const MOUSE_BUTTON_MIDDLE = 4;

/** Normalize browser wheel deltas to ±1 (Windows agent multiplies by 120). */
function normalizeWheelDelta(e: WheelEvent): { dx: number; dy: number } | null {
	let rawDx = e.deltaX;
	let rawDy = e.deltaY;
	if (e.shiftKey && rawDy !== 0 && rawDx === 0) {
		rawDx = rawDy;
		rawDy = 0;
	}
	if (rawDx === 0 && rawDy === 0) return null;
	let dx = 0;
	let dy = 0;
	if (Math.abs(rawDx) > Math.abs(rawDy)) {
		dx = rawDx > 0 ? -1 : 1;
	} else if (rawDy !== 0) {
		dy = rawDy > 0 ? -1 : 1;
	}
	if (dx === 0 && dy === 0) return null;
	return { dx, dy };
}

const PRESENCE_PING_MS = 15000;
const STATS_INTERVAL_MS = 1000;

// Video codecs that can be hardware-decoded in the browser image formats
// (webp/jpeg) are always handled by createImageBitmap.
const VIDEO_CODECS = ['av1', 'vp9', 'h264'];
const isVideoCodec = (fmt: string): boolean => VIDEO_CODECS.indexOf(fmt) !== -1;

/**
 * Probe whether the H.264 elementary stream can be decoded through Media
 * Source Extensions (JMuxer). MSE, unlike WebCodecs, does NOT require a
 * secure context, so this path enables GPU-accelerated H.264 over plain
 * HTTP (no HTTPS / domain / certificate needed).
 */
function hasMseH264(): boolean {
	return (
		typeof JMuxer === 'function' &&
		typeof window.MediaSource !== 'undefined' &&
		typeof window.MediaSource.isTypeSupported === 'function' &&
		window.MediaSource.isTypeSupported('video/mp4; codecs="avc1.42E01E"')
	);
}

/**
 * Whether the full WebCodecs `VideoDecoder` pipeline is usable. Requires a
 * secure context (HTTPS or localhost). When available it unlocks AV1/VP9/
 * H.264 hardware decode.
 */
function hasWebCodecs(): boolean {
	return window.isSecureContext === true && typeof window.VideoDecoder !== 'undefined';
}

let _decodableCodecsPromise: Promise<string[]> | null = null;
/** Codecs confirmed broken at runtime (e.g. AV1 on WebKit HW). */
const _blockedWireCodecs = new Set<string>();

/**
 * Sync fallback before async probe completes.
 */
function decodableCodecsSync(): string[] {
	const list: string[] = [];
	if (hasWebCodecs()) {
		const allowAv1 = RDVideo.av1ReliableOnRuntime();
		if (allowAv1 && !_blockedWireCodecs.has('av1')) list.push('av1');
		list.push('vp9', 'h264');
	} else if (hasMseH264()) {
		list.push('h264');
	}
	list.push('webp', 'jpeg');
	return list;
}

/**
 * Probe WebCodecs support and return wire codecs safe to advertise.
 */
function decodableCodecs(): Promise<string[]> {
	if (!_decodableCodecsPromise) {
		_decodableCodecsPromise = (async () => {
			try {
				const probed = await RDVideo.probeDecodableWireCodecs();
				return probed.filter((c) => !_blockedWireCodecs.has(c));
			} catch {
				/* fall through */
			}
			return decodableCodecsSync();
		})();
	}
	return _decodableCodecsPromise.then((list) => list.filter((c) => !_blockedWireCodecs.has(c)));
}

function blockWireCodec(codec: string): void {
	if (!codec) return;
	_blockedWireCodecs.add(String(codec).toLowerCase());
	_decodableCodecsPromise = null;
}

/** The parsed value's fields; a value that is not an object has none */
function asRecord<T extends object>(value: unknown): Partial<T> {
	return typeof value === 'object' && value !== null ? (value as Partial<T>) : {};
}

/** A text field; a field of another type reads as absent */
function text(value: unknown): string | undefined {
	return typeof value === 'string' ? value : undefined;
}

/** err.message as the JS read it: undefined when the caught value has none */
function errorMessage(err: unknown): unknown {
	return typeof err === 'object' && err !== null ? Reflect.get(err, 'message') : undefined;
}

/* ── Messages ── */

/** A control message of the desktop socket as JSON.parse read it */
export interface CDAPControlMessage {
	type?: unknown;
	session_id?: unknown;
	width?: unknown;
	height?: unknown;
	/** 'h264' | 'vp9' | 'av1' | 'webp' | 'jpeg' */
	format?: unknown;
	/** WebCodecs codec string from the agent */
	codec_string?: unknown;
	/** A frame (base64 or data URI), or the clipboard text */
	data?: unknown;
	text?: unknown;
	monitors?: unknown;
	active?: unknown;
	video_codec?: unknown;
	message?: unknown;
	error?: unknown;
	reason?: unknown;
	hidden?: unknown;
}

/** A display of a monitor_list message */
interface CDAPMonitorItem {
	idx?: unknown;
	name?: unknown;
	primary?: unknown;
	width?: unknown;
	height?: unknown;
}

/** A message of the chat relay as JSON.parse read it */
interface CDAPChatFrame {
	type?: unknown;
	text?: unknown;
	from?: unknown;
	messages?: unknown;
}

/** The first message on the desktop socket (it has no type) */
interface CDAPDesktopStart {
	width: number;
	height: number;
	quality: number;
	fps: number;
	fps_mode: string;
	device_pixel_ratio: number;
	client_css_width: number;
	client_css_height: number;
	/** The wire codecs this browser decodes */
	codecs: string[];
	video_codec: 'auto';
}

interface CDAPQualitySet {
	type: 'quality_set';
	quality?: number;
	fps?: number;
	fps_mode?: string;
}

interface CDAPToggle {
	type: 'show_cursor' | 'lock_after_session' | 'privacy_mode' | 'disable_clipboard' | 'block_input';
	enabled: boolean;
}

interface CDAPCommand {
	type: 'keyframe_request' | 'lock_screen' | 'restart_device';
}

interface CDAPMonitorSelect {
	type: 'monitor_select';
	index: number;
}

interface CDAPPresencePing {
	type: 'presence_ping';
	t: number;
}

interface CDAPModifiers {
	ctrl: boolean;
	alt: boolean;
	shift: boolean;
	meta: boolean;
}

interface CDAPKeyboardInput {
	type: 'input';
	input_type: 'keyboard';
	key: string;
	code: string;
	modifiers: CDAPModifiers;
	down: boolean;
}

interface CDAPTextInput {
	type: 'input';
	input_type: 'text';
	text: string;
}

interface CDAPMouseInput {
	type: 'input';
	input_type: 'mouse';
	x: number;
	y: number;
	/** Wheel only: ±1 */
	deltaX?: number;
	deltaY?: number;
	/** MOUSE_TYPE_* | (MOUSE_BUTTON_* << 3) */
	button: number;
}

/** What the client sends on the desktop socket */
export type CDAPDesktopMessage =
	| CDAPDesktopStart
	| CDAPQualitySet
	| CDAPToggle
	| CDAPCommand
	| CDAPMonitorSelect
	| CDAPPresencePing
	| CDAPKeyboardInput
	| CDAPTextInput
	| CDAPMouseInput;

/** What the client sends on the chat relay */
type CDAPChatMessage = { type: 'message'; text: string } | { type: 'get_history' };

/* ── Session types ── */

export interface CDAPSessionOptions {
	deviceId?: string;
	scaleMode?: string;
	fps?: number;
	/** '30' | '60' | 'adaptive' */
	fpsMode?: string;
	/** 'Best' | 'Balanced' | 'Low' */
	imageQuality?: string;
	qualityPreset?: string;
}

/** An event listener; each event has its own arguments (see the _emit calls) */
export type CDAPSessionListener = (...args: never[]) => void;

export interface CDAPMonitor {
	idx: number;
	name: string;
	osName: string;
	primary: boolean;
	width: number;
	height: number;
}

export interface CDAPPeerInfo {
	username: string;
	hostname: string;
	version: 'cdap';
	platform: string;
}

/** getStats(): the counters since the last stats event */
export interface CDAPSessionStats {
	frames: number;
	bytes: number;
	fps: number;
	kbps: number;
	transport: 'cdap';
}

/** The stats event, once a second while streaming */
export interface CDAPStatsEvent {
	fps: number;
	kbps: number;
	transport: 'cdap';
}

/** The RDClient input surface the toolbar calls */
export interface CDAPInputShim {
	start(): void;
	stop(): void;
	resetKeyboard(): void;
	blockInput(): boolean;
	setBlockInput(b: boolean): void;
}

/**
 * Stub renderer that mirrors the subset of `RDRenderer` the viewer uses
 * (resize + scale mode). Frames are painted directly by the adapter; no
 * codec pipeline is involved.
 */
export class CDAPRenderer {
	canvas: HTMLCanvasElement;
	ctx: CanvasRenderingContext2D | null;
	scaleMode: string;

	constructor(canvas: HTMLCanvasElement) {
		this.canvas = canvas;
		this.ctx = canvas.getContext('2d');
		this.scaleMode = 'fit';
	}
	resize(): void {
		// Canvas auto-sizes from `desktop_meta`; this is a no-op stub.
		// We still update the CSS object-fit rule on `setScaleMode`.
	}
	setScaleMode(mode: string): void {
		this.scaleMode = mode;
		const map: Record<string, string | undefined> = {
			fit: 'contain',
			fill: 'cover',
			'1:1': 'none',
			stretch: 'fill'
		};
		this.canvas.style.objectFit = map[mode] || 'contain';
	}
	// Subset of RDRenderer used elsewhere — left as no-ops so the
	// shared toolbar code does not throw when wired against CDAP.
	drawCursor(): void {
		/* handled by canvas.style.cursor on cursor_update */
	}
	clear(): void {
		try {
			this.ctx?.clearRect(0, 0, this.canvas.width, this.canvas.height);
		} catch {
			/* noop */
		}
	}
}

export class CDAPSession {
	deviceId: string;
	opts: CDAPSessionOptions;
	_state: string;
	_listeners: Record<string, CDAPSessionListener[]>;
	renderer: CDAPRenderer;
	canvas: HTMLCanvasElement;
	ctx: CanvasRenderingContext2D | null;
	input: CDAPInputShim;
	fileTransfer: CDAPFileTransfer;
	_chatWs: WebSocket | null;
	_audioMuted: boolean;
	_blockInput: boolean;
	_clipboardDisabled: boolean;
	_ws: WebSocket | null;
	_connected: boolean;
	_inputBound: boolean;
	_pressedKeys: Set<string>;
	_presenceTimer: ReturnType<typeof setInterval> | null;
	_statsTimer: ReturnType<typeof setInterval> | null;
	_readyTimer: ReturnType<typeof setTimeout> | null;
	_monitors: CDAPMonitor[];
	_activeMonitor: number;
	/** The ready message's session_id */
	_sessionId: unknown;
	/** The last error shown (the same one again within 5 s is not) */
	_lastErrorText: unknown;
	_lastErrorAt: number;
	_sessionActive: boolean;
	_clipboardToLocalEnabled: boolean;
	_targetFps: number;
	_fpsMode: string;
	_backgroundFps: number;
	_streamThrottledActive: boolean | null;
	_frameCount: number;
	_frameBytes: number;
	_lastStatsTime: number;
	_lastFrameTime: number;
	/** The encoding the agent is currently sending (from desktop_meta; MJPEG until then) */
	_activeFormat: string;
	_codecString: string | null;
	_codecFallbackDone: boolean;
	_videoRenderedFrames: number;
	/** RDVideo instance (lazy) */
	_video: RDVideo | null;
	_gotKeyframe: boolean;
	_keyframeRequestedAt: number;
	_onMouseDown: (e: MouseEvent) => void;
	_onMouseUp: (e: MouseEvent) => void;
	_onMouseMove: (e: MouseEvent) => void;
	_onWheel: (e: WheelEvent) => void;
	_onKeyDown: (e: KeyboardEvent) => void;
	_onKeyUp: (e: KeyboardEvent) => void;
	_onContextMenu: (e: Event) => void;
	_onPaste: (e: ClipboardEvent) => void;
	_onWindowBlur: () => void;
	_onVisibilityChange: () => void;

	constructor(canvas: HTMLCanvasElement, opts: CDAPSessionOptions = {}) {
		if (!canvas) throw new Error('Canvas element required');
		if (!opts.deviceId) throw new Error('deviceId required');

		this.deviceId = opts.deviceId;
		this.opts = opts;

		this._state = 'idle';
		this._listeners = {};

		this.renderer = new CDAPRenderer(canvas);
		this.canvas = canvas;
		this.ctx = canvas.getContext('2d');

		// Public RDClient-compatible stubs so the shared toolbar code
		// does not crash.
		this.input = {
			start: () => {
				/* keyboard/mouse are bound on connect */
			},
			stop: () => {
				this._releaseAllKeys(false);
			},
			resetKeyboard: () => {
				this._releaseAllKeys(true);
			},
			blockInput: () => this._blockInput,
			setBlockInput: (b) => this.setBlockInput(b)
		};
		this.fileTransfer = new CDAPFileTransfer({
			deviceId: this.deviceId,
			emit: (event, data) => this._emit(event, data)
		});
		this._chatWs = null;
		this._audioMuted = true;
		this._blockInput = false;
		this._clipboardDisabled = false;

		this._ws = null;
		this._connected = false;
		this._inputBound = false;
		this._pressedKeys = new Set();
		this._presenceTimer = null;
		this._statsTimer = null;
		this._readyTimer = null;
		this._monitors = [];
		this._activeMonitor = 0;
		this._sessionId = null;
		this._lastErrorText = '';
		this._lastErrorAt = 0;
		this._sessionActive = true;
		this._clipboardToLocalEnabled = true;
		this._targetFps = this._normaliseFps(opts.fps || 60);
		this._fpsMode =
			opts.fpsMode !== undefined && ['30', '60', 'adaptive'].includes(opts.fpsMode)
				? opts.fpsMode
				: 'adaptive';
		this._backgroundFps = 1;
		this._streamThrottledActive = null;

		// Stats counters
		this._frameCount = 0;
		this._frameBytes = 0;
		this._lastStatsTime = 0;
		this._lastFrameTime = 0;

		// Video decode pipeline (WebCodecs / JMuxer-MSE). `_activeFormat`
		// tracks the encoding the agent is currently sending; it is set
		// from `desktop_meta` and defaults to the legacy MJPEG path.
		this._activeFormat = 'jpeg';
		this._codecString = null;
		this._codecFallbackDone = false;
		this._videoRenderedFrames = 0;
		this._video = null;
		this._gotKeyframe = false;
		this._keyframeRequestedAt = 0;

		// Bound handlers (so remove works on disconnect)
		this._onMouseDown = this._handleMouseDown.bind(this);
		this._onMouseUp = this._handleMouseUp.bind(this);
		this._onMouseMove = this._handleMouseMove.bind(this);
		this._onWheel = this._handleWheel.bind(this);
		this._onKeyDown = this._handleKeyDown.bind(this);
		this._onKeyUp = this._handleKeyUp.bind(this);
		this._onContextMenu = (e) => e.preventDefault();
		this._onPaste = this._handlePaste.bind(this);
		this._onWindowBlur = this._handleWindowBlur.bind(this);
		this._onVisibilityChange = this._handleVisibilityChange.bind(this);
	}

	resetKeyboard(): void {
		this._releaseAllKeys(true);
	}

	get state(): string {
		return this._state;
	}
	get peerInfo(): CDAPPeerInfo {
		return {
			username: this.deviceId,
			hostname: this.deviceId,
			version: 'cdap',
			platform: ''
		};
	}

	// ── Event Emitter ────────────────────────────────────────────────

	on(event: string, fn: CDAPSessionListener): this {
		(this._listeners[event] = this._listeners[event] || []).push(fn);
		return this;
	}
	off(event: string, fn: CDAPSessionListener): this {
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

	_setState(s: string): void {
		if (this._state === s) return;
		this._state = s;
		this._emit('state', s);
	}

	// ── Public API (RDClient-compatible) ─────────────────────────────

	async connect(): Promise<void> {
		this._setState('connecting');
		this._codecFallbackDone = false;
		this._emit('log', 'Opening CDAP desktop session…');
		debugLog('[CDAP] connect()', this.deviceId);

		const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
		const url = `${proto}//${window.location.host}/api/cdap/devices/${encodeURIComponent(this.deviceId)}/desktop`;
		let ws: WebSocket;
		try {
			ws = new WebSocket(url, ['cdap-desktop']);
			ws.binaryType = 'arraybuffer';
		} catch (err) {
			this._setState('error');
			this._emit('error', errorMessage(err) || 'WebSocket open failed');
			throw err;
		}
		this._ws = ws;

		ws.addEventListener('open', () => this._handleOpen());
		ws.addEventListener('message', (e) => this._handleMessage(e));
		ws.addEventListener('error', () => {
			console.warn('[CDAP] socket error');
			this._emit('log', 'CDAP socket error');
		});
		ws.addEventListener('close', (e) => this._handleClose(e));

		this._connectChat();

		// Don't let the operator stare at "Connecting…" forever.
		// If the agent never replies with `ready` (e.g. screen capture
		// permission denied, agent offline, no admin role on device),
		// surface a clear error after 20s.
		this._readyTimer = setTimeout(() => {
			if (this._state === 'connecting') {
				console.warn('[CDAP] ready timeout — agent did not respond');
				this._emit(
					'error',
					'Agent did not start the desktop session (timeout). ' +
						'Check that the agent is online and screen capture is enabled.'
				);
				try {
					if (this._ws) this._ws.close(4001, 'ready_timeout');
				} catch {
					/* noop */
				}
			}
		}, 20000);
	}

	disconnect(): void {
		this._unbindInput();
		this._stopPresencePing();
		this._stopStats();
		this._closeVideoDecoder();
		this._closeChat();
		this._setAudioActive(false);
		if (this.fileTransfer && typeof this.fileTransfer.close === 'function') {
			try {
				this.fileTransfer.close();
			} catch {
				/* ignore */
			}
		}
		if (this._readyTimer) {
			clearTimeout(this._readyTimer);
			this._readyTimer = null;
		}
		if (this._ws && this._ws.readyState !== WebSocket.CLOSED) {
			try {
				this._ws.close(1000, 'client_disconnect');
			} catch {
				/* ignore */
			}
		}
		this._ws = null;
		this._connected = false;
		this._setState('disconnected');
		this._emit('disconnected', 'user');
	}

	authenticate(_password?: string): Promise<void> {
		// CDAP transport does not use a password challenge.
		return Promise.resolve();
	}
	verify2fa(_code?: string): Promise<void> {
		return Promise.resolve();
	}

	// RDClient toolbar-shim methods. Most are no-ops because the
	// CDAP transport renegotiates quality/cursor/etc. via dedicated
	// control messages instead of in-band login options. They MUST
	// exist so the shared toolbar code in the viewer does not throw
	// when the operator clicks them.

	setQuality(label?: string): void {
		const q = this._qualityFromLabel(label);
		this._send({ type: 'quality_set', quality: q });
	}
	setQualityPreset(preset: string): void {
		const balanced = { quality: 75, fps: 30 };
		const presets: Record<string, { quality: number; fps: number } | undefined> = {
			best: { quality: 92, fps: 60 },
			balanced,
			speed: { quality: 50, fps: 60 }
		};
		const p = presets[String(preset || '').toLowerCase()] || balanced;
		this._targetFps = this._fpsMode === '30' ? 30 : 60;
		this.opts.qualityPreset = preset;
		this._send({
			type: 'quality_set',
			quality: p.quality,
			fps: this._targetFps,
			fps_mode: this._fpsMode
		});
	}
	setFps(fps: number): void {
		const n = this._normaliseFps(fps);
		if (this._sessionActive) {
			this._targetFps = n;
		}
		this._send({ type: 'quality_set', fps: n });
	}
	setFpsMode(mode: string): void {
		const value = ['30', '60', 'adaptive'].includes(String(mode)) ? String(mode) : '30';
		const fps = value === '30' ? 30 : 60;
		this._fpsMode = value;
		this.opts.fpsMode = value;
		this.opts.fps = fps;
		this._targetFps = fps;
		this._send({ type: 'quality_set', fps, fps_mode: value });
	}
	setScaleMode(mode: string): void {
		try {
			this.renderer.setScaleMode(mode);
		} catch {
			/* noop */
		}
	}
	setShowCursor(_b?: boolean): void {
		/* CDAP cursor is server-driven */
	}
	setShowRemoteCursor(b: boolean): void {
		this._send({ type: 'show_cursor', enabled: !!b });
	}
	setLockAfterSession(b: boolean): void {
		this._send({ type: 'lock_after_session', enabled: !!b });
	}
	setPrivacyMode(b: boolean): void {
		this._send({ type: 'privacy_mode', enabled: !!b });
	}
	setDisableClipboard(b: boolean): void {
		this._clipboardDisabled = !!b;
		this._clipboardToLocalEnabled = !b && this._sessionActive;
		this._send({ type: 'disable_clipboard', enabled: !!b });
	}
	setBlockInput(b: boolean): void {
		this._blockInput = !!b;
		this._send({ type: 'block_input', enabled: !!b });
	}
	setAudioMuted(muted: boolean): void {
		this._audioMuted = !!muted;
		this._setAudioActive(!this._audioMuted);
	}

	/**
	 * Mark whether this client is the active tab in the multi-session viewer.
	 */
	setSessionActive(active: boolean): void {
		const next = !!active;
		const changed = next !== this._sessionActive;
		this._sessionActive = next;
		this._clipboardToLocalEnabled = next;
		this._syncInputCapture();
		if (this._connected) {
			this._syncStreamThrottle();
		} else if (changed) {
			this._syncStreamThrottle();
		}
	}

	setBackgroundFps(fps: number): void {
		const n = Number(fps);
		if (Number.isFinite(n) && n >= 1 && n <= 5) {
			this._backgroundFps = Math.round(n);
		}
	}

	_syncStreamThrottle(): void {
		if (!this._connected) return;
		const wantActive = this._sessionActive;
		if (this._streamThrottledActive === wantActive) return;
		this._streamThrottledActive = wantActive;
		if (wantActive) {
			if (this._video && this._video.setBackgroundMode) {
				this._video.setBackgroundMode(false);
			}
			this.setFps(this._targetFps || this._normaliseFps(this.opts.fps || 60));
			this._requestKeyframe();
		} else {
			if (this._video && this._video.setBackgroundMode) {
				this._video.setBackgroundMode(true);
			}
			this._send({ type: 'quality_set', fps: this._backgroundFps || 1 });
		}
	}

	_syncInputCapture(): void {
		if (this._sessionActive && this._connected) {
			this._bindInput();
		} else {
			this._unbindInput();
		}
	}

	requestKeyframe(): void {
		this._send({ type: 'keyframe_request' });
	}
	sendRefreshScreen(): void {
		this._send({ type: 'keyframe_request' });
	}
	sendCAD(): void {
		this.sendCtrlAltDel();
	}
	sendCtrlAltDel(): void {
		// Synthesised as Ctrl+Alt+Delete key combo.
		const send = (key: string, code: string, down: boolean) =>
			this._send({
				type: 'input',
				input_type: 'keyboard',
				key,
				code,
				down,
				modifiers: { ctrl: down, alt: down, shift: false, meta: false }
			});
		send('Control', 'ControlLeft', true);
		send('Alt', 'AltLeft', true);
		send('Delete', 'Delete', true);
		send('Delete', 'Delete', false);
		send('Alt', 'AltLeft', false);
		send('Control', 'ControlLeft', false);
	}
	sendLockScreen(): void {
		this._send({ type: 'lock_screen' });
	}
	sendRestart(): void {
		this.sendRestartRemoteDevice();
	}
	sendRestartRemoteDevice(): void {
		this._send({ type: 'restart_device' });
	}
	sendClipboard(text: string): boolean {
		if (!text) return false;
		return this.sendText(text);
	}
	toggleAudio(): boolean {
		this.setAudioMuted(!this._audioMuted);
		return !this._audioMuted;
	}
	sendChat(msg: string): boolean {
		const text = String(msg || '').trim();
		if (!text) return false;
		if (!this._chatWs || this._chatWs.readyState !== WebSocket.OPEN) {
			this._connectChat();
			// Best-effort: queue briefly after reconnect
			setTimeout(() => {
				if (this._chatWs && this._chatWs.readyState === WebSocket.OPEN) {
					this._chatWs.send(JSON.stringify({ type: 'message', text } satisfies CDAPChatMessage));
				}
			}, 500);
			return true;
		}
		this._chatWs.send(JSON.stringify({ type: 'message', text } satisfies CDAPChatMessage));
		return true;
	}

	_connectChat(): void {
		if (
			this._chatWs &&
			(this._chatWs.readyState === WebSocket.OPEN || this._chatWs.readyState === WebSocket.CONNECTING)
		) {
			return;
		}
		const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
		const url = `${proto}//${window.location.host}/ws/chat-operator/${encodeURIComponent(this.deviceId)}`;
		let ws: WebSocket;
		try {
			ws = new WebSocket(url);
		} catch (err) {
			console.warn('[CDAP] chat WS open failed:', err);
			return;
		}
		this._chatWs = ws;
		ws.addEventListener('message', (ev: MessageEvent<unknown>) => {
			try {
				const frame = asRecord<CDAPChatFrame>(JSON.parse(String(ev.data)));
				// The relay echoes the operator's own messages back: only the device's are received
				if (frame.type === 'message' && frame.text && frame.from !== 'operator') {
					this._emit('chat', frame.text);
				} else if (frame.type === 'history' && Array.isArray(frame.messages)) {
					const messages: unknown[] = frame.messages;
					for (const item of messages) {
						const m = asRecord<CDAPChatFrame>(item);
						if (m && m.text && m.from !== 'operator') {
							this._emit('chat', m.text);
						}
					}
				}
			} catch {
				/* ignore */
			}
		});
		ws.addEventListener('open', () => {
			try {
				ws.send(JSON.stringify({ type: 'get_history' } satisfies CDAPChatMessage));
			} catch {
				/* ignore */
			}
		});
	}

	_closeChat(): void {
		if (this._chatWs) {
			try {
				this._chatWs.close();
			} catch {
				/* ok */
			}
		}
		this._chatWs = null;
	}

	_setAudioActive(active: boolean): void {
		const widgetId = 'remote-audio';
		if (active) {
			// Ensure a mount point exists for cdap-audio.ts
			let el = document.getElementById('wval-remote-audio');
			if (!el) {
				el = document.createElement('div');
				el.id = 'wval-remote-audio';
				el.className = 'cdap-audio-widget';
				el.style.display = 'none';
				el.setAttribute('aria-hidden', 'true');
				el.innerHTML =
					'<div class="cdap-audio-status"></div><div class="cdap-audio-level"><div class="cdap-audio-level-fill"></div></div>';
				document.body.appendChild(el);
			}
			if (!CDAPAudio.isActive(this.deviceId, widgetId)) {
				CDAPAudio.open(this.deviceId, widgetId, { direction: 'receive' });
			}
			if (CDAPAudio.isMuted(this.deviceId, widgetId)) {
				CDAPAudio.toggleMute(this.deviceId, widgetId);
			}
		} else if (CDAPAudio.isActive(this.deviceId, widgetId)) {
			CDAPAudio.close(this.deviceId, widgetId);
		}
	}

	// Monitors — populated from `monitor_list` control messages.
	getMonitors(): CDAPMonitor[] {
		return this._monitors.slice();
	}
	switchMonitor(idx: number): void {
		const i = Math.max(0, Math.min(idx | 0, Math.max(this._monitors.length - 1, 0)));
		this._activeMonitor = i;
		this._send({ type: 'monitor_select', index: i });
	}

	// Fullscreen — delegate to the container the toolbar passes in.
	toggleFullscreen(container?: Element | null): void {
		const target = container || this.canvas.closest('.viewer-container') || this.canvas;
		if (!document.fullscreenElement) {
			if (target.requestFullscreen) target.requestFullscreen().catch(() => {});
		} else if (document.exitFullscreen) {
			document.exitFullscreen().catch(() => {});
		}
	}

	// Remote-side recording stubs: the viewer records locally with
	// MediaRecorder over canvas.captureStream().
	startRecording(): boolean {
		return false;
	}
	stopRecording(): boolean {
		return false;
	}
	downloadRecording(): boolean {
		return false;
	}
	isRecording(): boolean {
		return false;
	}

	getStats(): CDAPSessionStats {
		return {
			frames: this._frameCount,
			bytes: this._frameBytes,
			fps: 0,
			kbps: 0,
			transport: 'cdap'
		};
	}

	// ── Internal: WS lifecycle ───────────────────────────────────────

	_handleOpen(): void {
		decodableCodecs()
			.then((codecs) => {
				if (!this._ws || this._ws.readyState !== WebSocket.OPEN) return;
				this._sendDesktopStart(codecs);
			})
			.catch(() => {
				if (!this._ws || this._ws.readyState !== WebSocket.OPEN) return;
				this._sendDesktopStart(decodableCodecsSync());
			});
	}

	_sendDesktopStart(codecs: string[]): void {
		const dpr = window.devicePixelRatio || 1;
		const rect = this.canvas.getBoundingClientRect();
		const screenW = (window.screen && window.screen.width) || 1920;
		const screenH = (window.screen && window.screen.height) || 1080;

		const quality = this._qualityFromLabel(this.opts.imageQuality);
		const fps = this._normaliseFps(this.opts.fps);

		this._send({
			width: screenW,
			height: screenH,
			quality,
			fps,
			fps_mode: this._fpsMode,
			device_pixel_ratio: dpr,
			client_css_width: Math.round(rect.width || 0),
			client_css_height: Math.round(rect.height || 0),
			// Advertise the codecs this browser can decode so the agent
			// sends a GPU-encoded video stream. Over plain HTTP this still
			// includes H.264 (decoded via JMuxer/MSE), avoiding the slow
			// MJPEG fallback. See decodableCodecs().
			codecs: codecs,
			video_codec: 'auto'
		});

		this._startPresencePing();
	}

	_handleClose(e: CloseEvent): void {
		this._unbindInput();
		this._stopPresencePing();
		this._stopStats();
		this._closeVideoDecoder();
		if (this._readyTimer) {
			clearTimeout(this._readyTimer);
			this._readyTimer = null;
		}
		this._connected = false;
		const reason = (e && e.reason) || 'closed';
		debugLog('[CDAP] socket closed', e && e.code, reason);
		this._setState('disconnected');
		this._emit('disconnected', reason);
	}

	_handleMessage(event: MessageEvent<unknown>): void {
		// Binary fast path: encoded frame bytes. Route to the video
		// decoder for codec streams (h264/vp9/av1) or the image path for
		// MJPEG/WebP, based on the format last reported via desktop_meta.
		if (event.data instanceof ArrayBuffer) {
			const bytes = new Uint8Array(event.data);
			// Safety net when desktop_meta was not relayed (older servers):
			// JPEG SOI marker 0xFF 0xD8 at the start of the payload.
			if (!isVideoCodec(this._activeFormat) && bytes.length >= 2 && bytes[0] === 0xff && bytes[1] === 0xd8) {
				this._applyFormat('jpeg');
			}
			if (isVideoCodec(this._activeFormat)) {
				this._feedVideoFrame(event.data);
			} else {
				this._renderBinaryFrame(event.data);
			}
			return;
		}
		let msg: unknown;
		try {
			msg = JSON.parse(String(event.data));
		} catch {
			return;
		}
		this._dispatchControl(asRecord<CDAPControlMessage>(msg));
	}

	_dispatchControl(msg: CDAPControlMessage): void {
		switch (msg.type) {
			case 'ready':
				if (this._readyTimer) {
					clearTimeout(this._readyTimer);
					this._readyTimer = null;
				}
				this._sessionId = msg.session_id || null;
				this._connected = true;
				this._setState('streaming');
				this._syncInputCapture();
				this._startStats();
				this._emit('login_success');
				this._emit('session_start');
				this._emit('log', 'Streaming');
				if (!this._sessionActive) {
					this._streamThrottledActive = null;
					this._syncStreamThrottle();
				}
				debugLog('[CDAP] ready, session=', this._sessionId);
				break;

			case 'desktop_meta':
				if (msg.width && msg.height) {
					if (this.canvas.width !== msg.width) this.canvas.width = Number(msg.width);
					if (this.canvas.height !== msg.height) this.canvas.height = Number(msg.height);
				}
				this._applyFormat(text(msg.format), text(msg.codec_string));
				break;

			case 'frame':
				// Legacy JSON frame envelope (data URI / base64 jpeg).
				if (msg.data) this._renderEncodedFrame(msg);
				break;

			case 'cursor_update':
				this._applyCursor(msg);
				break;

			case 'clipboard_update':
				this._handleClipboardUpdate(msg);
				break;

			case 'monitor_list': {
				// Agent reports the available displays. Cache and notify
				// the viewer so the toolbar can populate its dropdown.
				const list: unknown[] = Array.isArray(msg.monitors) ? msg.monitors : [];
				this._monitors = list.map((item, idx) => {
					const m = asRecord<CDAPMonitorItem>(item);
					return {
						idx: typeof m.idx === 'number' ? m.idx : idx,
						name: String(m.name || `Monitor ${idx + 1}`),
						osName: String(m.name || ''),
						primary: !!m.primary,
						width: Number(m.width) | 0,
						height: Number(m.height) | 0
					};
				});
				if (typeof msg.active === 'number') this._activeMonitor = msg.active;
				debugLog('[CDAP] monitor_list', this._monitors);
				this._emit('monitors', this._monitors);
				break;
			}

			case 'codec_answer':
				if (msg.video_codec) {
					this._applyFormat(String(msg.video_codec).toLowerCase(), text(msg.codec_string));
				}
				break;

			case 'quality_adjust':
				break;

			case 'consent_required':
			case 'permission_required':
				this._emit('log', msg.message || 'Awaiting user consent on the device…');
				debugLog('[CDAP] consent_required', msg);
				break;

			case 'error':
				if (this._readyTimer) {
					clearTimeout(this._readyTimer);
					this._readyTimer = null;
				}
				{
					const text = msg.error || msg.message || 'CDAP error';
					const now = performance.now();
					if (text !== this._lastErrorText || now - this._lastErrorAt > 5000) {
						this._lastErrorText = text;
						this._lastErrorAt = now;
						console.error('[CDAP] error', text);
						this._emit('error', text);
					}
				}
				break;

			case 'end':
				if (this._readyTimer) {
					clearTimeout(this._readyTimer);
					this._readyTimer = null;
				}
				debugLog('[CDAP] end', msg);
				this._setState('disconnected');
				this._emit('disconnected', msg.reason || 'agent_end');
				break;

			default:
				// Surface unknown types in dev tools so we can spot
				// protocol drift between agent and gateway quickly.
				log.debugDebug('[CDAP] unhandled message', msg.type, msg);
				break;
		}
	}

	// ── Frame rendering ──────────────────────────────────────────────

	_renderBinaryFrame(buf: ArrayBuffer): void {
		const mime = this._activeFormat === 'webp' ? 'image/webp' : 'image/jpeg';
		const blob = new Blob([buf], { type: mime });
		this._frameCount++;
		this._frameBytes += buf.byteLength;
		this._lastFrameTime = performance.now();
		// Prefer createImageBitmap (off-thread decode); fall back to Image.
		if (typeof createImageBitmap === 'function') {
			createImageBitmap(blob)
				.then((bm) => {
					if (bm.width !== this.canvas.width || bm.height !== this.canvas.height) {
						if (bm.width > 0 && bm.height > 0) {
							this.canvas.width = bm.width;
							this.canvas.height = bm.height;
						}
					}
					try {
						this.ctx?.drawImage(bm, 0, 0);
					} catch {
						/* noop */
					}
					if (bm.close) bm.close();
				})
				.catch(() => {
					/* drop frame on decode error */
				});
		} else {
			const url = URL.createObjectURL(blob);
			const img = new Image();
			img.onload = () => {
				try {
					if (img.width !== this.canvas.width || img.height !== this.canvas.height) {
						this.canvas.width = img.width;
						this.canvas.height = img.height;
					}
					this.ctx?.drawImage(img, 0, 0);
				} catch {
					/* noop */
				}
				URL.revokeObjectURL(url);
			};
			img.onerror = () => URL.revokeObjectURL(url);
			img.src = url;
		}
	}

	// ── Video decode pipeline (WebCodecs / JMuxer-MSE) ───────────────

	/**
	 * React to a format change announced by the agent (`desktop_meta`).
	 * Tears down or (re)creates the video decoder as needed so the binary
	 * frame path knows whether to feed the codec decoder or the image
	 * decoder.
	 * @param format e.g. 'h264' | 'vp9' | 'av1' | 'webp' | 'jpeg'
	 * @param codecString WebCodecs codec string from the agent
	 */
	_applyFormat(format?: string, codecString?: string): void {
		const fmt = (format || this._activeFormat || 'jpeg').toLowerCase();
		if (fmt === this._activeFormat && codecString === this._codecString) return;
		const prevWasVideo = isVideoCodec(this._activeFormat);
		this._activeFormat = fmt;
		this._codecString = codecString || null;
		this._gotKeyframe = false;

		if (isVideoCodec(fmt)) {
			this._ensureVideoDecoder(fmt);
		} else if (prevWasVideo) {
			// Switched back to an image format — release the decoder.
			this._closeVideoDecoder();
		}
	}

	/**
	 * Lazily create the shared `RDVideo` decoder (WebCodecs when secure,
	 * JMuxer/MSE H.264 otherwise) and bind its decoded-frame callback to
	 * the canvas.
	 */
	_ensureVideoDecoder(codecName: string): void {
		if (this._video && this._video.currentCodec === codecName && this._video.initialized) {
			return;
		}
		this._closeVideoDecoder();
		this._videoRenderedFrames = 0;
		const v = new RDVideo();
		this._video = v;
		v.onFrame = (frame) => {
			this._videoRenderedFrames++;
			this._drawVideoFrame(frame);
		};
		v.onError = () => {
			this._gotKeyframe = false;
			this._requestKeyframe();
		};
		v.onCodecFailed = (failedCodec) => {
			this._handleCodecFailed(failedCodec);
		};
		v.onNeedKeyframe = () => this._requestKeyframe();
		v.init(codecName, { codecString: this._codecString || null }).catch((err: unknown) => {
			console.warn('[CDAP] video decoder init failed:', err && errorMessage(err));
			this._handleCodecFailed(codecName);
		});
	}

	_handleCodecFailed(codec: string | null | undefined): void {
		const c = String(codec || '').toLowerCase();
		if (!c || this._codecFallbackDone) return;
		blockWireCodec(c);
		this._codecFallbackDone = true;
		this._closeVideoDecoder();
		this._gotKeyframe = false;
		this._emit('log', 'Codec ' + c.toUpperCase() + ' failed — reconnecting with VP9/H.264…');
		const ws = this._ws;
		try {
			if (ws) ws.close(4002, 'codec_fallback');
		} catch {
			/* noop */
		}
		setTimeout(() => {
			if (this._state === 'disconnected' || this._state === 'error') {
				this.connect().catch(() => {});
			}
		}, 400);
	}

	/**
	 * Feed one binary video frame to the decoder. The agent prefixes each
	 * access unit with a single flag byte (bit0 = keyframe); the server
	 * has already stripped the 64-byte session id prefix.
	 */
	_feedVideoFrame(buf: ArrayBuffer): void {
		if (!this._sessionActive) return;
		const bytes = new Uint8Array(buf);
		if (bytes.length < 2) return;
		const isKey = ((bytes[0] ?? 0) & 1) === 1;
		const payload = bytes.subarray(1);

		this._frameCount++;
		this._frameBytes += buf.byteLength;
		this._lastFrameTime = performance.now();

		if (!this._video) {
			this._ensureVideoDecoder(this._activeFormat);
			if (!this._video) return;
		}
		// Wait for the first keyframe before feeding the decoder; delta
		// frames decoded without a reference produce errors/artifacts.
		if (!this._gotKeyframe) {
			if (!isKey) {
				this._requestKeyframe();
				return;
			}
			this._gotKeyframe = true;
		}

		this._video.decode({
			data: payload,
			key: isKey,
			codec: this._activeFormat,
			codecString: this._codecString || null
		});
	}

	/**
	 * Draw a decoded frame (WebCodecs `VideoFrame` or JMuxer `<video>`
	 * proxy) onto the canvas, resizing it to the frame dimensions.
	 */
	_drawVideoFrame(frame: RDDecodedFrame): void {
		try {
			const w = frame.displayWidth || this.canvas.width;
			const h = frame.displayHeight || this.canvas.height;
			if (w > 0 && h > 0 && (this.canvas.width !== w || this.canvas.height !== h)) {
				this.canvas.width = w;
				this.canvas.height = h;
			}
			const src = '_source' in frame ? frame._source || frame : frame;
			this.ctx?.drawImage(src, 0, 0, this.canvas.width, this.canvas.height);
		} catch {
			/* drop frame on draw error */
		} finally {
			try {
				if (frame.close) frame.close();
			} catch {
				/* noop */
			}
		}
	}

	/** Ask the agent to emit a fresh keyframe (throttled to ~1/s). */
	_requestKeyframe(): void {
		const now = performance.now();
		if (now - this._keyframeRequestedAt < 1000) return;
		this._keyframeRequestedAt = now;
		this._send({ type: 'keyframe_request' });
	}

	/** Tear down the video decoder and release GPU/DOM resources. */
	_closeVideoDecoder(): void {
		if (this._video) {
			try {
				this._video.close();
			} catch {
				/* noop */
			}
			this._video = null;
		}
		this._gotKeyframe = false;
	}

	_renderEncodedFrame(msg: CDAPControlMessage): void {
		if (!this._sessionActive) return;
		const fmt = msg.format || 'jpeg';
		const data = msg.data;
		// The frame is text (a data URI or base64); anything else is no frame
		if (typeof data !== 'string') return;
		const src = data.startsWith('data:') ? data : `data:image/${String(fmt)};base64,${data}`;
		this._frameCount++;
		this._lastFrameTime = performance.now();
		const img = new Image();
		img.onload = () => {
			try {
				if (msg.width && this.canvas.width !== msg.width) this.canvas.width = Number(msg.width);
				if (msg.height && this.canvas.height !== msg.height) this.canvas.height = Number(msg.height);
				this.ctx?.drawImage(img, 0, 0);
			} catch {
				/* noop */
			}
		};
		img.src = src;
	}

	// ── Cursor + clipboard ───────────────────────────────────────────

	_applyCursor(msg: CDAPControlMessage): void {
		if (msg.hidden) {
			this.canvas.style.cursor = 'none';
			return;
		}
		// Best-effort: leave system cursor visible. Custom cursor PNG
		// assembly mirrors `cdap-desktop.js` but is costly per frame;
		// CDAP cursor frames are infrequent so this can be added in a
		// follow-up without affecting steady-state perf.
		this.canvas.style.cursor = 'default';
	}

	_handleClipboardUpdate(msg: CDAPControlMessage): void {
		// Mirror device → operator clipboard when the agent allows it.
		// Server clipboard.go sends `data`; some paths may still use `text`.
		const text = msg.data || msg.text;
		if (!text || this._clipboardDisabled) return;
		if (this._clipboardToLocalEnabled && navigator.clipboard && navigator.clipboard.writeText) {
			navigator.clipboard.writeText(String(text)).catch(() => {
				/* permission denied */
			});
		}
		this._emit('clipboard', text);
	}

	// ── Input ────────────────────────────────────────────────────────

	_bindInput(): void {
		if (this._inputBound) return;
		const c = this.canvas;
		c.addEventListener('mousedown', this._onMouseDown);
		c.addEventListener('mouseup', this._onMouseUp);
		c.addEventListener('mousemove', this._onMouseMove);
		c.addEventListener('wheel', this._onWheel, { passive: false });
		c.addEventListener('contextmenu', this._onContextMenu);
		c.addEventListener('paste', this._onPaste);
		document.addEventListener('keydown', this._onKeyDown);
		document.addEventListener('keyup', this._onKeyUp);
		window.addEventListener('blur', this._onWindowBlur);
		document.addEventListener('visibilitychange', this._onVisibilityChange);
		c.tabIndex = 0;
		c.focus();
		this._inputBound = true;
	}

	_unbindInput(): void {
		if (!this._inputBound) return;
		this._releaseAllKeys(false);
		const c = this.canvas;
		c.removeEventListener('mousedown', this._onMouseDown);
		c.removeEventListener('mouseup', this._onMouseUp);
		c.removeEventListener('mousemove', this._onMouseMove);
		c.removeEventListener('wheel', this._onWheel);
		c.removeEventListener('contextmenu', this._onContextMenu);
		c.removeEventListener('paste', this._onPaste);
		document.removeEventListener('keydown', this._onKeyDown);
		document.removeEventListener('keyup', this._onKeyUp);
		window.removeEventListener('blur', this._onWindowBlur);
		document.removeEventListener('visibilitychange', this._onVisibilityChange);
		this._inputBound = false;
	}

	_handleWindowBlur(): void {
		if (this._inputBound && this._connected) {
			this._releaseAllKeys(false);
		}
	}

	_handleVisibilityChange(): void {
		if (
			this._inputBound &&
			this._connected &&
			typeof document !== 'undefined' &&
			document.visibilityState === 'hidden'
		) {
			this._releaseAllKeys(false);
		}
	}

	_releaseAllKeys(forceAllModifiers: boolean): void {
		if (!this._connected) {
			this._pressedKeys.clear();
			return;
		}
		for (const code of [...this._pressedKeys]) {
			this._send({
				type: 'input',
				input_type: 'keyboard',
				key: '',
				code,
				modifiers: { ctrl: false, alt: false, shift: false, meta: false },
				down: false
			});
		}
		this._pressedKeys.clear();
		if (forceAllModifiers) {
			for (const code of [
				'ShiftLeft',
				'ShiftRight',
				'ControlLeft',
				'ControlRight',
				'AltLeft',
				'AltRight',
				'MetaLeft',
				'MetaRight'
			]) {
				this._send({
					type: 'input',
					input_type: 'keyboard',
					key: '',
					code,
					modifiers: { ctrl: false, alt: false, shift: false, meta: false },
					down: false
				});
			}
		}
	}

	_isInputFocused(): boolean {
		const el = document.activeElement;
		if (!el) return false;
		// Hidden inputs (e.g. password field after authenticate) do
		// not block keyboard capture.
		if ('offsetParent' in el && el.offsetParent === null) return false;
		const tag = el.tagName;
		return (
			tag === 'INPUT' || tag === 'TEXTAREA' || ('isContentEditable' in el && el.isContentEditable === true)
		);
	}

	_coords(e: MouseEvent): { x: number; y: number } {
		const rect = this.canvas.getBoundingClientRect();
		return {
			x: Math.round(((e.clientX - rect.left) / rect.width) * this.canvas.width),
			y: Math.round(((e.clientY - rect.top) / rect.height) * this.canvas.height)
		};
	}

	_mouseButton(e: MouseEvent): number {
		if (e.button === 0) return MOUSE_BUTTON_LEFT;
		if (e.button === 2) return MOUSE_BUTTON_RIGHT;
		if (e.button === 1) return MOUSE_BUTTON_MIDDLE;
		return 0;
	}

	_handleMouseDown(e: MouseEvent): void {
		if (!this._connected) return;
		const { x, y } = this._coords(e);
		const btn = this._mouseButton(e);
		this._send({ type: 'input', input_type: 'mouse', x, y, button: MOUSE_TYPE_DOWN | (btn << 3) });
		e.preventDefault();
		this.canvas.focus();
	}
	_handleMouseUp(e: MouseEvent): void {
		if (!this._connected) return;
		const { x, y } = this._coords(e);
		const btn = this._mouseButton(e);
		this._send({ type: 'input', input_type: 'mouse', x, y, button: MOUSE_TYPE_UP | (btn << 3) });
		e.preventDefault();
	}
	_handleMouseMove(e: MouseEvent): void {
		if (!this._connected) return;
		const { x, y } = this._coords(e);
		this._send({ type: 'input', input_type: 'mouse', x, y, button: MOUSE_TYPE_MOVE });
	}
	_handleWheel(e: WheelEvent): void {
		if (!this._connected) return;
		const { x, y } = this._coords(e);
		const delta = normalizeWheelDelta(e);
		if (!delta) return;
		this._send({
			type: 'input',
			input_type: 'mouse',
			x,
			y,
			deltaX: delta.dx,
			deltaY: delta.dy,
			button: MOUSE_TYPE_WHEEL
		});
		e.preventDefault();
	}

	_handleKeyDown(e: KeyboardEvent): void {
		if (!this._connected) return;
		if (this._isInputFocused()) return;
		// Drop OS-level auto-repeat: the agent synthesises repeats on
		// the remote side.
		if (e.repeat) {
			e.preventDefault();
			return;
		}
		this._pressedKeys.add(e.code);
		// Unicode → text fallback (modifierless single printable
		// non-alphanumeric char gets routed via input_type: text so the
		// agent's OS-side layout handles it).
		if (typeof e.key === 'string' && e.key.length === 1 && !e.ctrlKey && !e.altKey && !e.metaKey) {
			const cc = e.key.charCodeAt(0);
			const printable = cc >= 0x20 && cc !== 0x7f;
			const ascii = cc < 0x80;
			const isAlphaNum =
				ascii && ((cc >= 0x30 && cc <= 0x39) || (cc >= 0x41 && cc <= 0x5a) || (cc >= 0x61 && cc <= 0x7a));
			if (printable && (!isAlphaNum || !ascii)) {
				this._send({ type: 'input', input_type: 'text', text: e.key });
				e.preventDefault();
				return;
			}
		}
		this._sendKeyEvent(e, true);
		e.preventDefault();
	}
	_handleKeyUp(e: KeyboardEvent): void {
		if (!this._connected) return;
		if (this._isInputFocused()) return;
		this._pressedKeys.delete(e.code);
		this._sendKeyEvent(e, false);
		e.preventDefault();
	}

	_sendKeyEvent(e: KeyboardEvent, down: boolean): void {
		this._send({
			type: 'input',
			input_type: 'keyboard',
			key: e.key,
			code: e.code,
			modifiers: {
				ctrl: e.ctrlKey,
				alt: e.altKey,
				shift: e.shiftKey,
				meta: e.metaKey
			},
			down
		});
	}

	_handlePaste(e: ClipboardEvent): void {
		if (!this._connected) return;
		const text = e.clipboardData?.getData('text/plain');
		if (text) this._send({ type: 'input', input_type: 'text', text });
		e.preventDefault();
	}

	// ── Public helpers used by the viewer toolbar (Paste / Text) ─────

	sendText(text: string): boolean {
		if (!this._connected || !text) return false;
		this._send({ type: 'input', input_type: 'text', text: String(text) });
		return true;
	}

	async pasteFromClipboard(): Promise<boolean> {
		if (!navigator.clipboard || !navigator.clipboard.readText) return false;
		try {
			const text = await navigator.clipboard.readText();
			if (!text) return false;
			return this.sendText(text);
		} catch {
			return false;
		}
	}

	// ── Wire helpers ─────────────────────────────────────────────────

	_send(payload: CDAPDesktopMessage): void {
		const ws = this._ws;
		if (ws && ws.readyState === WebSocket.OPEN) {
			ws.send(JSON.stringify(payload));
		}
	}

	_qualityFromLabel(label?: string): number {
		switch (String(label || '').toLowerCase()) {
			case 'best':
				return 92;
			case 'low':
				return 50;
			case 'speed':
				return 50;
			case 'balanced':
			default:
				return 75;
		}
	}
	_normaliseFps(fps: unknown): number {
		const n = Number(fps);
		if (!Number.isFinite(n) || n <= 0) return 30;
		return Math.min(60, Math.max(5, Math.round(n)));
	}

	// ── Presence ping ────────────────────────────────────────────────

	_startPresencePing(): void {
		this._stopPresencePing();
		this._presenceTimer = setInterval(() => {
			this._send({ type: 'presence_ping', t: Date.now() });
		}, PRESENCE_PING_MS);
	}
	_stopPresencePing(): void {
		if (this._presenceTimer) {
			clearInterval(this._presenceTimer);
			this._presenceTimer = null;
		}
	}

	// ── Stats (1s aggregate, fed to the toolbar) ─────────────────────

	_startStats(): void {
		this._stopStats();
		this._lastStatsTime = performance.now();
		this._statsTimer = setInterval(() => {
			const now = performance.now();
			const dt = (now - this._lastStatsTime) / 1000;
			if (dt <= 0) return;
			const fps = this._frameCount / dt;
			const kbps = (this._frameBytes * 8) / 1000 / dt;
			this._frameCount = 0;
			this._frameBytes = 0;
			this._lastStatsTime = now;
			this._emit('stats', {
				fps: Math.round(fps * 10) / 10,
				kbps: Math.round(kbps),
				transport: 'cdap'
			} satisfies CDAPStatsEvent);
		}, STATS_INTERVAL_MS);
	}
	_stopStats(): void {
		if (this._statsTimer) {
			clearInterval(this._statsTimer);
			this._statsTimer = null;
		}
	}
}
