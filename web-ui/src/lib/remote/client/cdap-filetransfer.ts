/**
 * CDAP file transfer adapter — RDFileTransfer-compatible surface over the
 * existing /api/cdap/devices/:id/files WebSocket (same protocol as cdap-filebrowser.js).
 */

/** A directory entry as the file sheet lists it */
export interface CDAPFileEntry {
	name: string;
	size: number;
	/** The agent's modified time (RFC 3339 text), or 0 */
	modifiedTime: unknown;
	/** 0 for a directory, 4 for a file */
	entryType: number;
	isDir: boolean;
}

/** A transfer the sheet shows from file_transfer_start on */
export interface CDAPTransferStart {
	id: number;
	type: 'download' | 'upload';
	fileName: string;
	fileSize: number;
}

/** The events the transfer emits through its session, and their data */
export interface CDAPFileEvents {
	file_connecting: undefined;
	file_connect_error: { error: unknown };
	file_browsing: { path: string };
	file_dir: { path: string; entries: CDAPFileEntry[] };
	/** A write or delete went through */
	file_action: undefined;
	file_transfer_start: CDAPTransferStart;
	file_transfer_progress: { id: number; transferred: number; total: number };
	file_transfer_complete: { id: number; type: 'download' | 'upload'; fileName: string };
	/** id 0 for an error of no transfer */
	file_transfer_error: { id: number; error: unknown };
}

export type CDAPFileEmit = <E extends keyof CDAPFileEvents>(event: E, data?: CDAPFileEvents[E]) => void;

export interface CDAPFileTransferOptions {
	deviceId: string;
	/** (event, data) => void on the parent CDAPSession */
	emit: CDAPFileEmit;
}

/** A file of the remote list to download */
export interface CDAPRemoteFile {
	name: string;
	size?: unknown;
}

/** A message of the files socket as JSON.parse read it */
export interface CDAPFileMessage {
	type?: unknown;
	request_id?: unknown;
	error?: unknown;
	path?: unknown;
	entries?: unknown;
	data?: unknown;
}

/** An entry of a file_list_response */
interface CDAPFileListItem {
	name?: unknown;
	size?: unknown;
	modified?: unknown;
	modified_time?: unknown;
	is_dir?: unknown;
}

interface CDAPFileList {
	type: 'file_list';
	path: string;
}

interface CDAPFileRead {
	type: 'file_read';
	path: string;
	request_id: string;
}

interface CDAPFileWrite {
	type: 'file_write';
	path: string;
	/** The file's bytes as base64 */
	data: string;
}

/** What the client asks on the files socket */
export type CDAPFileRequest = CDAPFileList | CDAPFileRead | CDAPFileWrite;

/** The parsed value's fields; a value that is not an object has none */
function asRecord<T extends object>(value: unknown): Partial<T> {
	return typeof value === 'object' && value !== null ? (value as Partial<T>) : {};
}

/** A text field; a field of another type reads as absent */
function text(value: unknown): string {
	return typeof value === 'string' ? value : '';
}

/** err.message as the JS read it: undefined when the caught value has none */
function errorMessage(err: unknown): unknown {
	return typeof err === 'object' && err !== null ? Reflect.get(err, 'message') : undefined;
}

export class CDAPFileTransfer {
	_deviceId: string;
	_emit: CDAPFileEmit;
	_ws: WebSocket | null;
	_connected: boolean;
	_currentPath: string;
	_entries: CDAPFileEntry[];
	/** Download callbacks by request_id, until the file_read_response */
	_pending: Record<string, (msg: CDAPFileMessage) => void>;
	_nextId: number;
	_enabled: boolean;
	_showHidden: boolean;
	_connectPromise: Promise<void> | null;
	/** Set by the file sheet: saves a finished download instead of the browser's download */
	_saveDownload: ((fileName: string, blob: Blob) => void) | null;

	constructor(opts: CDAPFileTransferOptions) {
		this._deviceId = opts.deviceId;
		this._emit = opts.emit;
		this._ws = null;
		this._connected = false;
		this._currentPath = '/';
		this._entries = [];
		this._pending = {};
		this._nextId = 1;
		this._enabled = true;
		this._showHidden = false;
		this._connectPromise = null;
		this._saveDownload = null;
	}

