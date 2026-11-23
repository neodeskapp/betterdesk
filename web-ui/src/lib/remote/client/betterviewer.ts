/**
 * BetterViewer — NeoDesk browser remote desktop (MNG_KVM protocol, AGPL-3.0).
 * SPDX-License-Identifier: AGPL-3.0-only
 * Copyright (c) BetterDesk contributors
 */

const CMD_TILE = 3;
const CMD_SCREEN = 7;
const CMD_GET_DISPLAYS = 11;
const CMD_INPUT_LOCK = 87;
const CMD_KEYSTATE = 18;

const INPUT_KEY = 1;
const INPUT_MOUSE = 2;
const INPUT_CTRLALTDEL = 10;
export const INPUT_TOUCH = 15;
export const INPUT_KEYUNICODE = 85;

const KEY_DOWN = 1;
const KEY_UP = 2;
const KEY_SCROLL = 3;

const MOUSE_LEFT = 0x02;
const MOUSE_RIGHT = 0x08;
const MOUSE_MIDDLE = 0x20;

export const MESH_KVM_MAGIC = [0x11, 0xfe, 0x00, 0x00, 0x4d, 0x45, 0x53, 0x48, 0x00, 0x00, 0x00, 0x00, 0x02];

// Bytes that are no packet header the parser skips in a row before it gives up on the stream
const MAX_JUNK = 64 * 1024;

export function shortToBytes(v: number): number[] {
	return [(v >> 8) & 0xff, v & 0xff];
}

export interface BetterViewerOptions {
	/** Send no keyboard or mouse input */
	viewOnly?: boolean;
}

/** An event listener; each event has its own data (see the _emit calls) */
export type BetterViewerListener = (...args: never[]) => void;

export interface BetterViewerStats {
	fps: number;
	/** Bits per second */
	bitrate: number;
	/** Tiles drawn since the last stats event */
	frames: number;
	/** Bytes received since the last stats event */
	bytes: number;
}

/** A decoded tile waiting for the tiles before it */
interface PendingTile {
	seq: number;
	img: ImageBitmap;
	x: number;
	y: number;
}

/** An input listener grabInput added: target, type, listener, options */
type InputListener = [EventTarget, string, EventListener, AddEventListenerOptions | undefined];

export class BetterViewer {
	canvas: HTMLCanvasElement;
	ctx: CanvasRenderingContext2D | null;
	opts: BetterViewerOptions;
	_ws: WebSocket | null;
	_relayReady: boolean;
	_protocolSent: boolean;
	_accum: Uint8Array<ArrayBuffer>;
	_junk: number;
	/** 0 idle, 3 streaming */
	_state: number;
	_screenW: number;
	_screenH: number;
	_tilesPending: PendingTile[];
	_tilesDrawn: number;
	_tilesReceived: number;
	_killDraw: number;
	_imageType: number;
	_compression: number;
	_scaling: number;
	_frameTimer: number;
	_handlers: Record<string, BetterViewerListener[]>;
	_stats: BetterViewerStats;
	_statsTimer: ReturnType<typeof setInterval> | null;
	_lastStats: number;
	_inputGrabbed: boolean;
	_inputListeners: InputListener[];
	/** Only the active tab of the viewer sends keys and mouse input */
	_active: boolean;
	_pressedKeys: number[];
	_viewOnly: boolean;

	constructor(canvas: HTMLCanvasElement, opts?: BetterViewerOptions | null) {
		this.canvas = canvas;
		this.ctx = canvas.getContext('2d');
		this.opts = opts || {};
		this._ws = null;
		this._relayReady = false;
		this._protocolSent = false;
		this._accum = new Uint8Array(0);
		this._junk = 0;
		this._state = 0;
		this._screenW = 0;
		this._screenH = 0;
		this._tilesPending = [];
		this._tilesDrawn = 0;
		this._tilesReceived = 0;
		this._killDraw = 0;
		this._imageType = 1;
		this._compression = 50;
		this._scaling = 1024;
		this._frameTimer = 100;
		this._handlers = {};
		this._stats = { fps: 0, bitrate: 0, frames: 0, bytes: 0 };
		this._statsTimer = null;
		this._lastStats = Date.now();
		this._inputGrabbed = false;
		this._inputListeners = [];
		this._active = true;
		this._pressedKeys = [];
		this._viewOnly = !!(opts && opts.viewOnly);
	}

