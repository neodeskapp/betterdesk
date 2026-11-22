/**
 * BetterDesk Web Remote Client - File Transfer Module
 * Handles RustDesk file transfer protocol: browse, download, upload, manage
 *
 * Protocol flow (RustDesk client io_loop — operator pulls from / pushes to peer):
 *   Browse:   FileAction.read_dir → FileResponse.dir
 *   Download: FileAction.send (full remote file path) → FileResponse.digest → FileAction.send_confirm → FileResponse.block* → FileResponse.done
 *   Upload:   FileAction.receive → FileResponse.digest (operator) → FileAction.send_confirm (peer) → FileResponse.block* → FileResponse.done
 *             (overwrite: peer may reply with FileResponse.digest is_upload=true → FileAction.send_confirm → blocks)
 *   Cancel:   FileAction.cancel
 */
import { RDCompress } from './compress.ts';
import { LocalFiles } from './local-files.ts';
import { debugLog } from './log.ts';
import { page, tauriInvoke } from './page.ts';
import type { RDProtocol } from './protocol.ts';
import { buildZip, type ZipEntry } from './zip.ts';
import type {
	FileDirectory,
	FileResponse,
	FileTransferBlock,
	FileTransferDigest,
	FileTransferDone,
	FileTransferError,
	FileTransferSendConfirmRequest,
	MessageInit
} from './generated/protos.ts';

export interface RDFileTransferOptions {
	/** Protocol handler */
	proto: RDProtocol;
	/** Sends a peer message */
	sendMessage: (msg: MessageInit) => void;
	/** Event emitter */
	emit: (event: string, ...args: unknown[]) => void;
	/** Async hook before browse/upload */
	ensureConnected?: (() => Promise<unknown>) | null;
	/** Returns true when file relay is ready */
	isConnected?: (() => boolean) | null;
}

export type RDTransferType = 'download' | 'upload';

export type RDTransferStatus = 'pending' | 'transferring' | 'complete' | 'error' | 'cancelled';

export type RDFolderJobStatus = 'queued' | 'transferring' | 'complete' | 'error' | 'cancelled';

/** A remote directory entry as the file sheet lists it */
export interface RDRemoteFileEntry {
	name: string;
	entryType: number;
	isHidden: boolean;
	size: number;
	modifiedTime: number;
	isDir: boolean;
}

/** The remote file downloadFile fetches (a listed entry or name and size) */
export interface RDRemoteFileRef {
	name: string;
	size?: number | null;
	modifiedTime?: number | null;
}

/** What an upload reads: a browser File or the desktop's native file (LocalFiles.createNativeUploadFile) */
export interface RDUploadSource {
	name: string;
	size: number;
	lastModified: number;
	/** The desktop's read handle; set on native files only */
	__rdNativeHandle?: unknown;
	slice(start: number, end: number): { arrayBuffer(): Promise<ArrayBuffer> };
}

export interface RDDownloadOptions {
	/** Desktop: write into this dir (no Save dialog) */
	localDir?: string | null;
	/** Override save name / relative file name */
	localFileName?: string | null;
	/** Desktop: exact output path */
	absolutePath?: string | null;
	folderJobId?: number | null;
	/** Skip top-level start event (folder child) */
	silent?: boolean;
	/** Keep the blocks in memory even on the desktop */
	forceBuffer?: boolean;
	/** Hand the finished file to captureResolve as a Blob */
	captureBlob?: boolean;
	captureResolve?: ((blob: Blob) => void) | null;
	captureReject?: ((err: unknown) => void) | null;
}

export interface RDUploadOptions {
	/** Relative name under remotePath (may include `/` or `\`) */
	remoteFileName?: string | null;
	folderJobId?: number | null;
	silent?: boolean;
}

/** A local folder tree to upload ({ rootName, dirs[], files[] } from LocalFiles.walk*) */
export interface RDFolderWalk {
	rootName?: string | null;
	dirs?: RDFolderWalkDir[] | null;
	files?: RDFolderWalkFile[] | null;
}

export interface RDFolderWalkDir {
	name: string;
	relativePath?: string | null;
}

export interface RDFolderWalkFile {
	name: string;
	relativePath?: string | null;
	size?: number | null;
	/** The browser file (folder pick or drop) */
	file?: RDUploadSource | null;
	/** The desktop's absolute path (opened with desktop_open_file) */
	path?: string | null;
}

/** The remote folder downloadFolder fetches */
export interface RDFolderEntry {
	name: string;
	isDir?: boolean;
}

/** A directory of a remote folder tree */
export interface RDRemoteTreeDir {
	name: string;
	path: string;
	relativePath: string;
}

/** A file of a remote folder tree */
export interface RDRemoteTreeFile {
	name: string;
	path: string;
	relativePath: string;
	size: number;
	modifiedTime: number;
	entry: RDRemoteFileEntry;
	parentPath: string;
}

interface RDTransferBase {
	id: number;
	remotePath: string;
	fileName: string;
	fileSize: number;
	folderJobId: number | null;
	startTime: number;
	status: RDTransferStatus;
	fileNum: number;
	stallTimer: ReturnType<typeof setTimeout> | null;
	resumable?: boolean;
	errorMessage?: string | null;
	/** The folder job waiting for this child: true when it finished */
	_folderWait?: ((ok: boolean) => void) | null;
}

export interface RDDownloadTransfer extends RDTransferBase {
	type: 'download';
	remoteFileName: string;
	receivedBytes: number;
	/** The received blocks; null while they stream to the desktop file */
	blocks: Uint8Array[] | null;
	streamDownload: boolean;
	captureBlob: boolean;
	captureResolve: ((blob: Blob) => void) | null;
	captureReject: ((err: unknown) => void) | null;
	/** The desktop's write handle (desktop_download_begin) */
	downloadHandle: unknown;
	localPath: string | null;
	localDir: string | null;
	absolutePath: string | null;
	writeChain: Promise<unknown>;
	/** The blocks and the done of the device, applied one after the other in the order they came */
	receiveChain: Promise<void>;
	_writeBatch?: Uint8Array[];
	_writeBatchLen?: number;
	_lastProgressEmit?: number;
}

export interface RDUploadTransfer extends RDTransferBase {
	type: 'upload';
	sentBytes: number;
	file: RDUploadSource;
	currentBlk: number;
	skipCompress?: boolean;
	_compressHits?: number;
	_compressMisses?: number;
}

export type RDTransfer = RDDownloadTransfer | RDUploadTransfer;

interface RDFolderJobBase {
	id: number;
	isFolder: true;
	fileName: string;
	fileSize: number;
	transferred: number;
	status: RDFolderJobStatus;
	currentFile: string | null;
	childId: number | null;
	fileIndex: number;
	startTime?: number;
}

export interface RDFolderUploadJob extends RDFolderJobBase {
	type: 'upload';
	remoteDest: string;
	rootName: string;
	dirs: RDFolderWalkDir[];
	files: RDFolderWalkFile[];
}

export interface RDFolderDownloadJob extends RDFolderJobBase {
	type: 'download';
	remotePath: string;
	folderEntry: RDFolderEntry;
	localRoot: string;
	pendingFiles: RDRemoteTreeFile[];
	/** Browser: fetch every file into memory and save the folder as one ZIP archive */
	asZip?: boolean;
}

export type RDFolderJob = RDFolderUploadJob | RDFolderDownloadJob;

/** file_transfer_start */
export interface RDFileTransferStartEvent {
	id: number;
	type: RDTransferType;
	fileName: string;
	fileSize: number;
	folderJobId?: number | null;
	isFolder?: boolean;
	percent?: number;
}

/** file_transfer_progress */
export interface RDFileTransferProgressEvent {
	id: number;
	fileName: string;
	fileSize: number;
	transferred: number;
	percent: number;
	type: RDTransferType;
	isFolder?: boolean;
	currentFile?: string | null;
	phase?: 'transferring' | 'saving';
}

/** file_transfer_complete */
export interface RDFileTransferCompleteEvent {
	id: number;
	fileName: string;
	fileSize: number;
	type: RDTransferType;
	elapsed: number;
	isFolder?: boolean;
	skipped?: boolean;
}

/** file_transfer_error */
export interface RDFileTransferErrorEvent {
	id: number;
	fileName: string;
	error: string;
	resumable?: boolean;
	type?: RDTransferType | null;
	folderJobId?: number | null;
	currentFile?: string | null;
	isFolder?: boolean;
}

/** file_transfer_cancelled */
export interface RDFileTransferCancelledEvent {
	id: number;
	fileName: string;
	isFolder?: boolean;
	folderJobId?: number | null;
}

/** file_transfer_overwrite_prompt: answer with confirmOverwrite(id, skip, applyToAll) */
export interface RDFileOverwritePromptEvent {
	id: number;
	fileName: string;
	fileSize: number;
	remotePath: string;
}

/** file_dir */
export interface RDFileDirEvent {
	path: string;
	entries: RDRemoteFileEntry[];
}

/** file_browsing and file_browse_timeout */
export interface RDFileBrowseEvent {
	path: string;
}

/** file_connect_error */
export interface RDFileConnectErrorEvent {
	error: string;
}

/** file_action */
export interface RDFileActionEvent {
	action: 'create_dir' | 'remove_file' | 'remove_dir' | 'rename';
	path: string;
	newName?: string;
}

/** One active transfer in getStats() */
export interface RDFileTransferStat {
	id: number;
	type: RDTransferType;
	fileName: string;
	fileSize: number;
	transferred: number;
	percent: number;
	speed: number;
	status: RDTransferStatus;
}

export interface RDFileTransferStats {
	active: RDFileTransferStat[];
	count: number;
}

interface RDPendingOverwrite {
	resolve: (skip: boolean) => void;
}

interface RDPendingDirRead {
	resolve: (entries: RDRemoteFileEntry[]) => void;
	reject: (err: unknown) => void;
	timer: ReturnType<typeof setTimeout>;
}

interface RDPendingCreate {
	resolve: () => void;
	reject: (err: unknown) => void;
	timer: ReturnType<typeof setTimeout>;
	path: string;
}

/** err.message as text, undefined when the caught value has none (the JS read err.message) */
function errorMessage(err: unknown): string | undefined {
	const message: unknown = typeof err === 'object' && err !== null ? Reflect.get(err, 'message') : undefined;
	return message ? String(message) : undefined;
}

/** A field of a desktop command's answer; undefined when the answer is no object */
function answerField(answer: unknown, key: string): unknown {
	return typeof answer === 'object' && answer !== null ? Reflect.get(answer, key) : undefined;
}

/**
 * The desktop's command bridge where the JS called window.__TAURI__.core.invoke
 * without a check: without the bridge the call throws, as the JS did.
 */
