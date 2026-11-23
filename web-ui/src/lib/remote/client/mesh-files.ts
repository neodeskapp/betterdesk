/**
 * MeshCentral remote files protocol (relay p=5) for NeoDesk mesh transport.
 */
import { RDFileTransfer } from './filetransfer.ts';
import { page } from './page.ts';

const CTRL = '102938';
const PROTOCOL = '5';

/** A directory entry as the file sheet lists it */
export interface MeshFileEntry {
	name: string;
	size: number;
	/** Milliseconds */
	modifiedTime: number;
	isDir: boolean;
	/** 0 for a directory, 4 for a file */
	entry_type: number;
}

export interface MeshDownload {
	id: number;
	type: 'download';
	remotePath: string;
	fileName: string;
	fileSize: number;
	receivedBytes: number;
	chunks: Uint8Array<ArrayBuffer>[];
	/** 'transferring' once the download request went out */
	status: 'pending' | 'transferring';
	reqid: number;
}

export interface MeshUpload {
	id: number;
	type: 'upload';
	file: File;
	fileSize: number;
	/** Bytes read and sent so far */
	offset: number;
	reqid: number;
}

export type MeshTransfer = MeshDownload | MeshUpload;

/** The events the transfer emits through its session, and their data */
export interface MeshFileEvents {
	file_browsing: { path: string };
	file_browse_timeout: { path: string };
	file_dir: { path: string; entries: MeshFileEntry[] };
	error: { message: unknown };
	file_transfer_start: { id: number; type: 'download' | 'upload'; fileName: string; fileSize: number };
	/** The fields the file sheet reads */
	file_transfer_progress: { id: number; transferred: number; fileSize: number; percent: number };
	file_transfer_complete: { id: number; type: 'download' | 'upload' };
	file_transfer_error: { id: number; error: unknown };
	file_transfer_cancelled: { id: number };
}

export type MeshFileEmit = <E extends keyof MeshFileEvents>(event: E, data: MeshFileEvents[E]) => void;

/** A file of the remote list to download */
export interface MeshRemoteFile {
	name: string;
	size?: unknown;
}

/** A message of the files relay as JSON.parse read it */
interface MeshFileMessage {
	ctrlChannel?: unknown;
	action?: unknown;
	dir?: unknown;
	path?: unknown;
	reqid?: unknown;
}

/** An entry of an ls answer: name, size, modified time in seconds */
interface MeshDirItem {
	n?: unknown;
	s?: unknown;
	dt?: unknown;
}

interface MeshLs {
	action: 'ls';
	path: string;
	reqid: number;
}

interface MeshMkdir {
	action: 'mkdir';
	path: string;
}

interface MeshRm {
	action: 'rm';
	path: string;
	delfiles: (string | undefined)[];
	rec?: number;
}

interface MeshDownloadRequest {
	action: 'download';
	path: string;
	reqid: number;
}

interface MeshUploadRequest {
	action: 'upload';
	path: string;
	reqid: number;
}

interface MeshUploadDone {
	action: 'uploaddone';
	reqid: number;
}

/** What the client sends on the files relay as JSON */
export type MeshFileRequest =
	MeshLs | MeshMkdir | MeshRm | MeshDownloadRequest | MeshUploadRequest | MeshUploadDone;

/** The parsed value's fields; a value that is not an object has none */
function asRecord<T extends object>(value: unknown): Partial<T> {
	return typeof value === 'object' && value !== null ? (value as Partial<T>) : {};
}

/** A field of a parsed value, as the JS read it (no throw on a value that is not an object) */
function field(value: unknown, key: string): unknown {
	return typeof value === 'object' && value !== null ? Reflect.get(value, key) : undefined;
}

/** err.message as the JS read it: undefined when the caught value has none */
function errorMessage(err: unknown): unknown {
	return field(err, 'message');
}

export class MeshFileTransfer {
	_deviceId: string;
	_emit: MeshFileEmit;
	_ws: WebSocket | null;
	_enabled: boolean;
	_currentPath: string;
	_showHidden: boolean;
	_nextId: number;
	_nextReqId: number;
	_transfers: Map<number, MeshTransfer>;
	_browseTimeout: ReturnType<typeof setTimeout> | null;
	/** The ls answer came, or the browse timed out */
	_browseTimedOut: boolean;
	_connecting: Promise<void> | null;
	_relayReady: boolean;
	_protocolSent: boolean;
	/**
	 * Set by the file sheet: saves a finished download into the chosen local
	 * folder; false (or a promise of false) leaves it to the browser
	 */
	_saveDownload: ((fileName: string, blob: Blob) => unknown) | null;