	on(event: string, fn: BetterViewerListener): void {
		if (!this._handlers[event]) this._handlers[event] = [];
		this._handlers[event].push(fn);
	}

	_emit(event: string, data?: unknown): void {
		const list = this._handlers[event] || [];
		for (const fn of list) {
			try {
				Reflect.apply(fn, undefined, [data]);
			} catch (e) {
				console.error(e);
			}
		}
	}

	attachWebSocket(ws: WebSocket): void {
		this._ws = ws;
		this._relayReady = false;
		this._protocolSent = false;
		this._accum = new Uint8Array(0);
		this._state = 0;

		ws.binaryType = 'arraybuffer';

		ws.addEventListener('message', (ev: MessageEvent<unknown>) => {
			if (typeof ev.data === 'string') {
				if (ev.data === 'c' || ev.data === 'cr') {
					this._onRelayReady();
				}
				return;
			}
			if (ev.data instanceof ArrayBuffer) this._onBinary(ev.data);
		});
	}

	_onRelayReady(): void {
		if (this._relayReady) return;
		this._relayReady = true;
		if (!this._protocolSent && this._ws && this._ws.readyState === 1) {
			this._ws.send('2');
			this._protocolSent = true;
		}
		this._state = 3;
		this._emit('state', 'streaming');
		this._emit('ready');
		this._emit('session_start');
		this._grabInput();
		this._startStats();
	}

	_startStats(): void {
		if (this._statsTimer) clearInterval(this._statsTimer);
		this._statsTimer = setInterval(() => {
			const now = Date.now();
			const elapsed = (now - this._lastStats) / 1000;
			if (elapsed > 0) {
				this._stats.fps = Math.round(this._stats.frames / elapsed);
				this._stats.bitrate = Math.round((this._stats.bytes * 8) / elapsed);
			}
			this._stats.frames = 0;
			this._stats.bytes = 0;
			this._lastStats = now;
			this._emit('stats', { fps: this._stats.fps, bitrate: this._stats.bitrate });
		}, 1000);
	}

	_sendRaw(bytes: Uint8Array<ArrayBuffer>): void {
		if (!this._ws || this._ws.readyState !== 1) return;
		this._ws.send(bytes);
	}

	_sendPacket(cmd: number, payload?: Uint8Array | null): void {
		const plen = payload ? payload.length : 0;
		const total = 4 + plen;
		const buf = new Uint8Array(total);
		buf[0] = 0;
		buf[1] = cmd & 0xff;
		buf[2] = (total >> 8) & 0xff;
		buf[3] = total & 0xff;
		if (payload) buf.set(payload, 4);
		this._sendRaw(buf);
	}

	_sendCompression(): void {
		const payload = new Uint8Array(6);
		payload[0] = this._imageType;
		payload[1] = this._compression;
		payload[2] = (this._scaling >> 8) & 0xff;
		payload[3] = this._scaling & 0xff;
		payload[4] = (this._frameTimer >> 8) & 0xff;
		payload[5] = this._frameTimer & 0xff;
		this._sendPacket(5, payload);
	}

	_sendUnpause(): void {
		const payload = new Uint8Array(1);
		payload[0] = 0;
		this._sendPacket(8, payload);
	}

	_sendRemoteInputLockQuery(): void {
		const payload = new Uint8Array(1);
		payload[0] = 2;
		this._sendPacket(87, payload);
	}

	_mergeAccum(chunk: ArrayBuffer): void {
		const incoming = new Uint8Array(chunk);
		const merged = new Uint8Array(this._accum.length + incoming.length);
		merged.set(this._accum);
		merged.set(incoming, this._accum.length);
		this._accum = merged;
	}

	_onBinary(chunk: ArrayBuffer): void {
		if (!this._relayReady) {
			this._onRelayReady();
		}
		this._stats.bytes += chunk.byteLength || 0;
		this._mergeAccum(chunk);
		this._processAccum();
	}