function desktopInvoke(cmd: string, args: Record<string, unknown>): Promise<unknown> {
	const invoke = tauriInvoke();
	if (!invoke) throw new TypeError('Desktop bridge unavailable');
	return invoke(cmd, args);
}

/** Whether a folder job was cancelled (read again after each await) */
function isCancelled(job: { status: string }): boolean {
	return job.status === 'cancelled';
}

function isOnArrayBuffer(data: Uint8Array): data is Uint8Array<ArrayBuffer> {
	return data.buffer instanceof ArrayBuffer;
}

/**
 * The blocks as Blob takes them: on an ArrayBuffer. Decoded and decompressed
 * bytes always are; bytes on other memory go as a copy.
 */
function blobParts(parts: Uint8Array[]): Uint8Array<ArrayBuffer>[] {
	return parts.map((part) => (isOnArrayBuffer(part) ? part : new Uint8Array(part)));
}

export class RDFileTransfer {
	_proto: RDProtocol;
	_sendMessage: (msg: MessageInit) => void;
	_emit: (event: string, ...args: unknown[]) => void;
	_ensureConnected: (() => Promise<unknown>) | null;
	_isConnected: (() => boolean) | null;
	/** Current remote directory path */
	_currentPath: string;
	/** Current directory entries */
	_entries: RDRemoteFileEntry[];
	/** Active transfers by ID */
	_transfers: Map<number, RDTransfer>;
	/** Transfer ID counter */
	_nextId: number;
	/** Whether file transfer is enabled */
	_enabled: boolean;
	/** Show hidden files */
	_showHidden: boolean;
	FILE_TYPE: { DIR: number; DIR_LINK: number; DIR_DRIVE: number; FILE: number; FILE_LINK: number };
	BLOCK_SIZE: number;
	TRANSFER_STALL_MS: number;
	/** Batch streamed download IPC writes (bytes) — fewer Tauri round-trips */
	DOWNLOAD_WRITE_BATCH: number;
	/** Emit UI progress at most this often during block streaming */
	PROGRESS_EMIT_MS: number;
	/** Session-wide overwrite strategy */
	_overwriteStrategy: 'overwrite' | 'skip' | null;
	/** Pending overwrite prompts */
	_pendingOverwrite: Map<number, RDPendingOverwrite>;
	/** Pending background read_dir promises (folder walk) */
	_pendingDirReads: Map<string, RDPendingDirRead>;
	/** Pending remote mkdir promises */
	_pendingCreates: Map<number, RDPendingCreate>;
	/** Queued folder jobs (max one active) */
	_folderJobQueue: RDFolderJob[];
	/** Active folder job */
	_activeFolderJob: RDFolderJob | null;
	_browseTimeout?: ReturnType<typeof setTimeout> | null;
	_browseTimedOut?: boolean;
	/**
	 * Set by the file sheet: saves a finished browser download into the
	 * chosen local folder; false (or a promise of false) falls back to the
	 * browser's download.
	 */
	_saveDownload?: ((fileName: string, blob: Blob) => boolean | Promise<boolean>) | null;

	constructor(opts: RDFileTransferOptions) {
		this._proto = opts.proto;
		this._sendMessage = opts.sendMessage;
		this._emit = opts.emit;
		this._ensureConnected = opts.ensureConnected || null;
		this._isConnected = opts.isConnected || null;

		this._currentPath = '';
		this._entries = [];
		this._transfers = new Map();
		this._nextId = 1;
		this._enabled = false;
		this._showHidden = false;

		// File type constants from proto
		this.FILE_TYPE = {
			DIR: 0,
			DIR_LINK: 2,
			DIR_DRIVE: 3,
			FILE: 4,
			FILE_LINK: 5
		};

		// Block size for uploads (128KB, matching hbb_common BUF_SIZE)
		this.BLOCK_SIZE = 131072;
		this.TRANSFER_STALL_MS = 15000;
		this.DOWNLOAD_WRITE_BATCH = 512 * 1024;
		this.PROGRESS_EMIT_MS = 200;

		this._overwriteStrategy = null;
		this._pendingOverwrite = new Map();
		this._pendingDirReads = new Map();
		this._pendingCreates = new Map();
		this._folderJobQueue = [];
		this._activeFolderJob = null;
	}

	_supportsStreamDownload(): boolean {
		return !!(page().__BETTERDESK_RDCLIENT_DESKTOP__ && tauriInvoke());
	}

	_bytesToBase64(data: Uint8Array | ArrayBuffer | number[]): string {
		return LocalFiles.bytesToBase64(data);
	}

	_normalizeRemotePathKey(path: string | null | undefined): string {
		return String(path || '').replace(/[\\/]+$/, '');
	}

	/**
	 * Join remote directory + relative path (uses remote separator).
	 * @param relativePath - uses `/` separators
	 */
	static buildRemoteRelativePath(
		remoteDir: string | null | undefined,
		relativePath: string | null | undefined
	): string {
		const rel = String(relativePath || '')
			.replace(/\\/g, '/')
			.replace(/^\/+/, '');
		if (!rel) return remoteDir || '';
		const parts = rel.split('/').filter(Boolean);
		let out = remoteDir || '';
		for (const part of parts) {
			out = RDFileTransfer.buildRemoteFilePath(out, part);
		}
		return out;
	}

	static joinLocalPath(base: string | null | undefined, relativePath: string | null | undefined): string {
		const rel = String(relativePath || '')
			.replace(/\\/g, '/')
			.replace(/^\/+/, '');
		if (!base) return rel;
		const sep = base.includes('\\') ? '\\' : '/';
		if (!rel) return base;
		const parts = rel.split('/').filter(Boolean);
		let out = base.replace(/[\\/]+$/, '');
		for (const part of parts) {
			out += sep + part;
		}
		return out;
	}

	/** Extensions that skip zstd compression (RustDesk is_compressed_file parity) */
	static get COMPRESSED_EXTENSIONS(): Set<string> {
		return new Set([
			'xz',
			'gz',
			'zip',
			'7z',
			'rar',
			'bz2',
			'tgz',
			'png',
			'jpg',
			'jpeg',
			'webp',
			'gif',
			'mp4',
			'mp3',
			'avi',
			'mkv'
		]);
	}

	static isPreCompressedFileName(fileName: string | null | undefined): boolean {
		const ext = (
			String(fileName || '')
				.split('.')
				.pop() ?? ''
		).toLowerCase();
		return RDFileTransfer.COMPRESSED_EXTENSIONS.has(ext);
	}

	static computeOffsetBlk(transferredSize: number | null | undefined, blockSize?: number | null): number {
		const size = Number(transferredSize || 0);
		const blk = Number(blockSize || 131072);
		if (!size || !blk) return 0;
		return Math.floor(size / blk);
	}

	/**
	 * Build full remote destination path for upload (RustDesk expects file path, not directory).
	 */
	static buildRemoteUploadPath(
		remoteDir: string | null | undefined,
		fileName: string | null | undefined
	): string {
		return RDFileTransfer.buildRemoteFilePath(remoteDir, fileName);
	}

	/**
	 * Join remote directory + file name (Windows or Unix separators).
	 * Used for download source paths and upload destination paths in send().
	 */
	static buildRemoteFilePath(
		remoteDir: string | null | undefined,
		fileName: string | null | undefined
	): string {
		const dir = remoteDir || '';
		const sep = dir.includes('\\') ? '\\' : '/';
		if (!dir) return fileName || '';
		return dir + (dir.endsWith(sep) ? '' : sep) + (fileName || '');
	}

	/**
	 * Parent directory for remote browse navigation (Windows drive roots + Unix).
	 * Bare `C:` is not a drive root — Win32 resolves it to the process cwd on that drive.
	 * @returns parent path, or '' for drive list / above Unix root
	 */
	static parentRemotePath(path: string | null | undefined): string {
		if (!path) return '';
		let parent = String(path).replace(/[\\/]+$/, '');
		if (!parent) return '';
		// Already at Windows drive root (C: or C:\ after strip) → drive list
		if (/^[A-Za-z]:$/.test(parent)) return '';
		const sep = parent.includes('\\') ? '\\' : '/';
		const lastSep = parent.lastIndexOf(sep);
		if (lastSep > 0) {
			parent = parent.substring(0, lastSep);
			// Keep trailing separator on drive roots: C:\Users → C:\ (not C:)
			if (/^[A-Za-z]:$/.test(parent)) return parent + '\\';
			return parent;
		}
		if (lastSep === 0) return sep; // Unix root
		return '';
	}

	_failTransfer(id: number, message?: string, opts?: { resumable?: boolean } | null): void {
		const options = opts || {};
		const transfer = this._transfers.get(id);
		if (!transfer) return;
		this._clearTransferTimeout(id);
		const fileName = transfer.fileName;
		const resumable = !!options.resumable;
		if (transfer.type === 'download' && typeof transfer.captureReject === 'function') {
			transfer.captureReject(new Error(message || 'Transfer failed'));
			transfer.captureResolve = null;
			transfer.captureReject = null;
		}
		const invoke = tauriInvoke();
		if (transfer.type === 'upload' && transfer.file && transfer.file.__rdNativeHandle && invoke) {
			invoke('desktop_release_file_handles', {
				handles: [transfer.file.__rdNativeHandle]
			}).catch(() => {
				/* ignore */
			});
		}
		if (transfer.type === 'download' && transfer.downloadHandle) {
			const keep = resumable;
			LocalFiles.abortDownload(transfer.downloadHandle, !keep).catch(() => {
				/* ignore */
			});
			if (!keep) {
				transfer.downloadHandle = null;
			}
		}
		if (typeof transfer._folderWait === 'function') {
			const resolve = transfer._folderWait;
			transfer._folderWait = null;
			resolve(false);
		}
		if (resumable) {
			transfer.status = 'error';
			transfer.errorMessage = message || 'Transfer failed';
			transfer.resumable = true;
		} else {
			this._transfers.delete(id);
		}
		this._emit('file_transfer_error', {
			id: id,
			fileName: fileName,
			error: message || 'Transfer failed',
			resumable: resumable,
			type: transfer.type,
			folderJobId: transfer.folderJobId || null,
			currentFile: null
		} satisfies RDFileTransferErrorEvent);
	}

	_needsFileConnection(): boolean | null {
		return this._ensureConnected && (!this._isConnected || !this._isConnected());
	}

