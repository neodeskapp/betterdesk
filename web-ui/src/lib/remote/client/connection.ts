/**
 * NeoDesk Web Remote Client - Connection Manager
 * Handles WebSocket connections to hbbs (rendezvous) and hbbr (relay)
 * via the Node.js WS proxy endpoints.
 */
import { page } from './page.ts';
import { ReasonError } from '../failures.ts';

/**
 * The bytes as WebSocket.send's type takes them: on an ArrayBuffer. Protobuf
 * and tweetnacl bytes always are; bytes on other memory go as a copy.
 */
function sendableBytes(data: Uint8Array): Uint8Array<ArrayBuffer> {
	return isOnArrayBuffer(data) ? data : new Uint8Array(data);
}

function isOnArrayBuffer(data: Uint8Array): data is Uint8Array<ArrayBuffer> {
	return data.buffer instanceof ArrayBuffer;
}

export interface RDConnectionOptions {
	/** Base URL of the NeoDesk server (auto-detected) */
	baseUrl?: string;
	deviceId?: string | number;
	guestSessionId?: string;
}

export type RDConnectionState = 'disconnected' | 'rendezvous' | 'relay' | 'connected' | 'error';

/** How the last socket closed */
export interface RDCloseInfo {
	kind: 'rendezvous' | 'relay';
	code: number;
	reason: string;
	at: number;
}

/** The events of a connection and their arguments */
export interface RDConnectionEvents {
	state: [state: RDConnectionState, prev: RDConnectionState];
	'rendezvous:open': [];
	'rendezvous:error': [event: Event];
	'rendezvous:close': [code: number, reason: string, info: RDCloseInfo];
	/** The socket's data: an ArrayBuffer for binary messages */
	'rendezvous:message': [data: unknown];
	'relay:open': [];
	'relay:error': [event: Event];
	'relay:close': [code: number, reason: string, info: RDCloseInfo];
	/** The socket's data: an ArrayBuffer for binary messages */
	'relay:message': [data: unknown];
	disconnected: [reason: string, info: RDCloseInfo];
}

export type RDConnectionListener<E extends keyof RDConnectionEvents> = (
	...args: RDConnectionEvents[E]
) => void;

type RDConnectionListeners = { [E in keyof RDConnectionEvents]?: RDConnectionListener<E>[] };

export class RDConnection {
	wsBase: string;
	deviceId: string;
	guestSessionId: string;
	rendezvousWs: WebSocket | null;
	relayWs: WebSocket | null;
	_state: RDConnectionState;
	_listeners: RDConnectionListeners;
	_lastClose: RDCloseInfo | null;
	/** Rejects of the connects still waiting for their socket to open */
	_pendingConnects: Set<(reason: Error) => void>;

	constructor(opts: RDConnectionOptions = {}) {
		const loc = window.location;
		const wsProtocol = loc.protocol === 'https:' ? 'wss:' : 'ws:';
		this.wsBase = opts.baseUrl || `${wsProtocol}//${loc.host}`;
		this.deviceId = String(opts.deviceId || '');
		this.guestSessionId = String(opts.guestSessionId || '');
		if (!this.guestSessionId && this.deviceId) {
			if (window.crypto && typeof window.crypto.randomUUID === 'function') {
				this.guestSessionId = window.crypto.randomUUID();
			} else if (window.crypto && typeof window.crypto.getRandomValues === 'function') {
				const bytes = new Uint8Array(16);
				window.crypto.getRandomValues(bytes);
				this.guestSessionId = Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('');
			} else {
				this.guestSessionId = `${Date.now().toString(36)}-${this.deviceId}`;
			}
		}

		this.rendezvousWs = null;
		this.relayWs = null;

		this._state = 'disconnected';
		this._listeners = {};
		this._lastClose = null;
		this._pendingConnects = new Set();
	}

	get state(): RDConnectionState {
		return this._state;
	}

	/**
	 * Guest Access Link token for WS auth (?guest=) — do not rely only on cookie.
	 */
	_guestQuerySuffix(): string {
		try {
			const q = new URLSearchParams(window.location.search);
			const token = q.get('guest') || q.get('t') || page().__guestToken || '';
			if (token) {
				const params = new URLSearchParams();
				params.set('guest', token);
				if (this.deviceId) params.set('peer_id', this.deviceId);
				if (this.guestSessionId) params.set('guest_session', this.guestSessionId);
				return `?${params.toString()}`;
			}
		} catch {
			/* ignore */
		}
		return '';
	}

	// ---- Event emitter ----

	on<E extends keyof RDConnectionEvents>(event: E, fn: RDConnectionListener<E>): this {
		if (!this._listeners[event]) this._listeners[event] = [];
		this._listeners[event].push(fn);
		return this;
	}

	off<E extends keyof RDConnectionEvents>(event: E, fn: RDConnectionListener<E>): this {
		// The same object, typed by this one event, so the filtered list can be written back
		const listeners: { [K in E]?: RDConnectionListener<K>[] } = this._listeners;
		const arr = listeners[event];
		if (arr) listeners[event] = arr.filter((f) => f !== fn);
		return this;
	}