	get currentPath(): string {
		return this._currentPath;
	}
	get enabled(): boolean {
		return this._enabled;
	}

	_needsFileConnection(): boolean {
		return !this._ws || this._ws.readyState !== WebSocket.OPEN;
	}

	ensureConnected(): Promise<void> {
		if (!this._needsFileConnection()) return Promise.resolve();
		if (this._connectPromise) return this._connectPromise;

		this._emit('file_connecting');
		this._connectPromise = new Promise<void>((resolve, reject) => {
			const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
			const url = `${proto}//${window.location.host}/api/cdap/devices/${encodeURIComponent(this._deviceId)}/files`;
			let ws: WebSocket;
			try {
				ws = new WebSocket(url, ['cdap-filebrowser']);
			} catch (err) {
				this._connectPromise = null;
				this._emit('file_connect_error', { error: errorMessage(err) || String(err) });
				reject(err);
				return;
			}
			this._ws = ws;
			const timer = setTimeout(() => {
				try {
					ws.close();
				} catch {
					/* ok */
				}
				this._connectPromise = null;
				this._emit('file_connect_error', { error: 'timeout' });
				reject(new Error('File transfer connection timeout'));
			}, 15000);

			ws.onopen = () => {
				clearTimeout(timer);
				this._connected = true;
				this._connectPromise = null;
				resolve();
			};
			ws.onmessage = (ev: MessageEvent<unknown>) => {
				try {
					this._handleMessage(asRecord<CDAPFileMessage>(JSON.parse(String(ev.data))));
				} catch {
					/* ignore */
				}
			};
			ws.onerror = () => {
				clearTimeout(timer);
				this._connectPromise = null;
				this._emit('file_connect_error', { error: 'websocket error' });
			};
			ws.onclose = () => {
				this._connected = false;
				this._ws = null;
				this._connectPromise = null;
			};
		});
		return this._connectPromise;
	}

	_send(payload: CDAPFileRequest): boolean {
		if (this._ws && this._ws.readyState === WebSocket.OPEN) {
			this._ws.send(JSON.stringify(payload));
			return true;
		}
		return false;
	}

	_handleMessage(msg: CDAPFileMessage): void {
		switch (msg.type) {
			case 'ready':
				break;
			case 'file_list_response':
				this._onList(msg);
				break;
			case 'file_read_response': {
				const requestId = msg.request_id;
				if (requestId && typeof requestId === 'string' && /^[\w-]{1,64}$/.test(requestId)) {
					const cb = Object.prototype.hasOwnProperty.call(this._pending, requestId)
						? this._pending[requestId]
						: null;
					if (typeof cb === 'function') {
						cb(msg);
						Reflect.deleteProperty(this._pending, requestId);
					}
				}
				break;
			}
			case 'file_write_response':
			case 'file_delete_response':
				if (msg.error) {
					this._emit('file_transfer_error', { id: 0, error: msg.error });
				} else {
					this._emit('file_action');
					this.browseDir(this._currentPath);
				}
				break;
			case 'error':
				this._emit('file_transfer_error', { id: 0, error: msg.error || 'Unknown error' });
				break;
			default:
				break;
		}
	}

	_onList(msg: CDAPFileMessage): void {
		const path = text(msg.path) || this._currentPath || '/';
		this._currentPath = path;
		const raw: unknown[] = Array.isArray(msg.entries) ? msg.entries : [];
		this._entries = raw
			.map((e) => asRecord<CDAPFileListItem>(e))
			.filter((e) => this._showHidden || !(text(e.name) || '').startsWith('.'))
			.map((e) => ({
				name: text(e.name),
				size: Number(e.size || 0),
				modifiedTime: e.modified || e.modified_time || 0,
				entryType: e.is_dir ? 0 : 4,
				isDir: !!e.is_dir
			}))
			.sort((a, b) => {
				if (a.isDir !== b.isDir) return a.isDir ? -1 : 1;
				return a.name.localeCompare(b.name, undefined, { sensitivity: 'base' });
			});
		this._emit('file_dir', { path: this._currentPath, entries: this._entries });
	}