	constructor(deviceId: string, emit?: MeshFileEmit | null) {
		this._deviceId = deviceId;
		this._emit = emit || (() => {});
		this._ws = null;
		this._enabled = false;
		this._currentPath = '';
		this._showHidden = false;
		this._nextId = 1;
		this._nextReqId = 1;
		this._transfers = new Map();
		this._browseTimeout = null;
		this._browseTimedOut = false;
		this._connecting = null;
		this._relayReady = false;
		this._protocolSent = false;
		this._saveDownload = null;
	}

	enable(): void {
		this._enabled = true;
	}

	disable(): void {
		this._enabled = false;
		this.cancelAll();
		if (this._ws) {
			try {
				this._ws.close();
			} catch {
				/* ignore */
			}
			this._ws = null;
		}
		this._relayReady = false;
		this._protocolSent = false;
	}

	get enabled(): boolean {
		return this._enabled;
	}

	get currentPath(): string {
		return this._currentPath;
	}

	_ensureConnected(): Promise<void> {
		if (this._ws && this._ws.readyState === WebSocket.OPEN && this._relayReady) {
			return Promise.resolve();
		}
		if (this._connecting) return this._connecting;

		this._connecting = (async () => {
			const relayBase = window.location.origin + '/';
			const headers: Record<string, string> = {
				'Content-Type': 'application/json',
				Accept: 'application/json'
			};
			const csrf = page().NeoDesk?.csrfToken || '';
			if (csrf) headers['X-CSRF-Token'] = csrf;
			const resp = await fetch(
				'/api/mesh/devices/' +
					encodeURIComponent(this._deviceId) +
					'/files?relay_base=' +
					encodeURIComponent(relayBase),
				{
					method: 'POST',
					credentials: 'same-origin',
					headers: headers
				}
			);
			if (!resp.ok) {
				const err: unknown = await resp.json().catch(() => ({}));
				throw new Error(String(field(err, 'error') || 'Mesh files tunnel failed'));
			}
			const data: unknown = await resp.json();
			const url = field(data, 'browser_url') || field(field(data, 'data'), 'browser_url');
			if (!url || typeof url !== 'string') throw new Error('No mesh relay URL');
			const wsUrl = url.indexOf('http') === 0 ? url.replace(/^http/, 'ws') : window.location.origin + url;

			return new Promise<void>((resolve, reject) => {
				const ws = new WebSocket(wsUrl);
				ws.binaryType = 'arraybuffer';
				this._ws = ws;
				this._relayReady = false;
				this._protocolSent = false;

				ws.onopen = () => {
					/* wait for c */
				};
				ws.onerror = () => {
					reject(new Error('Mesh files relay error'));
				};
				ws.onclose = () => {
					this._relayReady = false;
					this._ws = null;
				};
				ws.onmessage = (ev: MessageEvent<unknown>) => {
					if (typeof ev.data === 'string') {
						if (ev.data === 'c' || ev.data === 'cr') {
							if (!this._relayReady) {
								this._relayReady = true;
								ws.send(PROTOCOL);
								this._protocolSent = true;
								resolve();
							}
						}
						return;
					}
					if (ev.data instanceof ArrayBuffer || ArrayBuffer.isView(ev.data)) this._onBinary(ev.data);
				};

				setTimeout(() => {
					if (!this._relayReady) {
						reject(new Error('Mesh files relay timeout'));
						try {
							ws.close();
						} catch {
							/* ignore */
						}
					}
				}, 30000);
			});
		})().finally(() => {
			this._connecting = null;
		});

		return this._connecting;
	}

	_sendJson(obj: MeshFileRequest): void {
		if (!this._ws || this._ws.readyState !== WebSocket.OPEN) return;
		this._ws.send(JSON.stringify(obj));
	}

	_onBinary(data: ArrayBuffer | ArrayBufferView): void {
		const buf = data instanceof ArrayBuffer ? new Uint8Array(data) : new Uint8Array(data.buffer || data);
		if (buf.length === 0) return;

		if (buf[0] === 123) {
			try {
				const text = new TextDecoder().decode(buf);
				this._onJson(text);
			} catch {
				/* ignore */
			}
			return;
		}

		for (const id of this._transfers.keys()) {
			const t = this._transfers.get(id);
			if (t && t.type === 'download' && t.status === 'transferring') {
				const off = buf[0] === 0 ? 1 : 0;
				t.receivedBytes += buf.length - off;
				t.chunks.push(buf.slice(off));
				this._emit('file_transfer_progress', {
					id: id,
					transferred: t.receivedBytes,
					fileSize: t.fileSize,
					percent: t.fileSize > 0 ? Math.min(100, Math.round((t.receivedBytes / t.fileSize) * 100)) : 0
				});
				if (t.fileSize > 0 && t.receivedBytes >= t.fileSize) {
					this._finishDownload(id);
				}
				return;
			}
		}
	}