	_emit<E extends keyof RDConnectionEvents>(event: E, ...args: RDConnectionEvents[E]): void {
		const arr = this._listeners[event];
		if (arr) arr.forEach((fn) => fn(...args));
	}

	_recordClose(kind: RDCloseInfo['kind'], event: CloseEvent | null | undefined): RDCloseInfo {
		const info: RDCloseInfo = {
			kind,
			code: event && Number.isFinite(event.code) ? event.code : 0,
			reason: event && event.reason ? String(event.reason) : '',
			at: Date.now()
		};
		this._lastClose = info;
		return info;
	}

	// ---- Rendezvous connection ----

	/**
	 * Connect to hbbs rendezvous server via WS proxy
	 */
	connectRendezvous(): Promise<WebSocket> {
		return new Promise((resolve, reject) => {
			this._setState('rendezvous');
			const url = `${this.wsBase}/ws/rendezvous${this._guestQuerySuffix()}`;

			const ws = new WebSocket(url);
			ws.binaryType = 'arraybuffer';
			// Kept from the start, so close() also stops a socket that is still connecting
			this.rendezvousWs = ws;
			const settle = this._trackConnect(reject);

			ws.onopen = () => {
				if (this.rendezvousWs !== ws) return;
				settle();
				this._lastClose = null;
				this._emit('rendezvous:open');
				resolve(ws);
			};

			ws.onerror = (e) => {
				settle();
				this._emit('rendezvous:error', e);
				reject(new ReasonError('server_unreachable', 'Rendezvous connection failed'));
			};

			ws.onclose = (e) => {
				if (this.rendezvousWs !== ws) return;
				this.rendezvousWs = null;
				const info = this._recordClose('rendezvous', e);
				this._emit('rendezvous:close', info.code, info.reason, info);
			};

			ws.onmessage = (e) => {
				this._emit('rendezvous:message', e.data);
			};
		});
	}

	/**
	 * Send binary data to rendezvous server
	 */
	sendRendezvous(data: Uint8Array): void {
		if (this.rendezvousWs && this.rendezvousWs.readyState === WebSocket.OPEN) {
			this.rendezvousWs.send(sendableBytes(data));
		}
	}

	/**
	 * Close rendezvous connection
	 */
	closeRendezvous(): void {
		if (this.rendezvousWs) {
			this.rendezvousWs.close();
			this.rendezvousWs = null;
		}
	}

	// ---- Relay connection ----

	/**
	 * Connect to hbbr relay server via WS proxy
	 */
	connectRelay(): Promise<WebSocket> {
		return new Promise((resolve, reject) => {
			this._setState('relay');
			const guestQuery = this._guestQuerySuffix();
			const url = `${this.wsBase}/ws/relay${guestQuery}${guestQuery ? '&' : '?'}transport=message`;

			const ws = new WebSocket(url);
			ws.binaryType = 'arraybuffer';
			this.relayWs = ws;
			const settle = this._trackConnect(reject);

			ws.onopen = () => {
				if (this.relayWs !== ws) return;
				settle();
				this._lastClose = null;
				this._emit('relay:open');
				resolve(ws);
			};

			ws.onerror = (e) => {
				settle();
				this._emit('relay:error', e);
				reject(new ReasonError('relay_unreachable', 'Relay connection failed'));
			};

			ws.onclose = (e) => {
				if (this.relayWs !== ws) return;
				this.relayWs = null;
				const info = this._recordClose('relay', e);
				this._emit('relay:close', info.code, info.reason, info);
				if (this._state === 'connected') {
					this._setState('disconnected');
					this._emit('disconnected', info.reason || 'Connection closed', info);
				}
			};

			ws.onmessage = (e) => {
				this._emit('relay:message', e.data);
			};
		});
	}

	/**
	 * Send binary data to relay server
	 */
	sendRelay(data: Uint8Array): void {
		if (this.relayWs && this.relayWs.readyState === WebSocket.OPEN) {
			this.relayWs.send(sendableBytes(data));
		}
	}

	/**
	 * Mark connection as established
	 */
	setConnected(): void {
		this._setState('connected');
	}

	/**
	 * Close all connections
	 */
	close(): void {
		this.closeRendezvous();
		if (this.relayWs) {
			this.relayWs.close();
			this.relayWs = null;
		}
		// Connects still waiting for their socket end here (Cancel, a closed tab)
		const pending = [...this._pendingConnects];
		this._pendingConnects.clear();
		for (const reject of pending) reject(new Error('Connection cancelled'));
		this._setState('disconnected');
	}

	/**
	 * Remember a connect's reject until its socket opens or fails
	 * @returns call when the connect settled
	 */
	_trackConnect(reject: (reason: Error) => void): () => boolean {
		this._pendingConnects.add(reject);
		return () => this._pendingConnects.delete(reject);
	}

	_setState(state: RDConnectionState): void {
		if (this._state !== state) {
			const prev = this._state;
			this._state = state;
			this._emit('state', state, prev);
		}
	}
}
