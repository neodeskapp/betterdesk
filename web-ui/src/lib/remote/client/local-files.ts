/**
 * Local filesystem helpers for Web Remote file transfer modal.
 * Browser: File System Access API when available (Chrome/Edge + HTTPS).
 * RdClient desktop: native folder picker + path-based reads via Tauri.
 */
import { page, tauriInvoke } from './page.ts';

/** The File System Access folder picker (Chrome and Edge); TypeScript's DOM lib does not have it */
interface DirectoryPickerWindow {
	showDirectoryPicker?: (options?: { mode?: 'read' | 'readwrite' }) => Promise<FileSystemDirectoryHandle>;
}

interface LocalEntryBase {
	name: string;
	/** RdClient desktop: the entry's absolute path */
	path?: string;
	size: number;
	modifiedTime: number;
}

/** A folder in the local list: a desktop path or a File System Access handle */
export interface LocalDirEntry extends LocalEntryBase {
	isDir: true;
	handle?: FileSystemDirectoryHandle;
}

/** A file in the local list: a desktop path or a File System Access handle */
export interface LocalFileEntry extends LocalEntryBase {
	isDir: false;
	handle?: FileSystemFileHandle;
}

export type LocalEntry = LocalDirEntry | LocalFileEntry;

/** A folder inside a walked local folder */
export interface LocalWalkDir {
	name: string;
	path?: string;
	/** Below the walked folder, with `/` separators */
	relativePath: string;
	isDir: true;
	handle?: FileSystemDirectoryHandle;
}

/** A file inside a walked local folder */
export interface LocalWalkFile {
	name: string;
	path?: string;
	/** Below the walked folder, with `/` separators */
	relativePath: string;
	size: number;
	modifiedTime: number;
	isDir: false;
	/** File System Access: the file, read during the walk */
	file?: File;
	handle?: FileSystemFileHandle;
}

/** A local folder tree, folders sorted parents first (what RDFileTransfer.uploadFolder takes) */
export interface LocalWalk {
	rootName: string;
	dirs: LocalWalkDir[];
	files: LocalWalkFile[];
}

/** A walked desktop path, which may also be a single file */
export interface LocalPathWalk extends LocalWalk {
	isFile: boolean;
}

/** A part of a NativeUploadFile, read from the desktop shell on demand */
export interface NativeUploadSlice {
	arrayBuffer(): Promise<ArrayBuffer>;
}

/** A File-like object backed by a native read handle of the desktop shell */
export interface NativeUploadFile {
	name: string;
	size: number;
	lastModified: number;
	/** The desktop shell's read handle, as it gave it */
	__rdNativeHandle: unknown;
	__rdNativePath: unknown;
	slice(start?: number, end?: number): NativeUploadSlice;
}

/** LocalFiles.beginDownload: where the desktop shell writes a streamed download */
export interface LocalDownloadBeginOptions {
	suggestedName?: string | null;
	defaultDir?: string | null;
	absolutePath?: string | null;
	appendOffset?: number | null;
}

/** A level above the current folder in File System Access mode */
interface FsaStackEntry {
	handle: FileSystemDirectoryHandle | null;
	path: string;
}

function pickerWindow(): Window & DirectoryPickerWindow {
	return window;
}

function isFsAccessSupported(): boolean {
	return typeof pickerWindow().showDirectoryPicker === 'function' && window.isSecureContext === true;
}

function showDirectoryPicker(options: { mode?: 'read' | 'readwrite' }): Promise<FileSystemDirectoryHandle> {
	const picker = pickerWindow().showDirectoryPicker;
	if (typeof picker !== 'function') throw new TypeError('showDirectoryPicker is not a function');
	return picker.call(window, options);
}

function isDesktopBridge(): boolean {
	return page().__NEODESK_RDCLIENT_DESKTOP__ === true && tauriInvoke() !== null;
}

function desktopInvoke(cmd: string, args?: Record<string, unknown>): Promise<unknown> {
	const invoke = tauriInvoke();
	if (!invoke) throw new TypeError('No desktop bridge');
	return invoke(cmd, args || {});
}

/** A field of an answer of the desktop shell (undefined when the answer is no object) */
function answerField(answer: unknown, key: string): unknown {
	return typeof answer === 'object' && answer !== null ? Reflect.get(answer, key) : undefined;
}

/** A text field of a desktop answer ('' when it is missing) */
function answerText(answer: unknown, key: string): string {
	const value = answerField(answer, key);
	return value == null ? '' : String(value);
}

