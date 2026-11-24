/**
 * NeoDesk Web Remote Client — MeshCentral transport adapter.
 * Uses NeoViewer (MNG_KVM) over meshrelay.
 */
import type { ViewerCapabilities } from '../access.ts';
import { NeoViewer, type NeoViewerOptions } from './neoviewer.ts';
import { MeshFileTransfer } from './mesh-files.ts';
import { page } from './page.ts';

export interface MeshSessionOptions extends NeoViewerOptions {
	deviceId?: string;
	/** Ask the server to record the session */
	serverRecord?: boolean;
}

/** An event listener; each event has its own data (see the _emit calls) */
export type MeshSessionListener = (...args: never[]) => void;

/** A field of a parsed value, as the JS read it (no throw on a value that is not an object) */
function field(value: unknown, key: string): unknown {
	return typeof value === 'object' && value !== null ? Reflect.get(value, key) : undefined;
}

export class MeshSession {
	canvas: HTMLCanvasElement;
	opts: MeshSessionOptions;
	_handlers: Record<string, MeshSessionListener[]>;
	_state: string;
	_ws: WebSocket | null;
	_desktop: NeoViewer | null;
	_deviceId: string;
	_active: boolean;
	/** disconnect() is closing the desktop viewer: its own close events are not passed on */
	_closing: boolean;
	/** Made by connect() */
	declare fileTransfer?: MeshFileTransfer;

	constructor(canvas: HTMLCanvasElement, opts: MeshSessionOptions) {
		this.canvas = canvas;
		this.opts = opts || {};
		this._handlers = {};
		this._state = 'idle';
		this._ws = null;
		this._desktop = null;
		this._deviceId = opts.deviceId || '';
		this._active = true;
		this._closing = false;
	}

	on(event: string, fn: MeshSessionListener): void {
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

	_bindDesktop(desktop: NeoViewer): void {
		desktop.on('state', (s: string) => {
			if (this._closing) return;
			this._state = s;
			this._emit('state', s);
		});
		desktop.on('ready', () => {
			this._emit('ready');
		});
		desktop.on('session_start', () => {
			this._emit('session_start');
			this._onStreaming();
		});
		desktop.on('disconnected', (reason: unknown) => {
			if (this._closing) return;
			this._emit('disconnected', reason);
		});
		desktop.on('videoFrame', (f: unknown) => {
			this._emit('videoFrame', f);
		});
		desktop.on('stats', (s: unknown) => {
			this._emit('stats', s);
		});
		desktop.on('inputLock', (locked: boolean) => {
			this._emit('cursorUpdate', { locked: locked });
		});
		desktop.on('keyboardState', (state: unknown) => {
			this._emit('keyboardState', state);
		});
	}

	async connect(): Promise<void> {
		this._state = 'connecting';
		this._emit('state', 'connecting');
		this._emit('log', 'Mesh: requesting desktop tunnel…');

		this.fileTransfer = new MeshFileTransfer(this._deviceId, (event, data) => {
			this._emit(event, data);
		});

		const relayBase = window.location.origin + '/';
		const tunnelQs = new URLSearchParams({ relay_base: relayBase });
		const pageQs = new URLSearchParams(window.location.search);
		const meshShare = pageQs.get('mesh_share');
		if (meshShare) {
			tunnelQs.set('mesh_share', meshShare);
		}
		const guest = pageQs.get('guest') || pageQs.get('t');
		if (guest) {
			tunnelQs.set('guest', guest);
		}
		const capsEarly: Partial<ViewerCapabilities> = page().__capabilities || {};
		if (capsEarly.guest_view_only || capsEarly.mesh_view_only) {
			tunnelQs.set('view_only', '1');
		}
		if (pageQs.get('record') === '1' || this.opts.serverRecord) {
			tunnelQs.set('record', '1');
		}
		const csrf = page().NeoDesk?.csrfToken || '';
		const headers: Record<string, string> = {
			'Content-Type': 'application/json',
			Accept: 'application/json'
		};
		if (csrf) headers['X-CSRF-Token'] = csrf;
		const resp = await fetch(
			`/api/mesh/devices/${encodeURIComponent(this._deviceId)}/desktop?${tunnelQs.toString()}`,
			{
				method: 'POST',
				credentials: 'same-origin',
				headers
			}
		);
		if (!resp.ok) {
			const err: unknown = await resp.json().catch(() => ({}));
			throw new Error(String(field(err, 'error') || 'Mesh tunnel request failed'));
		}
		const data: unknown = await resp.json();
		const url = field(data, 'browser_url') || field(field(data, 'data'), 'browser_url');
		if (!url || typeof url !== 'string') throw new Error('No mesh relay URL');

		const wsUrl = url.startsWith('http') ? url.replace(/^http/, 'ws') : window.location.origin + url;
		const ws = new WebSocket(wsUrl);
		this._ws = ws;
		ws.binaryType = 'arraybuffer';

		const desktop = new NeoViewer(this.canvas, this.opts);
		this._desktop = desktop;
		desktop.setActive(this._active);
		this._bindDesktop(desktop);
		desktop.attachWebSocket(ws);

		const caps: Partial<ViewerCapabilities> = page().__capabilities || {};
		if ((caps.mesh_view_only || caps.guest_view_only) && typeof desktop.setViewOnly === 'function') {
			desktop.setViewOnly(true);
		}

		ws.onerror = () => this._emit('error', { message: 'Mesh relay error' });
		ws.onclose = () => this.disconnect();
	}

	_onStreaming(): void {
		if (this.fileTransfer && typeof this.fileTransfer.enable === 'function') {
			this.fileTransfer.enable();
		}
	}

	/** End the session; it says so once, also when the desktop viewer or the socket's close said it first */
	disconnect(): void {
		const announced = this._state === 'disconnected';
		if (this.fileTransfer && typeof this.fileTransfer.disable === 'function') {
			this.fileTransfer.disable();
		}
		this._closing = true;
		try {
			if (this._desktop) {
				this._desktop.disconnect();
				this._desktop = null;
			}
		} finally {
			this._closing = false;
		}
		if (this._ws) {
			this._ws.onclose = null;
			try {
				this._ws.close();
			} catch {
				/* ignore */
			}
			this._ws = null;
		}
		if (announced) return;
		this._state = 'disconnected';
		this._emit('state', 'disconnected');
		this._emit('disconnected');
	}

	/** The viewer's active tab: keyboard and mouse go to this session only then */
	setSessionActive(active: boolean): void {
		this._active = !!active;
		if (this._desktop) this._desktop.setActive(this._active);
	}

	authenticate(): Promise<void> {
		return Promise.resolve();
	}
	verify2fa(): Promise<void> {
		return Promise.resolve();
	}
	setViewOnly(viewOnly: boolean): void {
		if (this._desktop && typeof this._desktop.setViewOnly === 'function') {
			this._desktop.setViewOnly(viewOnly);
		}
	}
}