	/**
	 * Read whole packets from the buffer, walking it with an offset and
	 * keeping only the unread rest. A run of more than MAX_JUNK bytes that are
	 * no packet header ends the session: that is not a desktop stream.
	 */
	_processAccum(): void {
		const buf = this._accum;
		let off = 0;
		while (buf.length - off >= 4) {
			if (buf[off] === 0x11 && buf[off + 1] === 0xfe) {
				off += Math.min(buf.length - off, 13);
				continue;
			}
			const total = ((buf[off + 2] ?? 0) << 8) | (buf[off + 3] ?? 0);
			if (buf[off] !== 0 || total < 4) {
				off++;
				if (++this._junk > MAX_JUNK) {
					this._accum = new Uint8Array(0);
					this._junk = 0;
					this.disconnect('The device sent data that is not a desktop stream');
					return;
				}
				continue;
			}
			if (buf.length - off < total) break;
			this._junk = 0;
			const packet = buf.slice(off, off + total);
			off += total;
			this._handlePacket(packet);
			// A packet handler may have ended the session
			if (this._accum !== buf) return;
		}
		this._accum = off ? buf.slice(off) : buf;
	}

	_handlePacket(view: Uint8Array<ArrayBuffer>): void {
		if (view.length < 4) return;
		const cmd = view[1];

		if (cmd === CMD_TILE) {
			const x = ((view[4] ?? 0) << 8) | (view[5] ?? 0);
			const y = ((view[6] ?? 0) << 8) | (view[7] ?? 0);
			const jpeg = view.slice(8);
			this._drawTile(jpeg, x, y);
		} else if (cmd === CMD_SCREEN) {
			const w = ((view[4] ?? 0) << 8) | (view[5] ?? 0);
			const h = ((view[6] ?? 0) << 8) | (view[7] ?? 0);
			this._setScreenSize(w, h);
		} else if (cmd === CMD_GET_DISPLAYS) {
			/* optional multi-display UI */
		} else if (cmd === CMD_KEYSTATE && view.length >= 5) {
			this._emit('keyboardState', view[4]);
		} else if (cmd === CMD_INPUT_LOCK && view.length >= 5) {
			this._emit('inputLock', view[4] !== 0);
		}
	}

	_setScreenSize(w: number, h: number): void {
		if (w <= 0 || h <= 0) return;
		this._screenW = w;
		this._screenH = h;
		this.canvas.width = w;
		this.canvas.height = h;
		this._tilesPending = [];
		this._tilesDrawn = 0;
		this._tilesReceived = 0;
		this._killDraw = 0;
		this._sendCompression();
		this._sendUnpause();
		this._sendRemoteInputLockQuery();
		this._emit('videoFrame', { width: w, height: h });
	}

	_drawTile(jpegBytes: Uint8Array<ArrayBuffer>, x: number, y: number): void {
		const seq = ++this._tilesReceived;
		const blob = new Blob([jpegBytes], { type: 'image/jpeg' });
		createImageBitmap(blob)
			.then((img) => {
				this._tilesPending.push({ seq: seq, img: img, x: x, y: y });
				this._drainTiles();
			})
			.catch(() => {
				/* skip bad tile */
			});
	}

	_drainTiles(): void {
		while (this._tilesPending.length > 0) {
			const next = this._tilesPending[0];
			if (!next || next.seq !== this._tilesDrawn + 1) break;
			this._tilesPending.shift();
			// Without a 2D context the draw fails and the tiles after it wait
			if (!this.ctx) return;
			this.ctx.drawImage(next.img, next.x, next.y);
			this._tilesDrawn++;
			this._stats.frames++;
			this._emit('videoFrame', { width: this._screenW, height: this._screenH });
		}
	}

	sendCtrlAltDel(): void {
		this._sendPacket(INPUT_CTRLALTDEL, new Uint8Array(0));
	}

	_sendKeyKC(action: number, kc: number, extended: boolean): void {
		let up = action - 1;
		if (extended) up = action === KEY_DOWN ? 3 : 4;
		const payload = new Uint8Array(2);
		payload[0] = up;
		payload[1] = kc & 0xff;
		this._sendPacket(INPUT_KEY, payload);
	}

	_sendMouse(action: number, x: number, y: number, button: number, delta?: number | null): void {
		const payload = new Uint8Array(delta != null ? 8 : 6);
		payload[0] = 0;
		payload[1] = button;
		payload[2] = (x >> 8) & 0xff;
		payload[3] = x & 0xff;
		payload[4] = (y >> 8) & 0xff;
		payload[5] = y & 0xff;
		if (delta != null) {
			const d = delta < 0 ? 0x10000 + delta : delta;
			payload[6] = (d >> 8) & 0xff;
			payload[7] = d & 0xff;
			this._sendPacket(INPUT_MOUSE, payload);
		} else {
			this._sendPacket(INPUT_MOUSE, payload);
		}
	}