/** A path field of a desktop answer (undefined when it is missing) */
function answerPath(answer: unknown, key: string): string | undefined {
	const value = answerField(answer, key);
	return value == null ? undefined : String(value);
}

/** A list answer of the desktop shell; no answer is an empty list */
function answerList(answer: unknown): unknown[] {
	if (!answer) return [];
	if (!Array.isArray(answer)) throw new TypeError('The desktop shell did not answer with a list');
	return answer;
}

/** TypedArray views and other array-likes; new Uint8Array turns their items into bytes */
function isArrayLike(value: object): value is ArrayLike<number> {
	return 'length' in value && value.length != null;
}

/** The bytes on an ArrayBuffer, as ArrayBuffer.slice takes them (bytes on other memory go as a copy) */
function bufferBytes(data: Uint8Array): Uint8Array<ArrayBuffer> {
	return isOnArrayBuffer(data) ? data : new Uint8Array(data);
}

function isOnArrayBuffer(data: Uint8Array): data is Uint8Array<ArrayBuffer> {
	return data.buffer instanceof ArrayBuffer;
}

/**
 * Coerce IPC / File payloads to Uint8Array.
 *
 * CRITICAL: never use `new Uint8Array(string)`. In JS that does
 * `ToIndex(string)` → NaN → length 0, so base64 chunks become empty
 * buffers and remote files land as 0 KB shells (Cliprdr + File transfer).
 *
 * Desktop Tauri commands return standard base64 strings for file chunks;
 * older builds returned JSON number arrays.
 */
function coerceBinaryPayload(payload: unknown): Uint8Array {
	if (payload == null || payload === '') return new Uint8Array(0);
	if (payload instanceof Uint8Array) return payload;
	if (payload instanceof ArrayBuffer) return new Uint8Array(payload);
	if (Array.isArray(payload)) return new Uint8Array(payload);
	if (typeof payload === 'string') {
		try {
			const binary = atob(payload);
			const out = new Uint8Array(binary.length);
			for (let i = 0; i < binary.length; i++) out[i] = binary.charCodeAt(i);
			return out;
		} catch {
			throw new Error('Invalid base64 file chunk from desktop bridge');
		}
	}
	// TypedArray views / Buffer-like (must not be a string — handled above).
	if (
		typeof payload === 'object' &&
		isArrayLike(payload) &&
		typeof payload !== 'string' &&
		!(payload instanceof String)
	) {
		return new Uint8Array(payload);
	}
	return new Uint8Array(0);
}

/**
 * Encode bytes as standard base64 (chunked to avoid call stack limits).
 */
function bytesToBase64(data: unknown): string {
	const bytes = coerceBinaryPayload(data);
	let binary = '';
	const chunk = 0x8000;
	for (let i = 0; i < bytes.length; i += chunk) {
		binary += String.fromCharCode(...bytes.subarray(i, i + chunk));
	}
	return btoa(binary);
}

function base64ToBytes(b64: unknown): Uint8Array {
	return coerceBinaryPayload(b64);
}

/**
 * Build a duck-typed File-like object backed by a native read handle.
 * @param info - { handle, name, size, modifiedTime|modified_time }
 */
function createNativeUploadFile(info: unknown): NativeUploadFile | null {
	if (!info || !answerField(info, 'handle')) return null;
	const modified =
		answerField(info, 'modifiedTime') != null
			? answerField(info, 'modifiedTime')
			: answerField(info, 'modified_time') != null
				? Number(answerField(info, 'modified_time')) * 1000
				: 0;
	return {
		name: String(answerField(info, 'name') || 'file'),
		size: Number(answerField(info, 'size') || 0),
		lastModified: Number(modified),
		__rdNativeHandle: answerField(info, 'handle'),
		__rdNativePath: answerField(info, 'path') || null,
		slice(start?: number, end?: number): NativeUploadSlice {
			const handle = answerField(info, 'handle');
			const offset = Math.max(0, Number(start || 0));
			const length = Math.max(0, Number(end || 0) - offset);
			return {
				arrayBuffer(): Promise<ArrayBuffer> {
					return desktopInvoke('desktop_read_file_chunk', {
						handle: handle,
						offset: offset,
						length: length
					}).then((payload) => {
						const bytes = bufferBytes(base64ToBytes(payload));
						return bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength);
					});
				}
			};
		}
	};
}

function byRelativePath(a: { relativePath: string }, b: { relativePath: string }): number {
	return a.relativePath.length - b.relativePath.length || a.relativePath.localeCompare(b.relativePath);
}