	/** Runs run once the file connection is up; the promise settles with what run returned or threw */
	_runWithConnection<T>(run: () => Promise<T>): Promise<T>;
	_runWithConnection(run: () => void): Promise<void>;
	_runWithConnection(run: () => unknown): Promise<unknown> {
		if (this._ensureConnected) {
			if (this._needsFileConnection()) {
				this._emit('file_connecting');
			}
			return this._ensureConnected().then(() => run());
		}
		// Run synchronously so callers can register pending state before the next
		// event-loop turn (e.g. readDirAsync before an immediate FileResponse).
		try {
			return Promise.resolve(run());
		} catch (err) {
			return Promise.reject(err);
		}
	}

	_sendMessageSafe(msgObj: MessageInit): void {
		this._sendMessage(msgObj);
	}

	_armTransferTimeout(id: number): void {
		const transfer = this._transfers.get(id);
		if (!transfer) return;
		if (transfer.stallTimer) clearTimeout(transfer.stallTimer);
		transfer.stallTimer = setTimeout(() => {
			const t = this._transfers.get(id);
			if (!t || t.status !== 'pending') return;
			t.status = 'error';
			t.resumable =
				(t.type === 'upload' && (t.sentBytes || 0) > 0) ||
				(t.type === 'download' && (t.receivedBytes || 0) > 0);
			if (!t.resumable) {
				this._transfers.delete(id);
			}
			this._emit('file_transfer_error', {
				id: id,
				fileName: t.fileName,
				error: 'Remote did not start transfer',
				resumable: t.resumable,
				type: t.type
			} satisfies RDFileTransferErrorEvent);
		}, this.TRANSFER_STALL_MS);
	}

	_clearTransferTimeout(id: number): void {
		const transfer = this._transfers.get(id);
		if (transfer && transfer.stallTimer) {
			clearTimeout(transfer.stallTimer);
			transfer.stallTimer = null;
		}
	}

	/**
	 * Enable file transfer (called after successful login)
	 */
	enable(): void {
		this._enabled = true;
	}

	/**
	 * Disable file transfer
	 */
	disable(): void {
		this._enabled = false;
		this.cancelAll();
	}

	get enabled(): boolean {
		return this._enabled;
	}
	get currentPath(): string {
		return this._currentPath;
	}
	get entries(): RDRemoteFileEntry[] {
		return this._entries;
	}
	get showHidden(): boolean {
		return this._showHidden;
	}

	/**
	 * Respond to overwrite prompt from UI.
	 */
	confirmOverwrite(id: number, skip: boolean, applyToAll?: boolean): void {
		const pending = this._pendingOverwrite.get(id);
		if (!pending) return;
		this._pendingOverwrite.delete(id);
		if (applyToAll) {
			this._overwriteStrategy = skip ? 'skip' : 'overwrite';
		}
		pending.resolve(!!skip);
	}

	/**
	 * Resume a failed transfer when checkpoint data is available.
	 */
	resumeTransfer(id: number): void {
		const transfer = this._transfers.get(id);
		if (!transfer || !transfer.resumable) return;

		transfer.status = 'pending';
		transfer.errorMessage = null;
		transfer.resumable = false;

		this._emit('file_transfer_start', {
			id: id,
			type: transfer.type,
			fileName: transfer.fileName,
			fileSize: transfer.fileSize
		} satisfies RDFileTransferStartEvent);

		this._runWithConnection(() => {
			try {
				if (transfer.type === 'upload') {
					const modified =
						transfer.file && transfer.file.lastModified ? Math.floor(transfer.file.lastModified / 1000) : 0;
					const startOffset = transfer.sentBytes || 0;
					const isResume = startOffset > 0;
					if (!isResume) {
						const file = transfer.file;
						const files = [
							{
								entryType: this.FILE_TYPE.FILE,
								name: file.name,
								size: file.size,
								modifiedTime: modified
							}
						];
						this._sendMessageSafe(
							this._proto.buildFileReceiveRequest(
								id,
								transfer.remotePath,
								files,
								transfer.fileNum || 0,
								Number(file.size)
							)
						);
					}
					this._sendMessageSafe(
						this._proto.buildFileDigest(id, transfer.fileNum || 0, transfer.fileSize, modified, {
							isUpload: true,
							isResume: isResume,
							transferredSize: startOffset
						})
					);
					this._armTransferTimeout(id);
				} else if (transfer.type === 'download') {
					const remoteName = transfer.remoteFileName || transfer.fileName;
					const fullPath = RDFileTransfer.buildRemoteFilePath(transfer.remotePath, remoteName);
					const startSend = (): void => {
						this._sendMessageSafe(
							this._proto.buildFileSendRequest(id, fullPath, this._showHidden, transfer.fileNum || 0)
						);
						this._armTransferTimeout(id);
					};
					if (transfer.streamDownload && transfer.localPath) {
						LocalFiles.beginDownload({
							absolutePath: transfer.localPath,
							appendOffset: transfer.receivedBytes || 0
						})
							.then((result) => {
								if (!result || !answerField(result, 'started') || !answerField(result, 'handle')) {
									this._failTransfer(id, 'Download cancelled');
									return;
								}
								transfer.downloadHandle = answerField(result, 'handle');
								startSend();
							})
							.catch((err: unknown) => {
								this._failTransfer(id, errorMessage(err) || 'Could not resume download');
							});
					} else {
						startSend();
					}
				}
			} catch (err) {
				this._failTransfer(id, errorMessage(err) || String(err));
			}
		}).catch((err: unknown) => {
			this._failTransfer(id, errorMessage(err) || 'Could not connect file transfer session');
		});
	}

	/**
	 * Browse a directory on the remote machine
	 * @param path - Path to browse (empty = root/drives)
	 */
	browseDir(path?: string | null): void {
		if (!this._enabled) {
			console.warn('[FileTransfer] browseDir called but file transfer not enabled');
			return;
		}
		const dir = path != null ? path : '';
		const run = (): void => {
			debugLog('[FileTransfer] browseDir:', JSON.stringify(dir));
			this._sendMessageSafe(this._proto.buildReadDir(dir, this._showHidden));
			this._emit('file_browsing', { path: dir } satisfies RDFileBrowseEvent);
			if (this._browseTimeout) clearTimeout(this._browseTimeout);
			this._browseTimedOut = false;
			this._browseTimeout = setTimeout(() => {
				if (!this._browseTimedOut) {
					this._browseTimedOut = true;
					console.warn('[FileTransfer] browseDir timeout — no response from peer after 5s');
					this._emit('file_browse_timeout', { path: dir } satisfies RDFileBrowseEvent);
				}
			}, 5000);
		};
		if (this._ensureConnected) {
			if (this._needsFileConnection()) {
				this._emit('file_connecting');
			}
			this._ensureConnected()
				.then(run)
				.catch((err: unknown) => {
					this._emit('file_connect_error', {
						error: errorMessage(err) || String(err)
					} satisfies RDFileConnectErrorEvent);
				});
		} else {
			run();
		}
	}

	/**
	 * Navigate up to parent directory
	 */
	browseParent(): void {
		if (!this._currentPath) return;
		this.browseDir(RDFileTransfer.parentRemotePath(this._currentPath));
	}

	/**
	 * Toggle hidden file visibility
	 */
	setShowHidden(show: boolean): void {
		this._showHidden = !!show;
		// Refresh current directory
		if (this._enabled && this._currentPath !== undefined) {
			this.browseDir(this._currentPath);
		}
	}

	/**
	 * Download a file from remote
	 * @param remotePath - Remote directory path
	 * @param fileEntry - FileEntry { name, size, modifiedTime }
	 * @returns Transfer ID
	 */
	downloadFile(remotePath: string, fileEntry: RDRemoteFileRef, opts?: RDDownloadOptions | null): number {
		if (!this._enabled) return -1;
		const options = opts || {};

		const id = this._nextId++;
		const transfer: RDDownloadTransfer = {
			id: id,
			type: 'download',
			remotePath: remotePath,
			fileName: options.localFileName || fileEntry.name,
			remoteFileName: fileEntry.name,
			fileSize: Number(fileEntry.size || 0),
			receivedBytes: 0,
			blocks: this._supportsStreamDownload() && !options.forceBuffer ? null : [],
			streamDownload: this._supportsStreamDownload() && !options.forceBuffer,
			captureBlob: !!options.captureBlob,
			captureResolve: options.captureResolve || null,
			captureReject: options.captureReject || null,
			downloadHandle: null,
			localPath: null,
			localDir: options.localDir || null,
			absolutePath: options.absolutePath || null,
			folderJobId: options.folderJobId || null,
			writeChain: Promise.resolve(),
			receiveChain: Promise.resolve(),
			startTime: Date.now(),
			status: 'pending',
			fileNum: 0,
			stallTimer: null
		};
		this._transfers.set(id, transfer);

		if (!options.silent) {
			this._emit('file_transfer_start', {
				id: id,
				type: 'download',
				fileName: transfer.fileName,
				fileSize: transfer.fileSize,
				folderJobId: transfer.folderJobId
			} satisfies RDFileTransferStartEvent);
		}

		const startSend = (): void => {
			this._runWithConnection(() => {
				try {
					const fullPath = RDFileTransfer.buildRemoteFilePath(remotePath, fileEntry.name);
					this._sendMessageSafe(this._proto.buildFileSendRequest(id, fullPath, this._showHidden, 0));
					this._armTransferTimeout(id);
				} catch (err) {
					this._failTransfer(id, errorMessage(err) || String(err));
				}
			}).catch((err: unknown) => {
				this._failTransfer(id, errorMessage(err) || 'Could not connect file transfer session');
			});
		};

		if (transfer.streamDownload) {
			const beginOpts = {
				suggestedName: transfer.fileName,
				defaultDir: transfer.localDir,
				absolutePath: transfer.absolutePath,
				appendOffset: 0
			};
			LocalFiles.beginDownload(beginOpts)
				.then((result) => {
					if (!result || !answerField(result, 'started') || !answerField(result, 'handle')) {
						this._failTransfer(id, 'Download cancelled');
						return;
					}
					transfer.downloadHandle = answerField(result, 'handle');
					const path = answerField(result, 'path');
					transfer.localPath = (typeof path === 'string' && path) || null;
					startSend();
				})
				.catch((err: unknown) => {
					this._failTransfer(id, errorMessage(err) || 'Could not start download');
				});
		} else {
			startSend();
		}

		return id;
	}

	/**
	 * Download a remote file into browser memory for a second remote peer.
	 * This intentionally bypasses the desktop streaming path because the
	 * caller must receive a Blob before it can start the destination upload.
	 */
	downloadFileToBlob(remotePath: string, fileEntry: RDRemoteFileRef): Promise<Blob> {
		const size = Number((fileEntry && fileEntry.size) || 0);
		const maxSize = 256 * 1024 * 1024;
		if (size > maxSize) {
			return Promise.reject(new Error('Remote-to-remote browser transfer is limited to 256 MB per file'));
		}
		return new Promise((resolve, reject) => {
			this.downloadFile(remotePath, fileEntry, {
				forceBuffer: true,
				captureBlob: true,
				silent: true,
				captureResolve: resolve,
				captureReject: reject
			});
		});
	}