	browseDir(path?: string | null): void {
		const dir = path == null || path === '' ? '/' : path;
		this._emit('file_browsing', { path: dir });
		this.ensureConnected()
			.then(() => {
				this._currentPath = dir;
				this._send({ type: 'file_list', path: dir });
			})
			.catch((err: unknown) => {
				this._emit('file_connect_error', { error: errorMessage(err) || String(err) });
			});
	}

	browseParent(): void {
		if (!this._currentPath || this._currentPath === '/') {
			this.browseDir('/');
			return;
		}
		const parts = this._currentPath.split('/').filter(Boolean);
		parts.pop();
		this.browseDir('/' + parts.join('/'));
	}

	setShowHidden(show: boolean): void {
		this._showHidden = !!show;
		this.browseDir(this._currentPath || '/');
	}

	downloadFile(remotePath: string, fileEntry: CDAPRemoteFile): number {
		const id = this._nextId++;
		const base = remotePath || this._currentPath || '/';
		const full = (base.replace(/\/$/, '') + '/' + ((fileEntry && fileEntry.name) || '')).replace(/\/+/g, '/');
		const requestId = 'dl_' + id + '_' + Date.now();

		this._emit('file_transfer_start', {
			id,
			type: 'download',
			fileName: fileEntry.name,
			fileSize: Number(fileEntry.size || 0)
		});

		this.ensureConnected()
			.then(() => {
				this._pending[requestId] = (msg) => {
					if (msg.error) {
						this._emit('file_transfer_error', { id, error: msg.error });
						return;
					}
					if (!msg.data) {
						this._emit('file_transfer_error', { id, error: 'empty file' });
						return;
					}
					try {
						const binary = atob(String(msg.data));
						const bytes = new Uint8Array(binary.length);
						for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
						const blob = new Blob([bytes]);
						if (typeof this._saveDownload === 'function') {
							// Same hook as RDFileTransfer: (fileName, blob)
							this._saveDownload(fileEntry.name, blob);
						} else {
							const url = URL.createObjectURL(blob);
							const a = document.createElement('a');
							a.href = url;
							a.download = fileEntry.name;
							a.click();
							URL.revokeObjectURL(url);
						}
						this._emit('file_transfer_progress', {
							id,
							transferred: bytes.length,
							total: bytes.length
						});
						this._emit('file_transfer_complete', { id, type: 'download', fileName: fileEntry.name });
					} catch (err) {
						this._emit('file_transfer_error', { id, error: errorMessage(err) || String(err) });
					}
				};
				this._send({ type: 'file_read', path: full, request_id: requestId });
			})
			.catch((err: unknown) => {
				this._emit('file_transfer_error', { id, error: errorMessage(err) || String(err) });
			});
		return id;
	}

	uploadFile(file: File, remotePath?: string): number {
		const id = this._nextId++;
		const base = remotePath || this._currentPath || '/';
		const full = (base.replace(/\/$/, '') + '/' + file.name).replace(/\/+/g, '/');

		this._emit('file_transfer_start', {
			id,
			type: 'upload',
			fileName: file.name,
			fileSize: file.size || 0
		});

		this.ensureConnected()
			.then(() => {
				const reader = new FileReader();
				reader.onload = () => {
					const base64 = String(reader.result || '').split(',')[1] || '';
					this._emit('file_transfer_progress', {
						id,
						transferred: file.size || 0,
						total: file.size || 0
					});
					const ok = this._send({ type: 'file_write', path: full, data: base64 });
					if (!ok) {
						this._emit('file_transfer_error', { id, error: 'not connected' });
						return;
					}
					this._emit('file_transfer_complete', { id, type: 'upload', fileName: file.name });
					setTimeout(() => this.browseDir(this._currentPath), 400);
				};
				reader.onerror = () => {
					this._emit('file_transfer_error', { id, error: 'read failed' });
				};
				reader.readAsDataURL(file);
			})
			.catch((err: unknown) => {
				this._emit('file_transfer_error', { id, error: errorMessage(err) || String(err) });
			});
		return id;
	}

	cancelTransfer(): boolean {
		return false;
	}

	close(): void {
		if (this._ws) {
			try {
				if (this._ws.readyState === WebSocket.OPEN) {
					this._ws.send(JSON.stringify({ type: 'close' }));
				}
				this._ws.close();
			} catch {
				/* ok */
			}
		}
		this._ws = null;
		this._connected = false;
		this._pending = {};
	}
}