export class LocalFiles {
	_mode: 'fsa' | 'desktop' | null;
	_rootHandle: FileSystemDirectoryHandle | null;
	_currentHandle: FileSystemDirectoryHandle | null;
	/** The levels above the current folder: paths (desktop) or handles (File System Access) */
	_pathStack: (string | FsaStackEntry)[];
	_rootPath: string;
	_currentPath: string;

	constructor() {
		this._mode = null; // 'fsa' | 'desktop'
		this._rootHandle = null;
		this._currentHandle = null;
		this._pathStack = [];
		this._rootPath = '';
		this._currentPath = '';
	}

	static isSupported(): boolean {
		return isFsAccessSupported() || isDesktopBridge();
	}

	static isDesktopBridge = isDesktopBridge;
	static createNativeUploadFile = createNativeUploadFile;
	static bytesToBase64 = bytesToBase64;
	static base64ToBytes = base64ToBytes;
	static coerceBinaryPayload = coerceBinaryPayload;

	get currentPath(): string {
		return this._currentPath;
	}

	get hasRoot(): boolean {
		return this._mode === 'fsa' ? !!this._rootHandle : !!this._rootPath;
	}

	async pickRoot(): Promise<string> {
		if (isDesktopBridge()) {
			const folder = await desktopInvoke('desktop_pick_folder');
			const folderPath = answerField(folder, 'path');
			if (!folder || !folderPath) {
				throw new Error('AbortError');
			}
			this._mode = 'desktop';
			this._rootPath = String(folderPath);
			this._currentPath = String(folderPath);
			this._pathStack = [];
			return this._currentPath;
		}
		if (!isFsAccessSupported()) {
			throw new Error('unsupported');
		}
		this._mode = 'fsa';
		this._rootHandle = await showDirectoryPicker({ mode: 'readwrite' });
		this._currentHandle = this._rootHandle;
		this._pathStack = [];
		this._currentPath = this._rootHandle.name || '';
		return this._currentPath;
	}

	async listCurrent(): Promise<LocalEntry[]> {
		if (this._mode === 'desktop') {
			if (!this._currentPath) return [];
			const desktopEntries = await desktopInvoke('desktop_list_directory', { path: this._currentPath });
			return answerList(desktopEntries).map((e): LocalEntry => ({
				name: answerText(e, 'name'),
				path: answerPath(e, 'path'),
				isDir: !!answerField(e, 'isDir') || !!answerField(e, 'is_dir'),
				size: Number(answerField(e, 'size') || 0),
				modifiedTime: Number(
					answerField(e, 'modifiedTime') != null
						? answerField(e, 'modifiedTime')
						: answerField(e, 'modified_time') || 0
				)
			}));
		}
		if (!this._currentHandle) return [];
		const entries: LocalEntry[] = [];
		for await (const entry of this._currentHandle.values()) {
			entries.push(
				entry.kind === 'directory'
					? { name: entry.name, isDir: true, size: 0, modifiedTime: 0, handle: entry }
					: { name: entry.name, isDir: false, size: 0, modifiedTime: 0, handle: entry }
			);
		}
		entries.sort((a, b) => {
			if (a.isDir && !b.isDir) return -1;
			if (!a.isDir && b.isDir) return 1;
			return a.name.localeCompare(b.name, undefined, { sensitivity: 'base' });
		});
		for (const e of entries) {
			if (!e.isDir && e.handle) {
				try {
					const file = await e.handle.getFile();
					e.size = file.size;
					e.modifiedTime = file.lastModified ? Math.floor(file.lastModified / 1000) : 0;
				} catch {
					/* ignore */
				}
			}
		}
		return entries;
	}

	async enterDir(entry: LocalEntry | null | undefined): Promise<string | undefined> {
		if (this._mode === 'desktop') {
			if (!entry || !entry.isDir || !entry.path) return;
			this._pathStack.push(this._currentPath);
			this._currentPath = entry.path;
			return this._currentPath;
		}
		if (!entry || !entry.isDir || !entry.handle) return;
		this._pathStack.push({ handle: this._currentHandle, path: this._currentPath });
		this._currentHandle = entry.handle;
		this._currentPath = this._currentPath + (this._currentPath.endsWith('/') ? '' : '/') + entry.name;
		return this._currentPath;
	}

	async goUp(): Promise<string> {
		if (this._mode === 'desktop') {
			if (!this._pathStack.length) return this._currentPath;
			const prevPath = this._pathStack.pop();
			if (typeof prevPath === 'string') this._currentPath = prevPath;
			return this._currentPath;
		}
		if (!this._pathStack.length) return this._currentPath;
		const prev = this._pathStack.pop();
		if (prev && typeof prev !== 'string') {
			this._currentHandle = prev.handle;
			this._currentPath = prev.path;
		}
		return this._currentPath;
	}