	/**
	 * Upload a file to remote
	 * @param file - Browser File object (or the desktop's native file)
	 * @param remotePath - Remote destination directory
	 * @returns Transfer ID
	 */
	uploadFile(file: RDUploadSource, remotePath: string, opts?: RDUploadOptions | null): number {
		if (!this._enabled) return -1;
		const options = opts || {};
		const remoteName = options.remoteFileName || file.name;

		const id = this._nextId++;
		const transfer: RDUploadTransfer = {
			id: id,
			type: 'upload',
			remotePath: remotePath,
			fileName: remoteName,
			fileSize: file.size,
			sentBytes: 0,
			file: file,
			folderJobId: options.folderJobId || null,
			startTime: Date.now(),
			status: 'pending',
			fileNum: 0,
			currentBlk: 0,
			stallTimer: null
		};
		this._transfers.set(id, transfer);

		if (!options.silent) {
			this._emit('file_transfer_start', {
				id: id,
				type: 'upload',
				fileName: remoteName,
				fileSize: file.size,
				folderJobId: transfer.folderJobId
			} satisfies RDFileTransferStartEvent);
		}

		this._runWithConnection(() => {
			try {
				const files = [
					{
						entryType: this.FILE_TYPE.FILE,
						name: remoteName,
						size: file.size,
						modifiedTime: file.lastModified ? Math.floor(file.lastModified / 1000) : 0
					}
				];
				this._sendMessageSafe(
					this._proto.buildFileReceiveRequest(id, remotePath, files, 0, Number(file.size))
				);
				const modified = file.lastModified ? Math.floor(file.lastModified / 1000) : 0;
				this._sendMessageSafe(this._proto.buildFileDigest(id, 0, file.size, modified));
				this._armTransferTimeout(id);
			} catch (err) {
				this._failTransfer(id, errorMessage(err) || String(err));
			}
		}).catch((err: unknown) => {
			this._failTransfer(id, errorMessage(err) || 'Could not connect file transfer session');
		});

		return id;
	}

	/**
	 * Cancel a transfer (folder job id cancels the whole folder tree).
	 */
	cancelTransfer(id: number): void {
		if (this._activeFolderJob && this._activeFolderJob.id === id) {
			this._cancelFolderJob(id);
			return;
		}
		for (let i = 0; i < this._folderJobQueue.length; i++) {
			const job = this._folderJobQueue[i];
			if (job && job.id === id) {
				this._folderJobQueue.splice(i, 1);
				this._emit('file_transfer_cancelled', {
					id: id,
					fileName: job.fileName,
					isFolder: true
				} satisfies RDFileTransferCancelledEvent);
				return;
			}
		}

		const transfer = this._transfers.get(id);
		if (!transfer) return;

		transfer.status = 'cancelled';
		this._clearTransferTimeout(id);
		try {
			this._sendMessageSafe(this._proto.buildFileCancel(id));
		} catch {
			/* ignore */
		}
		if (transfer.type === 'download' && transfer.downloadHandle) {
			LocalFiles.abortDownload(transfer.downloadHandle, true).catch(() => {
				/* ignore */
			});
			transfer.downloadHandle = null;
		}
		this._transfers.delete(id);

		this._emit('file_transfer_cancelled', {
			id: id,
			fileName: transfer.fileName,
			folderJobId: transfer.folderJobId || null
		} satisfies RDFileTransferCancelledEvent);
		if (transfer.folderJobId) {
			this._onChildTransferSettled(transfer.folderJobId, id, false);
		}
	}

	/**
	 * Cancel all active transfers
	 */
	cancelAll(): void {
		if (this._activeFolderJob) {
			this._cancelFolderJob(this._activeFolderJob.id);
		}
		this._folderJobQueue = [];
		for (const [id] of this._transfers) {
			this.cancelTransfer(id);
		}
	}

	/**
	 * Create directory on remote
	 */
	createDir(path: string): void {
		if (!this._enabled) return;
		const id = this._nextId++;
		this._sendMessage(this._proto.buildFileDirCreate(id, path));
		this._emit('file_action', { action: 'create_dir', path: path } satisfies RDFileActionEvent);
	}

	/**
	 * Create remote directory and resolve when peer acks or times out.
	 */
	createDirAsync(path: string): Promise<void> {
		if (!this._enabled) return Promise.reject(new Error('File transfer not enabled'));
		return this._runWithConnection(() => {
			const id = this._nextId++;
			return new Promise<void>((resolve, reject) => {
				const timer = setTimeout(() => {
					this._pendingCreates.delete(id);
					resolve();
				}, 2000);
				this._pendingCreates.set(id, { resolve: resolve, reject: reject, timer: timer, path: path });
				try {
					this._sendMessageSafe(this._proto.buildFileDirCreate(id, path));
				} catch (err) {
					clearTimeout(timer);
					this._pendingCreates.delete(id);
					reject(err);
				}
			});
		});
	}

	/**
	 * Background read_dir that does not clobber the modal's current listing.
	 */
	readDirAsync(path: string | null | undefined): Promise<RDRemoteFileEntry[]> {
		if (!this._enabled) return Promise.reject(new Error('File transfer not enabled'));
		const key = this._normalizeRemotePathKey(path);
		return this._runWithConnection(() => {
			return new Promise<RDRemoteFileEntry[]>((resolve, reject) => {
				if (this._pendingDirReads.has(key)) {
					const prev = this._pendingDirReads.get(key);
					clearTimeout(prev?.timer);
				}
				const timer = setTimeout(() => {
					this._pendingDirReads.delete(key);
					reject(new Error('Remote directory listing timed out'));
				}, 10000);
				this._pendingDirReads.set(key, { resolve: resolve, reject: reject, timer: timer });
				try {
					this._sendMessageSafe(this._proto.buildReadDir(path || '', this._showHidden));
				} catch (err) {
					clearTimeout(timer);
					this._pendingDirReads.delete(key);
					reject(err);
				}
			});
		});
	}

	/**
	 * Upload a local folder tree as one folder job (sequential children).
	 * @param walk - { rootName, dirs[], files[] } from LocalFiles.walk*
	 * @param remoteDest - Remote parent directory
	 * @returns Folder job id
	 */
	uploadFolder(walk: RDFolderWalk | null | undefined, remoteDest: string | null | undefined): number {
		if (!this._enabled || !walk) return -1;
		const rootName = walk.rootName || 'folder';
		const dirs = walk.dirs || [];
		const files = walk.files || [];
		let totalSize = 0;
		for (const f of files) totalSize += Number(f.size || 0);

		const id = this._nextId++;
		const job: RDFolderUploadJob = {
			id: id,
			type: 'upload',
			isFolder: true,
			fileName: rootName,
			fileSize: totalSize,
			transferred: 0,
			status: 'queued',
			remoteDest: remoteDest || '',
			rootName: rootName,
			dirs: dirs,
			files: files,
			currentFile: null,
			childId: null,
			fileIndex: 0
		};
		this._enqueueFolderJob(job);
		return id;
	}

	/**
	 * Download a remote folder tree as one folder job.
	 * @param remotePath - Parent directory of the folder
	 * @param folderEntry - { name, isDir }
	 * @param localRoot - Absolute local destination directory (parent)
	 * @returns Folder job id
	 */
	downloadFolder(
		remotePath: string | null | undefined,
		folderEntry: RDFolderEntry | null | undefined,
		localRoot: string | null | undefined
	): number {
		if (!this._enabled || !folderEntry || !localRoot) return -1;
		const id = this._nextId++;
		const job: RDFolderDownloadJob = {
			id: id,
			type: 'download',
			isFolder: true,
			fileName: folderEntry.name,
			fileSize: 0,
			transferred: 0,
			status: 'queued',
			remotePath: remotePath || '',
			folderEntry: folderEntry,
			localRoot: localRoot,
			currentFile: null,
			childId: null,
			pendingFiles: [],
			fileIndex: 0
		};
		this._enqueueFolderJob(job);
		return id;
	}

	/**
	 * Download a remote folder tree in the browser: every file of it is
	 * fetched through the file transfer session, then the folder is saved as
	 * one ZIP archive (<name>.zip, into the sheet's local folder when one is
	 * open, else as a browser download).
	 * @returns Folder job id
	 */
	downloadFolderAsZip(
		remotePath: string | null | undefined,
		folderEntry: RDFolderEntry | null | undefined
	): number {
		if (!this._enabled || !folderEntry) return -1;
		const id = this._nextId++;
		this._enqueueFolderJob({
			id: id,
			type: 'download',
			isFolder: true,
			fileName: folderEntry.name,
			fileSize: 0,
			transferred: 0,
			status: 'queued',
			remotePath: remotePath || '',
			folderEntry: folderEntry,
			localRoot: '',
			currentFile: null,
			childId: null,
			pendingFiles: [],
			fileIndex: 0,
			asZip: true
		});
		return id;
	}

	_enqueueFolderJob(job: RDFolderJob): void {
		this._emit('file_transfer_start', {
			id: job.id,
			type: job.type,
			fileName: job.fileName,
			fileSize: job.fileSize,
			isFolder: true,
			percent: 0
		} satisfies RDFileTransferStartEvent);
		this._folderJobQueue.push(job);
		this._pumpFolderJobs();
	}

	_pumpFolderJobs(): void {
		if (this._activeFolderJob) return;
		const job = this._folderJobQueue.shift();
		if (!job) return;
		this._activeFolderJob = job;
		job.status = 'transferring';
		const run = job.type === 'upload' ? this._runFolderUpload(job) : this._runFolderDownload(job);
		Promise.resolve(run).catch((err: unknown) => {
			if (this._activeFolderJob && this._activeFolderJob.id === job.id) {
				job.status = 'error';
				this._emit('file_transfer_error', {
					id: job.id,
					fileName: job.fileName,
					error: errorMessage(err) || String(err),
					resumable: false,
					type: job.type,
					isFolder: true
				} satisfies RDFileTransferErrorEvent);
				this._activeFolderJob = null;
				this._pumpFolderJobs();
			}
		});
	}

	_cancelFolderJob(id: number): void {
		const job = this._activeFolderJob;
		if (!job || job.id !== id) return;
		job.status = 'cancelled';
		if (job.childId != null) {
			const child = this._transfers.get(job.childId);
			if (child) {
				child.folderJobId = null;
				this.cancelTransfer(job.childId);
			}
		}
		this._activeFolderJob = null;
		this._emit('file_transfer_cancelled', {
			id: id,
			fileName: job.fileName,
			isFolder: true
		} satisfies RDFileTransferCancelledEvent);
		this._pumpFolderJobs();
	}