	_onJson(text: string): void {
		let parsed: unknown;
		try {
			parsed = JSON.parse(text);
		} catch {
			return;
		}
		const msg = asRecord<MeshFileMessage>(parsed);

		if (msg.ctrlChannel === CTRL) return;

		if (msg.action === 'ls' && msg.dir) {
			this._browseTimedOut = true;
			if (this._browseTimeout) clearTimeout(this._browseTimeout);
			this._currentPath = typeof msg.path === 'string' ? msg.path : '';
			const dir: unknown[] = Array.isArray(msg.dir) ? msg.dir : [];
			const entries = dir.map((item): MeshFileEntry => {
				const e = asRecord<MeshDirItem>(item);
				const name = typeof e.n === 'string' ? e.n : '';
				const isDir = name.indexOf('.') === -1 && (e.s === 0 || e.s === undefined);
				return {
					name: name,
					size: typeof e.s === 'number' ? e.s : 0,
					modifiedTime: Number(e.dt || 0) * 1000,
					isDir: isDir,
					entry_type: isDir ? 0 : 4
				};
			});
			this._emit('file_dir', { path: this._currentPath, entries: entries });
			return;
		}

		if (msg.action === 'uploadack' && msg.reqid) {
			let upload: MeshUpload | null = null;
			for (const id of this._transfers.keys()) {
				const t = this._transfers.get(id);
				if (t && t.type === 'upload' && t.reqid === msg.reqid) {
					upload = t;
					break;
				}
			}
			// The agent took the last chunk: send the next one, or say the file is done
			if (upload) {
				if (upload.offset < upload.fileSize) this._readUploadChunk(upload);
				else this._finishUpload(upload);
			}
			return;
		}

		if (msg.action === 'uploaderror' || msg.action === 'downloaderror') {
			// The agent names the request; the list knows our own transfer ids only
			for (const tid of this._transfers.keys()) {
				const failed = this._transfers.get(tid);
				if (failed && failed.reqid === msg.reqid) {
					this._transfers.delete(tid);
					this._emit('file_transfer_error', { id: tid, error: msg.action });
					return;
				}
			}
		}
	}

	browseDir(path?: string | null): void {
		if (!this._enabled) return;
		const dir = path != null ? path : '';
		this._emit('file_browsing', { path: dir });
		this._browseTimedOut = false;
		if (this._browseTimeout) clearTimeout(this._browseTimeout);
		this._browseTimeout = setTimeout(() => {
			if (!this._browseTimedOut) {
				this._browseTimedOut = true;
				this._emit('file_browse_timeout', { path: dir });
			}
		}, 8000);

		this._ensureConnected()
			.then(() => {
				const reqid = this._nextReqId++;
				this._sendJson({ action: 'ls', path: dir, reqid: reqid });
			})
			.catch((err: unknown) => {
				this._emit('error', { message: errorMessage(err) });
			});
	}

	browseParent(): void {
		if (!this._currentPath) return;
		const parent = RDFileTransfer.parentRemotePath(this._currentPath);
		this.browseDir(parent);
	}

	createDir(path: string): void {
		this._ensureConnected()
			.then(() => {
				this._sendJson({ action: 'mkdir', path: path });
			})
			.catch(() => {});
	}

	removeFile(path: string): void {
		const parts = path.replace(/\\/g, '/').split('/');
		const name = parts.pop();
		const dir = parts.join('/') || '/';
		this._ensureConnected()
			.then(() => {
				this._sendJson({ action: 'rm', path: dir, delfiles: [name] });
			})
			.catch(() => {});
	}

	removeDir(path: string, recursive?: boolean): void {
		const parts = path.replace(/\\/g, '/').split('/');
		const name = parts.pop();
		const dir = parts.join('/') || '/';
		this._ensureConnected()
			.then(() => {
				this._sendJson({ action: 'rm', path: dir, delfiles: [name], rec: recursive ? 1 : 0 });
			})
			.catch(() => {});
	}