	async goHome(): Promise<string> {
		if (this._mode === 'desktop') {
			if (!this._rootPath) return '';
			this._currentPath = this._rootPath;
			this._pathStack = [];
			return this._currentPath;
		}
		if (!this._rootHandle) return '';
		this._currentHandle = this._rootHandle;
		this._pathStack = [];
		this._currentPath = this._rootHandle.name || '';
		return this._currentPath;
	}

	async readFile(entry: LocalEntry | null | undefined): Promise<File | NativeUploadFile | null> {
		if (this._mode === 'desktop') {
			if (!entry || entry.isDir) return null;
			const path = entry.path;
			if (!path) return null;
			const info = await desktopInvoke('desktop_open_file', { path: path });
			return createNativeUploadFile(info);
		}
		if (!entry || entry.isDir || !entry.handle) return null;
		return entry.handle.getFile();
	}

	/**
	 * Recursively expand a local directory (desktop path or FSA handle).
	 */
	async walkFolder(entry: LocalEntry | null | undefined): Promise<LocalWalk> {
		if (!entry || !entry.isDir) {
			return { rootName: '', dirs: [], files: [] };
		}
		if (this._mode === 'desktop' || (isDesktopBridge() && entry.path)) {
			const walked = await desktopInvoke('desktop_walk_paths', { path: entry.path });
			const dirs: LocalWalkDir[] = [];
			const files: LocalWalkFile[] = [];
			let rootName = entry.name || '';
			answerList(walked).forEach((e) => {
				const rel = String(
					answerField(e, 'relativePath') != null
						? answerField(e, 'relativePath')
						: answerField(e, 'relative_path') || ''
				);
				const isDir = !!(answerField(e, 'isDir') || answerField(e, 'is_dir'));
				if (rel === '') {
					rootName = answerText(e, 'name') || rootName;
					return;
				}
				if (isDir) {
					dirs.push({
						name: answerText(e, 'name'),
						path: answerPath(e, 'path'),
						relativePath: rel,
						isDir: true
					});
				} else {
					files.push({
						name: answerText(e, 'name'),
						path: answerPath(e, 'path'),
						relativePath: rel,
						size: Number(answerField(e, 'size') || 0),
						modifiedTime: Number(
							answerField(e, 'modifiedTime') != null
								? answerField(e, 'modifiedTime')
								: answerField(e, 'modified_time') || 0
						),
						isDir: false
					});
				}
			});
			dirs.sort(byRelativePath);
			return { rootName: rootName, dirs: dirs, files: files };
		}

		// File System Access API walk
		const rootNameFsa = entry.name || 'folder';
		const dirsFsa: LocalWalkDir[] = [];
		const filesFsa: LocalWalkFile[] = [];
		async function walkHandle(dirHandle: FileSystemDirectoryHandle, relPrefix: string): Promise<void> {
			for await (const child of dirHandle.values()) {
				const rel = relPrefix ? relPrefix + '/' + child.name : child.name;
				if (child.kind === 'directory') {
					dirsFsa.push({ name: child.name, relativePath: rel, isDir: true, handle: child });
					await walkHandle(child, rel);
				} else {
					const file = await child.getFile();
					filesFsa.push({
						name: child.name,
						relativePath: rel,
						size: file.size,
						modifiedTime: file.lastModified ? Math.floor(file.lastModified / 1000) : 0,
						isDir: false,
						file: file,
						handle: child
					});
				}
			}
		}
		// A folder entry of the File System Access list always has its handle
		if (!entry.handle) throw new TypeError('No folder handle to walk');
		await walkHandle(entry.handle, '');
		dirsFsa.sort(byRelativePath);
		return { rootName: rootNameFsa, dirs: dirsFsa, files: filesFsa };
	}