	_emitFolderProgress(job: RDFolderJob): void {
		const percent = job.fileSize > 0 ? Math.min(100, Math.round((job.transferred / job.fileSize) * 100)) : 0;
		this._emit('file_transfer_progress', {
			id: job.id,
			fileName: job.fileName,
			fileSize: job.fileSize,
			transferred: job.transferred,
			percent: percent,
			type: job.type,
			isFolder: true,
			currentFile: job.currentFile,
			phase: 'transferring'
		} satisfies RDFileTransferProgressEvent);
	}

	_finishFolderJob(job: RDFolderJob, ok: boolean): void {
		if (this._activeFolderJob && this._activeFolderJob.id === job.id) {
			this._activeFolderJob = null;
		}
		if (job.status === 'cancelled') {
			this._pumpFolderJobs();
			return;
		}
		if (ok) {
			job.status = 'complete';
			this._emit('file_transfer_complete', {
				id: job.id,
				fileName: job.fileName,
				fileSize: job.fileSize,
				type: job.type,
				isFolder: true,
				elapsed: (Date.now() - (job.startTime || Date.now())) / 1000
			} satisfies RDFileTransferCompleteEvent);
			if (job.type === 'upload') {
				this.browseDir(this._currentPath);
			}
		}
		this._pumpFolderJobs();
	}

	_waitForChildTransfer(childId: number): Promise<boolean> {
		return new Promise((resolve) => {
			const transfer = this._transfers.get(childId);
			if (!transfer) {
				resolve(false);
				return;
			}
			transfer._folderWait = resolve;
		});
	}

	_onChildTransferSettled(folderJobId: number, childId: number, ok: boolean): void {
		const job = this._activeFolderJob;
		if (!job || job.id !== folderJobId) return;
		const transfer = this._transfers.get(childId);
		if (transfer && typeof transfer._folderWait === 'function') {
			const resolve = transfer._folderWait;
			transfer._folderWait = null;
			resolve(ok);
		}
	}

	async _runFolderUpload(job: RDFolderUploadJob): Promise<void> {
		job.startTime = Date.now();
		const remoteRoot = RDFileTransfer.buildRemoteFilePath(job.remoteDest || '', job.rootName);
		try {
			await this.createDirAsync(remoteRoot);
			await new Promise((r) => {
				setTimeout(r, 0);
			});

			for (const dir of job.dirs) {
				if (isCancelled(job)) return this._finishFolderJob(job, false);
				const rel = dir.relativePath || dir.name;
				const remoteDir = RDFileTransfer.buildRemoteRelativePath(remoteRoot, rel);
				job.currentFile = rel;
				this._emitFolderProgress(job);
				await this.createDirAsync(remoteDir);
				await new Promise((r) => {
					setTimeout(r, 0);
				});
			}

			for (const [i, item] of job.files.entries()) {
				if (isCancelled(job)) return this._finishFolderJob(job, false);
				const rel = item.relativePath || item.name;
				job.currentFile = rel;
				job.fileIndex = i;
				this._emitFolderProgress(job);

				let file = item.file || null;
				if (!file && item.path) {
					const info = await desktopInvoke('desktop_open_file', { path: item.path });
					file = LocalFiles.createNativeUploadFile(info);
				}
				if (!file) {
					throw new Error('Could not open local file: ' + rel);
				}

				// Upload into remoteRoot with relative name so peer creates nested path.
				const parentRel = rel.includes('/') ? rel.replace(/\/[^/]+$/, '') : '';
				const remoteParent = parentRel
					? RDFileTransfer.buildRemoteRelativePath(remoteRoot, parentRel)
					: remoteRoot;
				const baseName = rel.includes('/') ? rel.split('/').pop() : rel;

				const childId = this.uploadFile(file, remoteParent, {
					remoteFileName: baseName,
					folderJobId: job.id,
					silent: true
				});
				job.childId = childId;
				const ok = await this._waitForChildTransfer(childId);
				job.childId = null;
				if (isCancelled(job)) return this._finishFolderJob(job, false);
				if (!ok) {
					throw new Error('Failed to upload ' + rel);
				}
				job.transferred += Number(item.size || file.size || 0);
				this._emitFolderProgress(job);
				await new Promise((r) => {
					setTimeout(r, 0);
				});
			}

			this._finishFolderJob(job, true);
		} catch (err) {
			if (isCancelled(job)) return this._finishFolderJob(job, false);
			job.status = 'error';
			this._emit('file_transfer_error', {
				id: job.id,
				fileName: job.fileName,
				error: errorMessage(err) || String(err),
				resumable: false,
				type: 'upload',
				isFolder: true,
				currentFile: job.currentFile
			} satisfies RDFileTransferErrorEvent);
			this._finishFolderJob(job, false);
		}
	}

	async _expandRemoteFolder(
		remoteFolderPath: string
	): Promise<{ dirs: RDRemoteTreeDir[]; files: RDRemoteTreeFile[]; totalSize: number }> {
		const dirs: RDRemoteTreeDir[] = [];
		const files: RDRemoteTreeFile[] = [];
		let totalSize = 0;
		const queue = [remoteFolderPath];
		while (queue.length) {
			const dirPath = queue.shift() ?? '';
			const entries = await this.readDirAsync(dirPath);
			for (const e of entries) {
				const childPath = RDFileTransfer.buildRemoteFilePath(dirPath, e.name);
				const rel = remoteFolderPath
					? childPath
							.slice(this._normalizeRemotePathKey(remoteFolderPath).length)
							.replace(/^[\\/]+/, '')
							.replace(/\\/g, '/')
					: e.name;
				if (e.isDir) {
					dirs.push({ name: e.name, path: childPath, relativePath: rel });
					queue.push(childPath);
				} else {
					files.push({
						name: e.name,
						path: childPath,
						relativePath: rel,
						size: Number(e.size || 0),
						modifiedTime: Number(e.modifiedTime || 0),
						entry: e,
						parentPath: dirPath
					});
					totalSize += Number(e.size || 0);
				}
			}
			await new Promise((r) => {
				setTimeout(r, 0);
			});
		}
		dirs.sort((a, b) => {
			return a.relativePath.length - b.relativePath.length || a.relativePath.localeCompare(b.relativePath);
		});
		return { dirs: dirs, files: files, totalSize: totalSize };
	}

	/**
	 * Fetch a remote folder into localRoot on the desktop (the file sheet
	 * refuses folder downloads in a browser before it gets here).
	 */
	/** Fetch a remote folder into memory and save it as <name>.zip (downloadFolderAsZip) */
	async _runZipFolderDownload(job: RDFolderDownloadJob): Promise<void> {
		job.startTime = Date.now();
		const name = job.folderEntry.name;
		const remoteFolder = RDFileTransfer.buildRemoteFilePath(job.remotePath || '', name);
		try {
			job.currentFile = '';
			this._emitFolderProgress(job);
			const tree = await this._expandRemoteFolder(remoteFolder);
			if (isCancelled(job)) return this._finishFolderJob(job, false);
			job.fileSize = tree.totalSize;
			job.pendingFiles = tree.files;
			this._emitFolderProgress(job);

			const entries: ZipEntry[] = [{ path: name + '/' }];
			for (const dir of tree.dirs) entries.push({ path: `${name}/${dir.relativePath}/` });
			for (const [i, item] of tree.files.entries()) {
				if (isCancelled(job)) return this._finishFolderJob(job, false);
				job.currentFile = item.relativePath;
				job.fileIndex = i;
				this._emitFolderProgress(job);
				let data: Blob | null = null;
				const childId = this.downloadFile(
					item.parentPath,
					item.entry || { name: item.name, size: item.size, modifiedTime: item.modifiedTime },
					{
						folderJobId: job.id,
						silent: true,
						localFileName: item.relativePath,
						forceBuffer: true,
						captureBlob: true,
						captureResolve: (blob) => {
							data = blob;
						}
					}
				);
				job.childId = childId;
				const ok = await this._waitForChildTransfer(childId);
				job.childId = null;
				if (isCancelled(job)) return this._finishFolderJob(job, false);
				if (!ok || !data) throw new Error('Failed to download ' + item.relativePath);
				entries.push({ path: `${name}/${item.relativePath}`, data, modifiedTime: item.modifiedTime });
				job.transferred += Number(item.size || 0);
				this._emitFolderProgress(job);
			}

			const zip = await buildZip(entries);
			if (isCancelled(job)) return this._finishFolderJob(job, false);
			// A failed save already reported its error for this job
			if (!(await this._saveBlob(job.id, name + '.zip', zip))) {
				job.status = 'error';
				return this._finishFolderJob(job, false);
			}
			this._finishFolderJob(job, true);
		} catch (err) {
			if (isCancelled(job)) return this._finishFolderJob(job, false);
			job.status = 'error';
			this._emit('file_transfer_error', {
				id: job.id,
				fileName: job.fileName,
				error: errorMessage(err) || String(err),
				resumable: false,
				type: 'download',
				isFolder: true,
				currentFile: job.currentFile
			} satisfies RDFileTransferErrorEvent);
			this._finishFolderJob(job, false);
		}
	}