	_canvasCoords(ev: MouseEvent): { x: number; y: number } {
		const rect = this.canvas.getBoundingClientRect();
		const scaleX = this.canvas.width / rect.width;
		const scaleY = this.canvas.height / rect.height;
		const x = Math.floor((ev.clientX - rect.left) * scaleX);
		const y = Math.floor((ev.clientY - rect.top) * scaleY);
		return { x: x, y: y };
	}

	_grabInput(): void {
		if (this._inputGrabbed || this._viewOnly) return;
		this._inputGrabbed = true;

		this._listen(this.canvas, 'mousedown', (e) => {
			if (!this._sendsInput()) return;
			const c = this._canvasCoords(e);
			const btn = e.button === 2 ? MOUSE_RIGHT : e.button === 1 ? MOUSE_MIDDLE : MOUSE_LEFT;
			this._sendMouse(KEY_DOWN, c.x, c.y, btn);
			e.preventDefault();
		});
		this._listen(this.canvas, 'mouseup', (e) => {
			if (!this._sendsInput()) return;
			const c = this._canvasCoords(e);
			const btn = e.button === 2 ? MOUSE_RIGHT : e.button === 1 ? MOUSE_MIDDLE : MOUSE_LEFT;
			this._sendMouse(KEY_UP, c.x, c.y, btn * 2);
			e.preventDefault();
		});
		this._listen(this.canvas, 'mousemove', (e) => {
			if (!this._sendsInput()) return;
			const c = this._canvasCoords(e);
			this._sendMouse(0, c.x, c.y, 0);
			e.preventDefault();
		});
		this._listen(
			this.canvas,
			'wheel',
			(e) => {
				if (!this._sendsInput()) return;
				const c = this._canvasCoords(e);
				const delta = e.deltaY * -3;
				this._sendMouse(KEY_SCROLL, c.x, c.y, 0, delta);
				e.preventDefault();
			},
			{ passive: false }
		);

		this._listen(document, 'keydown', (e) => {
			if (!this._sendsInput()) return;
			this._sendKeyKC(KEY_DOWN, e.keyCode, false);
			e.preventDefault();
		});
		this._listen(document, 'keyup', (e) => {
			if (!this._sendsInput()) return;
			this._sendKeyKC(KEY_UP, e.keyCode, false);
			e.preventDefault();
		});
	}

	_listen<K extends keyof HTMLElementEventMap>(
		target: HTMLElement,
		type: K,
		fn: (e: HTMLElementEventMap[K]) => void,
		opts?: AddEventListenerOptions
	): void;
	_listen<K extends keyof DocumentEventMap>(
		target: Document,
		type: K,
		fn: (e: DocumentEventMap[K]) => void,
		opts?: AddEventListenerOptions
	): void;
	_listen(target: EventTarget, type: string, fn: EventListener, opts?: AddEventListenerOptions): void {
		target.addEventListener(type, fn, opts);
		this._inputListeners.push([target, type, fn, opts]);
	}

	/** Input goes out only while streaming, in the active tab, without view only */
	_sendsInput(): boolean {
		return this._state === 3 && this._active && !this._viewOnly;
	}

	_releaseInput(): void {
		for (const l of this._inputListeners) {
			l[0].removeEventListener(l[1], l[2], l[3]);
		}
		this._inputListeners = [];
		this._inputGrabbed = false;
	}

	setViewOnly(viewOnly: boolean): void {
		this._viewOnly = !!viewOnly;
	}

	/** The viewer's active tab: only that one takes keyboard and mouse input */
	setActive(active: boolean): void {
		this._active = !!active;
	}

	disconnect(reason?: string): void {
		this._releaseInput();
		if (this._statsTimer) {
			clearInterval(this._statsTimer);
			this._statsTimer = null;
		}
		if (this._ws) {
			try {
				this._ws.close();
			} catch {
				/* ignore */
			}
			this._ws = null;
		}
		this._state = 0;
		this._emit('state', 'disconnected');
		this._emit('disconnected', reason);
	}
}