	downloadFile(remotePath: string, fileEntry: MeshRemoteFile): number {
		if (!this._enabled) return -1;
		const id = this._nextId++;
		const transfer: MeshDownload = {
			id: id,
			type: 'download',
			remotePath: remotePath,
			fileName: fileEntry.name,
			fileSize: Number(fileEntry.size || 0),
			receivedBytes: 0,
			chunks: [],
			status: 'pending',
			reqid: this._nextReqId++
		};
		this._transfers.set(id, transfer);
		const sep = remotePath.indexOf('\\') >= 0 ? '\\' : '/';
		const fullPath = remotePath + (remotePath.endsWith(sep) ? '' : sep) + fileEntry.name;

		this._emit('file_transfer_start', {
			id: id,
			type: 'download',
			fileName: fileEntry.name,
			fileSize: transfer.fileSize
		});

		this._ensureConnected()
			.then(() => {
				transfer.status = 'transferring';
				this._sendJson({ action: 'download', path: fullPath, reqid: transfer.reqid });
			})
			.catch((err: unknown) => {
				this._emit('file_transfer_error', { id: id, error: errorMessage(err) });
				this._transfers.delete(id);
			});

		return id;
	}

	_finishDownload(id: number): void {
		const t = this._transfers.get(id);
		if (!t || t.type !== 'download') return;
		this._transfers.delete(id);
		const blob = new Blob(t.chunks);
		const browserDownload = (): void => {
			const url = URL.createObjectURL(blob);
			const a = document.createElement('a');
			a.href = url;
			a.download = t.fileName;
			a.click();
			URL.revokeObjectURL(url);
		};
		// Into the folder chosen in the file sheet when its hook takes the file, else the browser's download
		const save = this._saveDownload;
		if (typeof save !== 'function') {
			browserDownload();
			this._emit('file_transfer_complete', { id: id, type: 'download' });
			return;
		}
		Promise.resolve()
			.then(() => save(t.fileName, blob))
			.then((savedToFolder) => {
				if (!savedToFolder) browserDownload();
				this._emit('file_transfer_complete', { id: id, type: 'download' });
			})
			.catch((err: unknown) => {
				this._emit('file_transfer_error', {
					id: id,
					error: 'Failed to save file: ' + (errorMessage(err) || 'unknown error')
				});
			});
	}

	uploadFile(file: File, remotePath: string): number {
		if (!this._enabled) return -1;
		const id = this._nextId++;
		const sep = remotePath.indexOf('\\') >= 0 ? '\\' : '/';
		const fullPath = remotePath + (remotePath.endsWith(sep) ? '' : sep) + file.name;
		const transfer: MeshUpload = {
			id: id,
			type: 'upload',
			file: file,
			fileSize: file.size,
			offset: 0,
			reqid: this._nextReqId++
		};
		this._transfers.set(id, transfer);

		this._emit('file_transfer_start', {
			id: id,
			type: 'upload',
			fileName: file.name,
			fileSize: file.size
		});

		this._ensureConnected()
			.then(() => {
				this._sendJson({ action: 'upload', path: fullPath, reqid: transfer.reqid });
				if (transfer.fileSize > 0) this._readUploadChunk(transfer);
				else this._finishUpload(transfer);
			})
			.catch((err: unknown) => {
				this._emit('file_transfer_error', { id: id, error: errorMessage(err) });
				this._transfers.delete(id);
			});

		return id;
	}

	/** Read the next 64 KB of the file and send it; the agent's uploadack asks for the one after */
	_readUploadChunk(transfer: MeshUpload): void {
		const chunkSize = 65536;
		const start = transfer.offset;
		const end = Math.min(start + chunkSize, transfer.fileSize);
		const slice = transfer.file.slice(start, end);
		slice
			.arrayBuffer()
			.then((buf) => {
				if (!this._transfers.has(transfer.id)) return;
				transfer.offset = end;
				this._sendUploadChunk(new Uint8Array(buf));
			})
			.catch((err: unknown) => {
				this._emit('file_transfer_error', { id: transfer.id, error: errorMessage(err) });
				this._transfers.delete(transfer.id);
			});
	}

	_finishUpload(transfer: MeshUpload): void {
		this._sendJson({ action: 'uploaddone', reqid: transfer.reqid });
		this._emit('file_transfer_complete', { id: transfer.id, type: 'upload' });
		this._transfers.delete(transfer.id);
	}

	_sendUploadChunk(chunk: Uint8Array): void {
		if (!this._ws || this._ws.readyState !== WebSocket.OPEN) return;
		const out = new Uint8Array(chunk.length + 1);
		out[0] = 0;
		out.set(chunk, 1);
		this._ws.send(out);
	}

	cancelTransfer(id: number): boolean {
		if (this._transfers.has(id)) {
			this._transfers.delete(id);
			this._emit('file_transfer_cancelled', { id: id });
			return true;
		}
		return false;
	}

	cancelAll(): void {
		this._transfers.clear();
	}
}