	async _runFolderDownload(job: RDFolderDownloadJob): Promise<void> {
		if (job.asZip) return this._runZipFolderDownload(job);
		job.startTime = Date.now();
		const remoteFolder = RDFileTransfer.buildRemoteFilePath(job.remotePath || '', job.folderEntry.name);
		const localFolder = RDFileTransfer.joinLocalPath(job.localRoot, job.folderEntry.name);
		try {
			if (LocalFiles.isDesktopBridge()) {
				await desktopInvoke('desktop_mkdir_p', { path: localFolder });
			}

			job.currentFile = '';
			this._emit('file_transfer_progress', {
				id: job.id,
				fileName: job.fileName,
				fileSize: 0,
				transferred: 0,
				percent: 0,
				type: 'download',
				isFolder: true,
				phase: 'transferring',
				currentFile: '…'
			} satisfies RDFileTransferProgressEvent);

			const tree = await this._expandRemoteFolder(remoteFolder);
			if (isCancelled(job)) return this._finishFolderJob(job, false);
			job.fileSize = tree.totalSize;
			job.pendingFiles = tree.files;
			this._emitFolderProgress(job);

			for (const dir of tree.dirs) {
				if (isCancelled(job)) return this._finishFolderJob(job, false);
				const localDir = RDFileTransfer.joinLocalPath(localFolder, dir.relativePath);
				await desktopInvoke('desktop_mkdir_p', { path: localDir });
			}

			for (const [i, item] of tree.files.entries()) {
				if (isCancelled(job)) return this._finishFolderJob(job, false);
				job.currentFile = item.relativePath;
				job.fileIndex = i;
				this._emitFolderProgress(job);

				const localParent = RDFileTransfer.joinLocalPath(
					localFolder,
					item.relativePath.includes('/') ? item.relativePath.replace(/\/[^/]+$/, '') : ''
				);
				if (localParent && localParent !== localFolder) {
					await desktopInvoke('desktop_mkdir_p', { path: localParent });
				}
				const absolutePath = RDFileTransfer.joinLocalPath(localFolder, item.relativePath);

				const childId = this.downloadFile(
					item.parentPath,
					item.entry || {
						name: item.name,
						size: item.size,
						modifiedTime: item.modifiedTime
					},
					{
						absolutePath: absolutePath,
						folderJobId: job.id,
						silent: true,
						localFileName: item.relativePath
					}
				);
				job.childId = childId;
				const ok = await this._waitForChildTransfer(childId);
				job.childId = null;
				if (isCancelled(job)) return this._finishFolderJob(job, false);
				if (!ok) {
					throw new Error('Failed to download ' + item.relativePath);
				}
				job.transferred += Number(item.size || 0);
				this._emitFolderProgress(job);
				await new Promise((r) => {
					setTimeout(r, 0);
				});
			}

			this._finishFolderJob(job, true);
		} catch (err) {
			if (isCancelled(job)) return this._finishFolderJob(job, false);
			job.status = 'error';
			this._emit('file_transfer_error', {
				id: job.id,
				fileName: job.fileName,
				error: errorMessage(err) || String(err),
				resumable: false,
				type: 'download',
				isFolder: true,
				currentFile: job.currentFile
			} satisfies RDFileTransferErrorEvent);
			this._finishFolderJob(job, false);
		}
	}

	/**
	 * Delete file on remote
	 */
	removeFile(path: string): void {
		if (!this._enabled) return;
		const id = this._nextId++;
		this._sendMessage(this._proto.buildFileRemove(id, path, 0));
		this._emit('file_action', { action: 'remove_file', path: path } satisfies RDFileActionEvent);
	}

	/**
	 * Delete directory on remote
	 */
	removeDir(path: string, recursive: boolean): void {
		if (!this._enabled) return;
		const id = this._nextId++;
		this._sendMessage(this._proto.buildFileRemoveDir(id, path, recursive));
		this._emit('file_action', { action: 'remove_dir', path: path } satisfies RDFileActionEvent);
	}

	/**
	 * Rename file/directory on remote
	 */
	rename(path: string, newName: string): void {
		if (!this._enabled) return;
		const id = this._nextId++;
		this._sendMessage(this._proto.buildFileRename(id, path, newName));
		this._emit('file_action', { action: 'rename', path: path, newName: newName } satisfies RDFileActionEvent);
	}

	// ---- Incoming message handlers ----

	/**
	 * Handle FileResponse from peer
	 */
	handleFileResponse(resp: FileResponse): void {
		debugLog(
			'[FileTransfer] handleFileResponse:',
			Object.keys(resp)
				.filter((k) => Reflect.get(resp, k) != null)
				.join(', ')
		);
		if (resp.dir) {
			this._handleDir(resp.dir);
		} else if (resp.block) {
			this._handleBlock(resp.block);
		} else if (resp.digest) {
			this._handleDigest(resp.digest);
		} else if (resp.done) {
			this._handleDone(resp.done);
		} else if (resp.error) {
			this._handleError(resp.error);
		}
	}

	/**
	 * Handle directory listing response
	 */
	_handleDir(dir: FileDirectory): void {
		// Clear browse timeout — we got a response
		if (this._browseTimeout) {
			clearTimeout(this._browseTimeout);
			this._browseTimeout = null;
		}
		this._browseTimedOut = false;

		debugLog(
			'[FileTransfer] _handleDir: path=%s entries=%d',
			dir.path || '(root)',
			(dir.entries || []).length
		);
		const entries: RDRemoteFileEntry[] = (dir.entries || []).map((e) => ({
			name: e.name,
			entryType: e.entryType != null ? e.entryType : 0,
			isHidden: !!e.isHidden,
			size: Number(e.size || 0),
			modifiedTime: Number(e.modifiedTime || 0),
			isDir: (e.entryType || 0) <= 3
		}));

		// Sort: directories first, then by name
		entries.sort((a, b) => {
			if (a.isDir && !b.isDir) return -1;
			if (!a.isDir && b.isDir) return 1;
			return a.name.localeCompare(b.name, undefined, { sensitivity: 'base' });
		});

		const pathKey = this._normalizeRemotePathKey(dir.path || '');
		let pending: RDPendingDirRead | null | undefined = null;
		if (this._pendingDirReads.has(pathKey)) {
			pending = this._pendingDirReads.get(pathKey);
			this._pendingDirReads.delete(pathKey);
		} else if (this._pendingDirReads.size === 1) {
			// Peers may return a slightly different path string — match the sole pending read.
			const onlyKey = this._pendingDirReads.keys().next().value ?? '';
			pending = this._pendingDirReads.get(onlyKey);
			this._pendingDirReads.delete(onlyKey);
		}
		if (pending) {
			clearTimeout(pending.timer);
			pending.resolve(entries);
			return;
		}

		this._currentPath = dir.path || '';
		this._entries = entries;
		this._emit('file_dir', {
			path: this._currentPath,
			entries: this._entries
		} satisfies RDFileDirEvent);
	}

	/**
	 * Handle transfer digest (file metadata before data blocks)
	 */
	_handleDigest(digest: FileTransferDigest): void {
		const id = Number(digest.id);
		const transfer = this._transfers.get(id);
		if (!transfer) return;

		this._clearTransferTimeout(id);
		transfer.fileSize = Number(digest.fileSize || transfer.fileSize || 0);
		transfer.fileNum = Number(digest.fileNum != null ? digest.fileNum : 0);
		transfer.status = 'transferring';

		const fileNum = transfer.fileNum;
		const isIdentical = !!digest.isIdentical;
		const transferredSize = Number(digest.transferredSize != null ? digest.transferredSize : 0);
		const isResume = !!digest.isResume;

		if (transfer.type === 'download') {
			let offsetBlk = 0;
			if (isResume && transferredSize > 0) {
				offsetBlk = RDFileTransfer.computeOffsetBlk(transferredSize, this.BLOCK_SIZE);
				transfer.receivedBytes = transferredSize;
			} else if (transfer.receivedBytes > 0) {
				offsetBlk = RDFileTransfer.computeOffsetBlk(transfer.receivedBytes, this.BLOCK_SIZE);
			}
			this._sendMessageSafe(this._proto.buildFileSendConfirm(id, fileNum, false, offsetBlk));
		} else if (transfer.type === 'upload') {
			// Peer-initiated digest (overwrite check) — operator already sent initial digest.
			if (!digest.isUpload) return;
			if (isIdentical) {
				this._sendMessageSafe(this._proto.buildFileSendConfirm(id, fileNum, true, 0));
				if (typeof transfer._folderWait === 'function') {
					const resolve = transfer._folderWait;
					transfer._folderWait = null;
					resolve(true);
				} else {
					this._emit('file_transfer_complete', {
						id: id,
						fileName: transfer.fileName,
						fileSize: transfer.fileSize,
						type: 'upload',
						elapsed: (Date.now() - transfer.startTime) / 1000
					} satisfies RDFileTransferCompleteEvent);
				}
				this._transfers.delete(id);
				return;
			}
			const proceedUpload = (skip: boolean): void => {
				if (skip) {
					this._sendMessageSafe(this._proto.buildFileSendConfirm(id, fileNum, true, 0));
					if (typeof transfer._folderWait === 'function') {
						const resolve = transfer._folderWait;
						transfer._folderWait = null;
						resolve(true);
					} else {
						this._emit('file_transfer_complete', {
							id: id,
							fileName: transfer.fileName,
							fileSize: transfer.fileSize,
							type: 'upload',
							elapsed: (Date.now() - transfer.startTime) / 1000,
							skipped: true
						} satisfies RDFileTransferCompleteEvent);
					}
					this._transfers.delete(id);
					return;
				}
				this._sendMessageSafe(this._proto.buildFileSendConfirm(id, fileNum, false, 0));
				this._sendUploadBlocks(transfer, transfer.sentBytes || 0);
			};

			if (this._overwriteStrategy === 'skip') {
				proceedUpload(true);
				return;
			}
			if (this._overwriteStrategy === 'overwrite') {
				proceedUpload(false);
				return;
			}

			this._pendingOverwrite.set(id, {
				resolve: proceedUpload
			});
			this._emit('file_transfer_overwrite_prompt', {
				id: id,
				fileName: transfer.fileName,
				fileSize: transfer.fileSize,
				remotePath: transfer.remotePath
			} satisfies RDFileOverwritePromptEvent);
			return;
		}

		// Only a download gets here: the upload branch above always returns.
		const startTransferred = transfer.receivedBytes || transferredSize || 0;
		this._emit('file_transfer_progress', {
			id: id,
			fileName: transfer.fileName,
			fileSize: transfer.fileSize,
			transferred: startTransferred,
			percent:
				transfer.fileSize > 0 ? Math.min(100, Math.round((startTransferred / transfer.fileSize) * 100)) : 0,
			type: transfer.type,
			phase: 'transferring'
		} satisfies RDFileTransferProgressEvent);
	}

	/**
	 * Handle FileAction.send_confirm from peer (upload ready to stream).
	 */
	handleSendConfirm(confirm: FileTransferSendConfirmRequest): void {
		const id = Number(confirm.id);
		const transfer = this._transfers.get(id);
		if (!transfer || transfer.type !== 'upload') return;

		this._clearTransferTimeout(id);
		transfer.fileNum = Number(confirm.fileNum != null ? confirm.fileNum : 0);

		if (confirm.skip === true) {
			if (typeof transfer._folderWait === 'function') {
				const resolve = transfer._folderWait;
				transfer._folderWait = null;
				resolve(true);
			} else {
				this._emit('file_transfer_complete', {
					id: id,
					fileName: transfer.fileName,
					fileSize: transfer.fileSize,
					type: 'upload',
					elapsed: (Date.now() - transfer.startTime) / 1000
				} satisfies RDFileTransferCompleteEvent);
			}
			this._transfers.delete(id);
			return;
		}

		const offsetBlk = Number(confirm.offsetBlk != null ? confirm.offsetBlk : 0);
		const startOffset = offsetBlk > 0 ? offsetBlk * this.BLOCK_SIZE : transfer.sentBytes || 0;

		transfer.status = 'transferring';
		this._emit('file_transfer_progress', {
			id: id,
			fileName: transfer.fileName,
			fileSize: transfer.fileSize,
			transferred: startOffset,
			percent: transfer.fileSize > 0 ? Math.min(100, Math.round((startOffset / transfer.fileSize) * 100)) : 0,
			type: 'upload',
			phase: 'transferring'
		} satisfies RDFileTransferProgressEvent);
		this._sendUploadBlocks(transfer, startOffset);
	}