	/** Walk an absolute desktop path (folder drop / pick). */
	static async walkDesktopPath(path: string): Promise<LocalPathWalk> {
		if (!isDesktopBridge() || !path) {
			return { rootName: '', dirs: [], files: [], isFile: false };
		}
		const walked = answerList(await desktopInvoke('desktop_walk_paths', { path: path }));
		const dirs: LocalWalkDir[] = [];
		const files: LocalWalkFile[] = [];
		let rootName = '';
		let isFile = false;
		walked.forEach((e) => {
			const rel = String(
				answerField(e, 'relativePath') != null
					? answerField(e, 'relativePath')
					: answerField(e, 'relative_path') || ''
			);
			const isDir = !!(answerField(e, 'isDir') || answerField(e, 'is_dir'));
			if (rel === '' && isDir) {
				rootName = answerText(e, 'name') || rootName;
				return;
			}
			if (isDir) {
				dirs.push({
					name: answerText(e, 'name'),
					path: answerPath(e, 'path'),
					relativePath: rel,
					isDir: true
				});
			} else {
				if (walked.length === 1) {
					isFile = true;
					rootName = answerText(e, 'name') || rootName;
				}
				files.push({
					name: answerText(e, 'name'),
					path: answerPath(e, 'path'),
					relativePath: rel || answerText(e, 'name'),
					size: Number(answerField(e, 'size') || 0),
					modifiedTime: Number(
						answerField(e, 'modifiedTime') != null
							? answerField(e, 'modifiedTime')
							: answerField(e, 'modified_time') || 0
					),
					isDir: false
				});
			}
		});
		if (!rootName && files.length === 1 && dirs.length === 0) {
			isFile = true;
			rootName = files[0]?.name ?? '';
		}
		dirs.sort(byRelativePath);
		return { rootName: rootName, dirs: dirs, files: files, isFile: isFile };
	}

	async saveDownload(fileName: string, blob: Blob): Promise<boolean> {
		if (isDesktopBridge()) {
			const buf = await blob.arrayBuffer();
			const data = Array.from(new Uint8Array(buf));
			const result = await desktopInvoke('desktop_save_download', {
				file_name: fileName,
				data: data
			});
			return !!(result && answerField(result, 'saved'));
		}
		if (!this._currentHandle || !isFsAccessSupported()) return false;
		try {
			const fh = await this._currentHandle.getFileHandle(fileName, { create: true });
			const w = await fh.createWritable();
			await w.write(blob);
			await w.close();
			return true;
		} catch (e) {
			console.warn('[LocalFiles] save to folder failed:', e);
			return false;
		}
	}

	async mkdirp(path: string): Promise<boolean> {
		if (isDesktopBridge()) {
			await desktopInvoke('desktop_mkdir_p', { path: path });
			return true;
		}
		return false;
	}

	/**
	 * Streaming download helpers (desktop only). beginDownload answers with
	 * the desktop shell's { started, handle, path }, or null in a browser.
	 */
	static async beginDownload(opts?: LocalDownloadBeginOptions | null): Promise<unknown> {
		if (!isDesktopBridge()) return null;
		const args = {
			suggestedName: opts && opts.suggestedName != null ? opts.suggestedName : null,
			defaultDir: opts && opts.defaultDir != null ? opts.defaultDir : null,
			absolutePath: opts && opts.absolutePath != null ? opts.absolutePath : null,
			appendOffset: opts && opts.appendOffset != null ? Number(opts.appendOffset) : 0
		};
		// Tauri rename_all: camelCase in JS maps to snake in some versions; pass both styles via camelCase (serde rename).
		return desktopInvoke('desktop_download_begin', {
			suggestedName: args.suggestedName,
			defaultDir: args.defaultDir,
			absolutePath: args.absolutePath,
			appendOffset: args.appendOffset
		});
	}

	static async writeDownload(handle: unknown, data: unknown): Promise<void> {
		if (!isDesktopBridge() || !handle) return;
		const bytes = coerceBinaryPayload(data);
		if (!bytes.length) return;
		await desktopInvoke('desktop_download_write', {
			handle: handle,
			dataBase64: bytesToBase64(bytes)
		});
	}

	/** The desktop shell's answer ({ saved, ... }), or { saved: false } in a browser */
	static async finishDownload(handle: unknown): Promise<unknown> {
		if (!isDesktopBridge() || !handle) return { saved: false };
		return desktopInvoke('desktop_download_finish', { handle: handle });
	}

	static async abortDownload(handle: unknown, deleteFile?: boolean): Promise<void> {
		if (!isDesktopBridge() || !handle) return;
		await desktopInvoke('desktop_download_abort', {
			handle: handle,
			deleteFile: deleteFile !== false
		});
	}

	/** The desktop shell's answer ({ path } of the picked folder), or null in a browser */
	static async pickFolder(): Promise<unknown> {
		if (!isDesktopBridge()) return null;
		return desktopInvoke('desktop_pick_folder');
	}

	/** Pick one or more local files via native dialog (desktop only). */
	static async pickNativeFiles(): Promise<NativeUploadFile[]> {
		if (!isDesktopBridge()) return [];
		const picked = await desktopInvoke('desktop_pick_files');
		return answerList(picked)
			.map(createNativeUploadFile)
			.filter((file): file is NativeUploadFile => Boolean(file));
	}
}