	/**
	 * Handle data block (download)
	 */
	_handleBlock(block: FileTransferBlock): void {
		const id = Number(block.id);
		const transfer = this._transfers.get(id);
		if (!transfer || transfer.type !== 'download') return;

		const emitProgress = (): void => {
			const percent =
				transfer.fileSize > 0
					? Math.min(100, Math.round((transfer.receivedBytes / transfer.fileSize) * 100))
					: 0;
			if (
				transfer.folderJobId &&
				this._activeFolderJob &&
				this._activeFolderJob.id === transfer.folderJobId
			) {
				const job = this._activeFolderJob;
				const childTransferred = transfer.receivedBytes || 0;
				this._emit('file_transfer_progress', {
					id: job.id,
					fileName: job.fileName,
					fileSize: job.fileSize,
					transferred: job.transferred + childTransferred,
					percent:
						job.fileSize > 0
							? Math.min(100, Math.round(((job.transferred + childTransferred) / job.fileSize) * 100))
							: percent,
					type: 'download',
					isFolder: true,
					currentFile: job.currentFile,
					phase: 'transferring'
				} satisfies RDFileTransferProgressEvent);
				return;
			}
			this._emit('file_transfer_progress', {
				id: id,
				fileName: transfer.fileName,
				fileSize: transfer.fileSize,
				transferred: transfer.receivedBytes,
				percent: percent,
				type: 'download',
				phase: 'transferring'
			} satisfies RDFileTransferProgressEvent);
		};

		const flushWriteBatch = (): Promise<unknown> => {
			if (!transfer._writeBatch || !transfer._writeBatch.length) {
				return Promise.resolve();
			}
			const parts = transfer._writeBatch;
			transfer._writeBatch = [];
			transfer._writeBatchLen = 0;
			let total = 0;
			for (const part of parts) total += part.length;
			const merged = new Uint8Array(total);
			let off = 0;
			for (const part of parts) {
				merged.set(part, off);
				off += part.length;
			}
			return LocalFiles.writeDownload(transfer.downloadHandle, merged);
		};

		const maybeEmitProgress = (force: boolean): void => {
			const now = Date.now();
			if (!force && transfer._lastProgressEmit && now - transfer._lastProgressEmit < this.PROGRESS_EMIT_MS) {
				return;
			}
			transfer._lastProgressEmit = now;
			emitProgress();
		};

		const applyBlock = (data: Uint8Array | null | undefined): void => {
			const bytes = data && data.length ? (data instanceof Uint8Array ? data : new Uint8Array(data)) : null;
			if (transfer.streamDownload && transfer.downloadHandle) {
				transfer.writeChain = transfer.writeChain
					.then(() => {
						if (!bytes || !bytes.length) {
							maybeEmitProgress(false);
							return null;
						}
						if (!transfer._writeBatch) {
							transfer._writeBatch = [];
							transfer._writeBatchLen = 0;
						}
						transfer._writeBatch.push(bytes);
						transfer._writeBatchLen = (transfer._writeBatchLen || 0) + bytes.length;
						transfer.receivedBytes += bytes.length;
						const flush =
							transfer._writeBatchLen >= this.DOWNLOAD_WRITE_BATCH ? flushWriteBatch() : Promise.resolve();
						return flush.then(() => {
							maybeEmitProgress(false);
						});
					})
					.catch((err: unknown) => {
						this._failTransfer(id, 'Failed to write download: ' + (errorMessage(err) || String(err)));
					});
				return;
			}
			if (bytes && bytes.length) {
				if (!transfer.blocks) transfer.blocks = [];
				transfer.blocks.push(bytes);
				transfer.receivedBytes += bytes.length;
			}
			maybeEmitProgress(false);
		};

		const raw = block.data;
		const compressed = !!(block.compressed && raw && raw.length);
		// A compressed block takes a while to inflate: the blocks after it wait, so the file keeps their order
		const active = () => this._transfers.get(id) === transfer && transfer.status !== 'error';
		transfer.receiveChain = transfer.receiveChain.then(async () => {
			if (!active()) return;
			let data = raw;
			if (compressed) {
				try {
					data = await this._decompressBlock(raw);
				} catch (err) {
					this._failTransfer(id, 'Failed to decompress block: ' + (errorMessage(err) || String(err)));
					return;
				}
			}
			if (active()) applyBlock(data);
		});
	}

	/**
	 * Decompress a zstd block from the RustDesk peer (when compressed=true).
	 */
	async _decompressBlock(data: Uint8Array): Promise<Uint8Array> {
		return RDCompress.decompressZstd(data, { force: true });
	}

	/**
	 * Handle transfer done
	 */
	_handleDone(done: FileTransferDone): void {
		const id = Number(done.id);
		const transfer = this._transfers.get(id);
		if (!transfer) return;
		if (transfer.type === 'download') {
			// The done follows the last block: it waits until every block before it is applied
			transfer.receiveChain = transfer.receiveChain.then(() => {
				if (this._transfers.get(id) === transfer && transfer.status !== 'error')
					this._finishTransfer(transfer);
			});
			return;
		}
		this._finishTransfer(transfer);
	}

	/** The device's done of a transfer: save a download, report the transfer complete */
	_finishTransfer(transfer: RDTransfer): void {
		const id = transfer.id;
		this._clearTransferTimeout(id);
		transfer.status = 'complete';
		const elapsed = (Date.now() - transfer.startTime) / 1000;
		const folderJobId = transfer.folderJobId || null;

		const finishComplete = (): void => {
			if (!folderJobId) {
				this._emit('file_transfer_complete', {
					id: id,
					fileName: transfer.fileName,
					fileSize: transfer.fileSize,
					type: transfer.type,
					elapsed: elapsed
				} satisfies RDFileTransferCompleteEvent);
			}

			if (typeof transfer._folderWait === 'function') {
				const resolve = transfer._folderWait;
				transfer._folderWait = null;
				resolve(true);
			}

			this._transfers.delete(id);

			if (transfer.type === 'upload' && !folderJobId) {
				this.browseDir(this._currentPath);
			}
		};

		if (transfer.type === 'download') {
			if (transfer.captureBlob) {
				try {
					const blob = new Blob(blobParts(transfer.blocks || []), { type: 'application/octet-stream' });
					if (typeof transfer.captureResolve === 'function') {
						transfer.captureResolve(blob);
					}
					transfer.captureResolve = null;
					transfer.captureReject = null;
				} catch (err) {
					if (typeof transfer.captureReject === 'function') transfer.captureReject(err);
				}
				finishComplete();
				return;
			}
			if (transfer.streamDownload && transfer.downloadHandle) {
				if (!folderJobId) {
					this._emit('file_transfer_progress', {
						id: id,
						fileName: transfer.fileName,
						fileSize: transfer.fileSize,
						transferred: transfer.receivedBytes,
						percent: 100,
						type: 'download',
						phase: 'saving'
					} satisfies RDFileTransferProgressEvent);
				}
				transfer.writeChain
					.then(() => {
						// Flush any pending batched chunks before finish.
						if (transfer._writeBatch && transfer._writeBatch.length) {
							const parts = transfer._writeBatch;
							transfer._writeBatch = [];
							transfer._writeBatchLen = 0;
							let total = 0;
							for (const part of parts) total += part.length;
							const merged = new Uint8Array(total);
							let off = 0;
							for (const part of parts) {
								merged.set(part, off);
								off += part.length;
							}
							return LocalFiles.writeDownload(transfer.downloadHandle, merged);
						}
						return null;
					})
					.then(() => {
						return LocalFiles.finishDownload(transfer.downloadHandle);
					})
					.then(() => {
						transfer.downloadHandle = null;
						finishComplete();
					})
					.catch((err: unknown) => {
						this._failTransfer(id, 'Failed to save file: ' + (errorMessage(err) || String(err)));
					});
				return;
			}
			if (!folderJobId) {
				this._emit('file_transfer_progress', {
					id: id,
					fileName: transfer.fileName,
					fileSize: transfer.fileSize,
					transferred: transfer.receivedBytes,
					percent: 100,
					type: 'download',
					phase: 'saving'
				} satisfies RDFileTransferProgressEvent);
			}
			this._triggerDownload(transfer);
		}

		finishComplete();
	}

	/**
	 * Handle transfer error
	 */
	_handleError(error: FileTransferError): void {
		const id = Number(error.id);
		const pendingCreate = this._pendingCreates.get(id);
		if (pendingCreate) {
			clearTimeout(pendingCreate.timer);
			this._pendingCreates.delete(id);
			// Remote mkdir failed — still resolve so upload can attempt file create,
			// unless the message clearly indicates a hard failure.
			const msg = String(error.error || '');
			if (/denied|not allowed|permission|no such/i.test(msg)) {
				pendingCreate.reject(new Error(msg || 'Could not create remote folder'));
			} else {
				pendingCreate.resolve();
			}
			return;
		}

		const transfer = this._transfers.get(id);
		const fileName = transfer ? transfer.fileName : 'unknown';

		if (transfer) {
			this._clearTransferTimeout(id);
			if (transfer.type === 'download' && typeof transfer.captureReject === 'function') {
				transfer.captureReject(new Error(error.error || 'Unknown error'));
				transfer.captureResolve = null;
				transfer.captureReject = null;
			}
			transfer.status = 'error';
			transfer.resumable =
				!transfer.folderJobId &&
				((transfer.type === 'download' ? transfer.receivedBytes : transfer.sentBytes) || 0) > 0;
			if (transfer.type === 'download' && transfer.downloadHandle) {
				LocalFiles.abortDownload(transfer.downloadHandle, !transfer.resumable).catch(() => {
					/* ignore */
				});
				if (!transfer.resumable) transfer.downloadHandle = null;
			}
			if (typeof transfer._folderWait === 'function') {
				const resolve = transfer._folderWait;
				transfer._folderWait = null;
				resolve(false);
			}
			if (!transfer.resumable) {
				this._transfers.delete(id);
			}
		}

		this._emit('file_transfer_error', {
			id: id,
			fileName: fileName,
			error: error.error || 'Unknown error',
			resumable: transfer ? transfer.resumable : false,
			type: transfer ? transfer.type : null,
			folderJobId: transfer ? transfer.folderJobId : null
		} satisfies RDFileTransferErrorEvent);
	}

	// ---- Upload block streaming ----

	async _tryCompressBlock(
		data: Uint8Array,
		fileName: string,
		transfer: RDUploadTransfer | null | undefined
	): Promise<{ data: Uint8Array; compressed: boolean }> {
		if (transfer && transfer.skipCompress) {
			return { data: data, compressed: false };
		}
		if (RDFileTransfer.isPreCompressedFileName(fileName)) {
			return { data: data, compressed: false };
		}
		const result = await RDCompress.compressZstd(data);
		if (transfer) {
			if (result.compress) {
				transfer._compressHits = (transfer._compressHits || 0) + 1;
				transfer._compressMisses = 0;
			} else {
				transfer._compressMisses = (transfer._compressMisses || 0) + 1;
				// After several non-beneficial blocks, skip zstd for the rest of the file.
				if (transfer._compressMisses >= 4 && !((transfer._compressHits || 0) > 0)) {
					transfer.skipCompress = true;
				}
			}
		}
		return { data: result.content, compressed: result.compress };
	}

	/**
	 * Stream file blocks for upload
	 */
	async _sendUploadBlocks(transfer: RDUploadTransfer, startOffset?: number): Promise<void> {
		const file = transfer.file;
		if (!file) return;

		const decodeChunkPayload = (payload: unknown): Uint8Array => {
			return LocalFiles.coerceBinaryPayload(payload);
		};

		const readSlice = async (offset: number, end: number): Promise<Uint8Array> => {
			if (file.__rdNativeHandle) {
				const length = end - offset;
				const invoke = tauriInvoke();
				const payload = await (invoke
					? invoke('desktop_read_file_chunk', {
							handle: file.__rdNativeHandle,
							offset: offset,
							length: length
						})
					: Promise.reject(new Error('Desktop bridge unavailable')));
				const bytes = decodeChunkPayload(payload);
				// Desktop returns base64; a missed decode produces length-0 and remote 0 KB shells.
				if (length > 0 && (!bytes || bytes.length === 0) && offset < file.size) {
					throw new Error(
						'Empty file chunk at offset ' + offset + ' (desktop IPC decode failed or file unreadable)'
					);
				}
				return bytes;
			}
			const slice = file.slice(offset, end);
			return new Uint8Array(await slice.arrayBuffer());
		};

		try {
			let offset = Math.max(0, Number(startOffset || 0));
			let blkId = RDFileTransfer.computeOffsetBlk(offset, this.BLOCK_SIZE);
			let lastProgressEmit = 0;

			while (offset < file.size && transfer.status === 'transferring') {
				const end = Math.min(offset + this.BLOCK_SIZE, file.size);
				const raw = await readSlice(offset, end);
				if (end - offset > 0 && (!raw || raw.length === 0)) {
					throw new Error('Upload read returned empty data at offset ' + offset);
				}
				const packed = await this._tryCompressBlock(raw, file.name, transfer);

				this._sendMessageSafe(
					this._proto.buildFileBlock(transfer.id, transfer.fileNum, packed.data, packed.compressed, blkId)
				);

				transfer.sentBytes = end;
				blkId++;
				offset = end;

				const now = Date.now();
				const forceProgress = end >= file.size;
				if (forceProgress || !lastProgressEmit || now - lastProgressEmit >= this.PROGRESS_EMIT_MS) {
					lastProgressEmit = now;
					const percent = Math.min(100, Math.round((end / file.size) * 100));
					if (
						transfer.folderJobId &&
						this._activeFolderJob &&
						this._activeFolderJob.id === transfer.folderJobId
					) {
						const job = this._activeFolderJob;
						this._emit('file_transfer_progress', {
							id: job.id,
							fileName: job.fileName,
							fileSize: job.fileSize,
							transferred: job.transferred + end,
							percent:
								job.fileSize > 0
									? Math.min(100, Math.round(((job.transferred + end) / job.fileSize) * 100))
									: percent,
							type: 'upload',
							isFolder: true,
							currentFile: job.currentFile,
							phase: 'transferring'
						} satisfies RDFileTransferProgressEvent);
					} else {
						this._emit('file_transfer_progress', {
							id: transfer.id,
							fileName: transfer.fileName,
							fileSize: transfer.fileSize,
							transferred: end,
							percent: percent,
							type: 'upload'
						} satisfies RDFileTransferProgressEvent);
					}
				}

				// Yield occasionally so video/input keep running — not every few blocks.
				if (blkId % 64 === 0) {
					await new Promise((r) => setTimeout(r, 0));
				}
			}

			// Send done
			if (transfer.status === 'transferring') {
				this._sendMessageSafe(this._proto.buildFileDone(transfer.id, transfer.fileNum));
			}

			const invoke = tauriInvoke();
			if (file.__rdNativeHandle && invoke) {
				invoke('desktop_release_file_handles', {
					handles: [file.__rdNativeHandle]
				}).catch(() => {
					/* ignore */
				});
			}
		} catch (err) {
			transfer.status = 'error';
			transfer.resumable =
				!transfer.folderJobId && (transfer.sentBytes || 0) > 0 && (transfer.sentBytes || 0) < file.size;
			if (typeof transfer._folderWait === 'function') {
				const resolve = transfer._folderWait;
				transfer._folderWait = null;
				resolve(false);
			}
			this._emit('file_transfer_error', {
				id: transfer.id,
				fileName: transfer.fileName,
				error: errorMessage(err) || 'Upload failed',
				resumable: transfer.resumable,
				type: 'upload',
				folderJobId: transfer.folderJobId || null
			} satisfies RDFileTransferErrorEvent);
			if (!transfer.resumable) {
				this._transfers.delete(transfer.id);
			}
		}
	}

	// ---- Browser download trigger ----

	/**
	 * Save a finished file: into the file sheet's local folder when its save
	 * hook takes it, else as a browser download. A failed save is reported as
	 * a file_transfer_error of the transfer and resolves false. A browser
	 * download that cannot start throws.
	 */
	_saveBlob(id: number, fileName: string, blob: Blob): Promise<boolean> {
		const finishBrowserDownload = (): void => {
			const url = URL.createObjectURL(blob);
			const a = document.createElement('a');
			a.href = url;
			a.download = fileName;
			a.style.display = 'none';
			document.body.appendChild(a);
			a.click();
			setTimeout(() => {
				URL.revokeObjectURL(url);
				a.remove();
			}, 5000);
		};

		if (typeof this._saveDownload === 'function') {
			return Promise.resolve(this._saveDownload(fileName, blob))
				.then((savedToFolder) => {
					if (!savedToFolder) finishBrowserDownload();
					return true;
				})
				.catch((err: unknown) => {
					this._emit('file_transfer_error', {
						id: id,
						fileName: fileName,
						error: 'Failed to save file: ' + (errorMessage(err) || 'unknown error')
					} satisfies RDFileTransferErrorEvent);
					return false;
				});
		}
		finishBrowserDownload();
		return Promise.resolve(true);
	}

	/**
	 * Assemble received blocks into a Blob and trigger download
	 */
	_triggerDownload(transfer: RDDownloadTransfer): void {
		if (transfer.streamDownload) return;
		const blocks = transfer.blocks || [];

		try {
			const blob = new Blob(blobParts(blocks), { type: 'application/octet-stream' });
			void this._saveBlob(transfer.id, transfer.fileName, blob);
		} catch (err) {
			this._emit('file_transfer_error', {
				id: transfer.id,
				fileName: transfer.fileName,
				error: 'Failed to save file: ' + (errorMessage(err) || 'unknown error')
			} satisfies RDFileTransferErrorEvent);
		}
	}

	// ---- Utility ----

	/**
	 * Format file size for display
	 */
	static formatSize(bytes: number): string {
		if (bytes === 0) return '0 B';
		const units = ['B', 'KB', 'MB', 'GB', 'TB'];
		const i = Math.floor(Math.log(bytes) / Math.log(1024));
		return (bytes / Math.pow(1024, i)).toFixed(i > 0 ? 1 : 0) + ' ' + units[i];
	}

	/**
	 * Format timestamp to locale string
	 * @param ts - Unix timestamp in seconds
	 */
	static formatTime(ts: number | null | undefined): string {
		if (!ts) return '';
		return new Date(ts * 1000).toLocaleString();
	}

	/**
	 * Get icon name for file entry type
	 * @returns Material Symbols name
	 */
	static getFileIcon(entry: { isDir?: boolean; entryType?: number; name?: string | null }): string {
		if (entry.isDir) {
			if (entry.entryType === 3) return 'storage'; // Drive
			return 'folder';
		}
		const ext = ((entry.name || '').split('.').pop() ?? '').toLowerCase();
		const imageExts = ['jpg', 'jpeg', 'png', 'gif', 'bmp', 'webp', 'svg', 'ico'];
		const videoExts = ['mp4', 'avi', 'mkv', 'mov', 'wmv', 'flv', 'webm'];
		const audioExts = ['mp3', 'wav', 'ogg', 'flac', 'aac', 'wma', 'm4a'];
		const docExts = ['pdf', 'doc', 'docx', 'xls', 'xlsx', 'ppt', 'pptx', 'txt', 'rtf', 'csv'];
		const codeExts = [
			'js',
			'ts',
			'py',
			'go',
			'rs',
			'java',
			'c',
			'cpp',
			'h',
			'css',
			'html',
			'json',
			'xml',
			'yml',
			'yaml',
			'toml',
			'sh',
			'bat',
			'ps1'
		];
		const archiveExts = ['zip', 'tar', 'gz', 'rar', '7z', 'bz2', 'xz'];

		if (imageExts.includes(ext)) return 'image';
		if (videoExts.includes(ext)) return 'movie';
		if (audioExts.includes(ext)) return 'music_note';
		if (docExts.includes(ext)) return 'description';
		if (codeExts.includes(ext)) return 'code';
		if (archiveExts.includes(ext)) return 'archive';
		if (ext === 'exe' || ext === 'msi') return 'apps';
		return 'insert_drive_file';
	}

	/**
	 * Get transfer statistics
	 */
	getStats(): RDFileTransferStats {
		const active: RDFileTransferStat[] = [];
		for (const [, t] of this._transfers) {
			const transferred = t.type === 'download' ? t.receivedBytes : t.sentBytes || 0;
			const elapsed = (Date.now() - t.startTime) / 1000;
			active.push({
				id: t.id,
				type: t.type,
				fileName: t.fileName,
				fileSize: t.fileSize,
				transferred: transferred,
				percent: t.fileSize > 0 ? Math.round((transferred / t.fileSize) * 100) : 0,
				speed: elapsed > 0 ? transferred / elapsed : 0,
				status: t.status
			});
		}
		return { active: active, count: active.length };
	}
}
