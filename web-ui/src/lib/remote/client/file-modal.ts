/**
 * RustDesk-style centered file transfer modal for Web Remote.
 */
import { RDDesktopDnd } from './desktop-dnd.ts';
import { RDFileTransfer } from './filetransfer.ts';
import { LocalFiles, type LocalEntry, type LocalWalk, type NativeUploadFile } from './local-files.ts';
import { page, tauriInvoke } from './page.ts';

/** A file to upload: one the browser gave, or one the desktop shell opened */
export type FileSheetUpload = File | NativeUploadFile;

/** A remote directory entry, as the file transfers list them (file_dir) */
export interface FileSheetEntry {
	name: string;
	isDir: boolean;
	size: number;
	/** 3 is a drive (RDFileTransfer.getFileIcon) */
	entryType?: number;
}

/**
 * The file transfer of a session as the sheet drives it: RDFileTransfer
 * (RDClient), CDAPFileTransfer (CDAPSession) or MeshFileTransfer
 * (MeshSession). The optional members are the ones CDAP or mesh lack.
 */
export interface FileSheetTransfer {
	currentPath: string;
	enabled: boolean;
	showHidden?: boolean;
	enable?(): void;
	browseDir(path: string): void;
	browseParent(): void;
	setShowHidden?(show: boolean): void;
	createDir?(path: string): void;
	rename?(path: string, newName: string): void;
	removeDir?(path: string, recursive: boolean): void;
	removeFile?(path: string): void;
	confirmOverwrite?(id: number, skip: boolean, applyToAll: boolean): void;
	uploadFile(file: FileSheetUpload, remotePath: string): unknown;
	uploadFolder?(walk: LocalWalk, remoteDest: string): unknown;
	downloadFile(remotePath: string, entry: FileSheetEntry): unknown;
	downloadFolder?(remotePath: string, entry: FileSheetEntry, localRoot: string): unknown;
	/** The browser: the folder as one ZIP archive (RustDesk sessions) */
	downloadFolderAsZip?(remotePath: string, entry: FileSheetEntry): unknown;
	cancelTransfer(id: number): unknown;
	resumeTransfer?(id: number): void;
	/** Saves a finished download into the chosen local folder; null leaves it to the browser */
	_saveDownload?: ((fileName: string, blob: Blob) => unknown) | null;
}

/** The session's client: an RDClient, a CDAPSession or a MeshSession */
export interface FileSheetClient {
	fileTransfer?: FileSheetTransfer | null;
	on(event: string, fn: (...args: never[]) => void): unknown;
	/** The second factor the file transfer session asked for (RDClient) */
	submitFile2FA?(code: string): void;
}

/** What the sheet opens on: the viewer's session of one device */
export interface FileSheetSession {
	deviceId: string;
	client?: FileSheetClient | null;
}

/** file_dir: the remote directory the transfer listed */
interface FileSheetDirEvent {
	path?: string;
	entries?: FileSheetEntry[];
}

/** file_connect_error */
interface FileSheetConnectErrorEvent {
	error?: string;
}

/** file_transfer_*: one transfer or folder job (the fields the sheet reads) */
export interface FileSheetTransferEvent {
	id: number;
	type?: string;
	fileName?: string;
	fileSize?: number;
	/** Set on the files of a folder job, which get no row of their own */
	folderJobId?: number | null;
	isFolder?: boolean;
	currentFile?: string | null;
	phase?: string;
	percent?: number;
	transferred?: number;
	error?: string;
	resumable?: boolean;
}

/** A row of the transfer queue */
export interface FileSheetTransferMeta {
	id: number;
	fileName: string;
	type: string;
	/** pending, transferring, saving, done or error */
	phase: string;
	percent: number;
	transferred: number;
	total: number;
	error: string | null;
	resumable: boolean;
	isFolder: boolean;
	currentFile: string | null;
}

function t(key: string, fallback?: string): string {
	const w = page();
	if (typeof w.t === 'function') {
		const val = w.t(key);
		if (val && val !== key) return val;
	}
	return fallback !== undefined ? fallback : key;
}

function escapeHtml(s: unknown): string {
	return String(s || '')
		.replace(/&/g, '&amp;')
		.replace(/</g, '&lt;')
		.replace(/>/g, '&gt;')
		.replace(/"/g, '&quot;');
}

function formatSize(bytes: number): string {
	return RDFileTransfer.formatSize(bytes);
}

/** A field of a caught value (undefined when it has none) */
function errorField(err: unknown, key: 'name' | 'message'): unknown {
	return typeof err === 'object' && err !== null ? Reflect.get(err, key) : undefined;
}

/**
 * An element of the sheet's own markup. It is always there; the code reads
 * it without a check, so a missing one throws.
 */
function part<E extends Element = HTMLElement>(root: ParentNode | null, selector: string): E {
	const node = root?.querySelector<E>(selector);
	if (!node) throw new TypeError('The file sheet has no ' + selector);
	return node;
}

/**
 * Mount menus inside the viewer shell so they stay visible when
 * #rd-viewer-shell is the Fullscreen API element (body children are hidden).
 */
function getOverlayHost(): Element {
	return document.fullscreenElement || document.getElementById('rd-viewer-shell') || document.body;
}

/** The file panel is a side sheet beside the remote screen (#rd-sheet-host). */
function getSheetHost(): Element {
	return document.getElementById('rd-sheet-host') || getOverlayHost();
}

export class FileTransferModal {
	_el: HTMLDivElement | null;
	_open: boolean;
	_session: FileSheetSession | null;
	_local: LocalFiles;
	_localEntries: LocalEntry[];
	_remoteEntries: FileSheetEntry[];
	_selectedLocal: LocalEntry | null;
	_selectedRemote: FileSheetEntry | null;
	_transferMeta: Map<number, FileSheetTransferMeta>;
	_wiredSessionId: string | null;
	_remoteReady: boolean;
	_pendingNativePaths: string[] | null;
	_contextMenu: HTMLDivElement | null;
	_queuePaintTimer: ReturnType<typeof setTimeout> | null;
	_queuePaintPending: boolean;

	constructor() {
		this._el = null;
		this._open = false;
		this._session = null;
		this._local = new LocalFiles();
		this._localEntries = [];
		this._remoteEntries = [];
		this._selectedLocal = null;
		this._selectedRemote = null;
		this._transferMeta = new Map();
		this._wiredSessionId = null;
		this._remoteReady = false;
		this._pendingNativePaths = null;
		this._contextMenu = null;
		this._queuePaintTimer = null;
		this._queuePaintPending = false;
	}

	_ensureDom(): void {
		if (this._el) return;
		const el = document.createElement('div');
		el.className = 'file-transfer-modal-overlay';
		el.id = 'file-transfer-modal';
		el.style.display = 'none';
		el.innerHTML =
			'<div class="file-transfer-modal" role="dialog" aria-labelledby="ft-modal-title">' +
			'<div class="ft-modal-header">' +
			'<h2 class="ft-modal-title" id="ft-modal-title"></h2>' +
			'<button type="button" class="ft-modal-close modal-close"><span class="material-icons" aria-hidden="true">close</span></button>' +
			'</div>' +
			'<div class="ft-modal-body">' +
			'<div class="ft-pane ft-pane-local">' +
			'<div class="ft-pane-head"><span class="ft-pane-label"></span></div>' +
			'<div class="ft-pane-toolbar">' +
			'<button type="button" class="btn btn-sm ft-btn ft-local-pick"></button>' +
			'<button type="button" class="btn btn-sm ft-btn ft-local-up"></button>' +
			'<button type="button" class="btn btn-sm ft-btn ft-local-home"></button>' +
			'<span class="ft-path ft-local-path"></span>' +
			'<button type="button" class="btn btn-sm ft-btn ft-btn-action ft-local-send"></button>' +
			'</div>' +
			'<div class="ft-local-body">' +
			'<div class="ft-local-dropzone ft-dropzone-disabled" tabindex="0" role="button">' +
			'<span class="ft-dropzone-title"></span>' +
			'<span class="ft-dropzone-subtitle"></span>' +
			'<span class="ft-dropzone-multiple"></span>' +
			'<span class="ft-dropzone-target"></span>' +
			'</div>' +
			'<div class="ft-list ft-local-list" hidden></div>' +
			'</div>' +
			'<input type="file" class="ft-local-file-input" multiple hidden />' +
			'</div>' +
			'<div class="ft-pane ft-pane-remote">' +
			'<div class="ft-pane-head"><span class="ft-pane-label"></span></div>' +
			'<div class="ft-pane-toolbar">' +
			'<button type="button" class="btn btn-sm ft-btn ft-remote-up"></button>' +
			'<button type="button" class="btn btn-sm ft-btn ft-remote-home"></button>' +
			'<button type="button" class="btn btn-sm ft-btn ft-remote-new-folder"></button>' +
			'<button type="button" class="btn btn-sm ft-btn ft-remote-hidden"></button>' +
			'<span class="ft-path ft-remote-path"></span>' +
			'<button type="button" class="btn btn-sm ft-btn ft-btn-action ft-remote-download"></button>' +
			'</div>' +
			'<div class="ft-list ft-remote-list"></div>' +
			'<div class="ft-drop-overlay" hidden>' +
			'<span class="ft-drop-overlay-text"></span>' +
			'</div>' +
			'</div>' +
			'<div class="ft-pane ft-pane-queue">' +
			'<div class="ft-pane-head"><span class="ft-pane-label"></span></div>' +
			'<div class="ft-queue-list"></div>' +
			'<div class="ft-queue-empty"><span></span></div>' +
			'</div>' +
			'</div>' +
			'<div class="ft-overwrite-dialog" hidden role="alertdialog" aria-modal="true">' +
			'<div class="ft-overwrite-panel">' +
			'<div class="ft-overwrite-title"></div>' +
			'<div class="ft-overwrite-message"></div>' +
			'<div class="ft-overwrite-actions">' +
			'<button type="button" class="btn btn-sm ft-overwrite-skip"></button>' +
			'<button type="button" class="btn btn-sm ft-overwrite-skip-all"></button>' +
			'<button type="button" class="btn btn-sm ft-overwrite-all"></button>' +
			'<button type="button" class="btn btn-sm btn-primary ft-overwrite-confirm"></button>' +
			'</div>' +
			'</div>' +
			'</div>' +
			'</div>';
		getSheetHost().appendChild(el);
		this._el = el;
		part(el, '.ft-pane-local .ft-pane-label').textContent = t('remote.file_local', 'Local computer');
		part(el, '.ft-pane-remote .ft-pane-label').textContent = t('remote.file_remote', 'Remote computer');
		part(el, '.ft-pane-queue .ft-pane-label').textContent = t('remote.file_transfers', 'Transfers');
		part(el, '.ft-local-send').textContent = t('remote.file_send', 'Send');
		part(el, '.ft-remote-download').textContent = t('remote.file_download', 'Download');
		part(el, '.ft-queue-empty span:last-child').textContent = t(
			'remote.file_queue_empty',
			'No transfers in progress'
		);
		part(el, '.ft-modal-close').setAttribute(
			'aria-label',
			t('remote_viewer.close_files', 'Close file transfer')
		);
		part(el, '.ft-modal-close').classList.add('icon-btn');
		// Text labels: "up" was read as "upload" when it was an arrow icon
		part(el, '.ft-local-pick').textContent = t('remote.file_pick_folder', 'Choose folder');
		part(el, '.ft-local-up').textContent = t('remote.file_parent', 'Parent folder');
		part(el, '.ft-local-home').textContent = t('remote.file_home', 'Home folder');
		part(el, '.ft-remote-up').textContent = t('remote.file_parent', 'Parent folder');
		part(el, '.ft-remote-home').textContent = t('remote.file_home', 'Home folder');
		part(el, '.ft-remote-new-folder').textContent = t('remote.file_new_folder', 'New folder');
		part(el, '.ft-remote-hidden').textContent = t('remote.file_show_hidden', 'Show hidden files');

		this._initDropzoneLabels();
		this._initOverwriteDialogLabels();

		part(el, '.ft-modal-close').addEventListener('click', () => {
			this.close();
		});
		part(el, '.ft-local-pick').addEventListener('click', () => {
			this._pickLocalFolder();
		});
		part(el, '.ft-local-up').addEventListener('click', () => {
			this._localGoUp();
		});
		part(el, '.ft-local-home').addEventListener('click', () => {
			this._localGoHome();
		});
		part(el, '.ft-local-send').addEventListener('click', () => {
			this._sendSelectedLocal();
		});
		part(el, '.ft-remote-up').addEventListener('click', () => {
			this._remoteGoUp();
		});
		part(el, '.ft-remote-home').addEventListener('click', () => {
			this._remoteGoHome();
		});
		part(el, '.ft-remote-download').addEventListener('click', () => {
			this._downloadSelectedRemote();
		});
		part(el, '.ft-remote-new-folder').addEventListener('click', () => {
			this._remoteNewFolder();
		});
		part(el, '.ft-remote-hidden').addEventListener('click', () => {
			this._toggleRemoteHidden();
		});
		part(el, '.ft-local-file-input').addEventListener('change', (e) => {
			const input = e.target as HTMLInputElement;
			this._uploadFiles(input.files);
			input.value = '';
		});

		const dropzone = part(el, '.ft-local-dropzone');
		dropzone.addEventListener('click', () => {
			if (dropzone.classList.contains('ft-dropzone-disabled')) return;
			if (LocalFiles.isDesktopBridge()) {
				LocalFiles.pickNativeFiles()
					.then((files) => {
						this._uploadNativeFiles(files);
					})
					.catch((e: unknown) => {
						if (e && errorField(e, 'message') !== 'AbortError') console.warn('[FileModal]', e);
					});
				return;
			}
			part(this._el, '.ft-local-file-input').click();
		});
		dropzone.addEventListener('keydown', (e) => {
			if (dropzone.classList.contains('ft-dropzone-disabled')) return;
			if (e.key === 'Enter' || e.key === ' ') {
				e.preventDefault();
				part(this._el, '.ft-local-file-input').click();
			}
		});
		dropzone.addEventListener('dragover', (e) => {
			if (dropzone.classList.contains('ft-dropzone-disabled')) return;
			e.preventDefault();
			if (e.dataTransfer) e.dataTransfer.dropEffect = 'copy';
			dropzone.classList.add('ft-dropzone-active');
		});
		dropzone.addEventListener('dragleave', (e) => {
			if (dropzone.contains(e.relatedTarget as Node | null)) return;
			dropzone.classList.remove('ft-dropzone-active');
		});
		dropzone.addEventListener('drop', (e) => {
			if (dropzone.classList.contains('ft-dropzone-disabled')) return;
			e.preventDefault();
			dropzone.classList.remove('ft-dropzone-active');
			if (LocalFiles.isDesktopBridge()) {
				return;
			}
			if (e.dataTransfer && e.dataTransfer.files.length) this._uploadFiles(e.dataTransfer.files);
		});

		const remotePane = part(el, '.ft-pane-remote');
		remotePane.addEventListener('dragover', (e) => {
			if (!this._remoteReady) return;
			e.preventDefault();
			if (e.dataTransfer) e.dataTransfer.dropEffect = 'copy';
			remotePane.classList.add('ft-drag-over');
			this._showDragOverlay();
		});
		remotePane.addEventListener('dragleave', (e) => {
			if (remotePane.contains(e.relatedTarget as Node | null)) return;
			remotePane.classList.remove('ft-drag-over');
			this._hideDragOverlay();
		});
		remotePane.addEventListener('drop', (e) => {
			if (!this._remoteReady) return;
			e.preventDefault();
			remotePane.classList.remove('ft-drag-over');
			this._hideDragOverlay();
			if (LocalFiles.isDesktopBridge()) {
				return;
			}
			if (e.dataTransfer && e.dataTransfer.files.length) this._uploadFiles(e.dataTransfer.files);
		});

		document.addEventListener('click', () => {
			this._hideContextMenu();
		});
	}

	_initDropzoneLabels(): void {
		if (!this._el) return;
		const dz = this._el.querySelector('.ft-local-dropzone');
		if (!dz) return;
		part(dz, '.ft-dropzone-title').textContent = t(
			'remote.file_dropzone_title',
			'Drop files or folders here'
		);
		part(dz, '.ft-dropzone-subtitle').textContent = t(
			'remote.file_dropzone_subtitle',
			'or click to choose files'
		);
		part(dz, '.ft-dropzone-multiple').textContent = t(
			'remote.file_dropzone_multiple',
			'Files and folders supported'
		);
	}

	_initOverwriteDialogLabels(): void {
		if (!this._el) return;
		part(this._el, '.ft-overwrite-title').textContent = t(
			'remote.file_overwrite_title',
			'File already exists'
		);
		part(this._el, '.ft-overwrite-skip').textContent = t('remote.file_overwrite_skip', 'Skip');
		part(this._el, '.ft-overwrite-skip-all').textContent = t('remote.file_overwrite_skip_all', 'Skip all');
		part(this._el, '.ft-overwrite-confirm').textContent = t('remote.file_overwrite_confirm', 'Overwrite');
		part(this._el, '.ft-overwrite-all').textContent = t('remote.file_overwrite_all', 'Overwrite all');
	}

	_updateDropzoneTarget(): void {
		if (!this._el) return;
		const target = this._el.querySelector('.ft-dropzone-target');
		if (!target) return;
		const ft = this._session && this._session.client && this._session.client.fileTransfer;
		const path = ft && ft.currentPath ? ft.currentPath : t('remote.file_remote', 'Remote');
		const hint = t('remote.file_drop_upload_hint', 'Drop to upload to: {path}');
		target.textContent = hint.replace('{path}', path);
	}

	_setDropzoneEnabled(enabled: boolean): void {
		if (!this._el) return;
		this._remoteReady = !!enabled;
		const dz = this._el.querySelector('.ft-local-dropzone');
		if (dz) {
			dz.classList.toggle('ft-dropzone-disabled', !enabled);
			if (!enabled) {
				const wait = t('remote.file_dropzone_waiting', 'Waiting for remote connection…');
				part(dz, '.ft-dropzone-target').textContent = wait;
			} else {
				this._updateDropzoneTarget();
			}
		}
		if (enabled && this._pendingNativePaths && this._pendingNativePaths.length) {
			const pending = this._pendingNativePaths;
			this._pendingNativePaths = null;
			this.uploadNativePaths(pending);
		}
	}

	_syncLocalLayout(): void {
		if (!this._el) return;
		const hasRoot = !!(this._local && this._local.hasRoot);
		const dz = this._el.querySelector('.ft-local-dropzone');
		const list = this._el.querySelector<HTMLElement>('.ft-local-list');
		if (dz) dz.classList.toggle('compact', hasRoot);
		if (list) list.hidden = !hasRoot;
	}

	_syncSaveDownloadHook(): void {
		const ft = this._session && this._session.client && this._session.client.fileTransfer;
		if (!ft) return;
		if (!this._local) {
			ft._saveDownload = null;
			return;
		}
		if (LocalFiles.isDesktopBridge()) {
			ft._saveDownload = (fileName: string, blob: Blob) => {
				return this._local.saveDownload(fileName, blob);
			};
			return;
		}
		if (this._local.hasRoot) {
			ft._saveDownload = (fileName: string, blob: Blob) => {
				return this._local.saveDownload(fileName, blob);
			};
		} else {
			ft._saveDownload = null;
		}
	}

	_showDragOverlay(): void {
		const overlay = this._el && this._el.querySelector<HTMLElement>('.ft-drop-overlay');
		if (!overlay) return;
		const ft = this._session && this._session.client && this._session.client.fileTransfer;
		const path = ft && ft.currentPath ? ft.currentPath : t('remote.file_remote', 'Remote');
		const hint = t('remote.file_drop_upload_hint', 'Drop to upload to: {path}');
		part(overlay, '.ft-drop-overlay-text').textContent = hint.replace('{path}', path);
		overlay.hidden = false;
	}

	_hideDragOverlay(): void {
		const overlay = this._el && this._el.querySelector<HTMLElement>('.ft-drop-overlay');
		if (overlay) overlay.hidden = true;
	}

	_updateLocalToolbarState(): void {
		if (!this._el) return;
		const hasRoot = !!(this._local && this._local.hasRoot);
		const noTreeTitle = t('remote.file_no_local_tree', 'Choose a folder to browse local files (optional)');
		const upBtn = this._el.querySelector<HTMLButtonElement>('.ft-local-up');
		const homeBtn = this._el.querySelector<HTMLButtonElement>('.ft-local-home');
		const sendBtn = this._el.querySelector<HTMLElement>('.ft-local-send');
		if (upBtn) {
			if (!upBtn.dataset.defaultTitle) upBtn.title = '';
			upBtn.dataset.defaultTitle = upBtn.title;
			upBtn.disabled = !hasRoot;
			upBtn.title = hasRoot ? upBtn.dataset.defaultTitle : noTreeTitle;
		}
		if (homeBtn) {
			if (!homeBtn.dataset.defaultTitle) homeBtn.title = '';
			homeBtn.dataset.defaultTitle = homeBtn.title;
			homeBtn.disabled = !hasRoot;
			homeBtn.title = hasRoot ? homeBtn.dataset.defaultTitle : noTreeTitle;
		}
		if (sendBtn) sendBtn.style.display = hasRoot ? '' : 'none';
		this._syncLocalLayout();
	}

	_updateRemoteHiddenButton(): void {
		const btn = this._el && this._el.querySelector('.ft-remote-hidden');
		const ft = this._session && this._session.client && this._session.client.fileTransfer;
		if (!btn || !ft) return;
		if (ft.showHidden) {
			btn.textContent = t('remote.file_hide_hidden', 'Hide hidden files');
			btn.classList.add('active');
		} else {
			btn.textContent = t('remote.file_show_hidden', 'Show hidden files');
			btn.classList.remove('active');
		}
	}

	_ensureMountedInHost(): void {
		if (!this._el) return;
		const host = getSheetHost();
		if (this._el.parentNode !== host) {
			host.appendChild(this._el);
		}
	}

	open(session?: FileSheetSession | null): void {
		if (!this._local) {
			this._local = new LocalFiles();
		}
		this._ensureDom();
		this._ensureMountedInHost();
		this._session = session || null;

		// Always show the overlay first — never fail closed with a silent return.
		if (this._el) this._el.style.display = 'flex';
		this._open = true;
		document.getElementById('btn-file-transfer')?.classList.add('active');

		// The sheet sits beside the device's own screen, so the title needs no name
		part(this._el, '.ft-modal-title').textContent = t('remote.file_transfer', 'File transfer');

		if (!session || !session.client || !session.client.fileTransfer) {
			this._remoteReady = false;
			this._setDropzoneEnabled(false);
			this._renderRemoteError(
				t('remote.file_transfer_unavailable', 'File transfer is not available for this session.')
			);
			this._paintTransferQueue();
			return;
		}

		this._transferMeta.clear();
		this._remoteReady = false;
		this._initDropzoneLabels();
		this._setDropzoneEnabled(false);
		this._updateLocalToolbarState();
		this._renderLocalList();
		this._remoteEntries = [];
		this._selectedRemote = null;
		part(this._el, '.ft-remote-path').textContent = '';
		this._renderRemoteLoading(t('remote.file_connecting', 'Connecting file transfer session…'));
		this._updateRemoteHiddenButton();
		if (this._wiredSessionId !== session.deviceId) {
			this._wireClient(session);
			this._wiredSessionId = session.deviceId;
		}
		this._syncSaveDownloadHook();
		const ft = session.client.fileTransfer;
		if (ft && typeof ft.enable === 'function' && !ft.enabled) {
			try {
				ft.enable();
			} catch {
				/* ignore */
			}
		}
		if (typeof ft.browseDir === 'function') {
			ft.browseDir('');
		}
		this._paintTransferQueue();
	}

	close(): void {
		this._open = false;
		if (this._el) this._el.style.display = 'none';
		this._hideDragOverlay();
		this._hideContextMenu();
		this._hideOverwriteDialog();
		this._pendingNativePaths = null;
		document.getElementById('btn-file-transfer')?.classList.remove('active');
		if (this._session && this._session.client && this._session.client.fileTransfer) {
			this._session.client.fileTransfer._saveDownload = null;
		}
		try {
			document.dispatchEvent(new CustomEvent('rd-file-sheet-closed'));
		} catch {
			/* old browsers */
		}
	}

	isOpen(): boolean {
		return !!this._open && !!this._el && this._el.style.display !== 'none';
	}

	_wireClient(session: FileSheetSession): void {
		const client = session.client;
		if (!client) return;
		client.on('file_browsing', () => {
			this._remoteEntries = [];
			this._renderRemoteLoading(t('remote.file_loading', 'Loading…'));
			this._setDropzoneEnabled(false);
		});
		client.on('file_connecting', () => {
			if (!this._remoteEntries.length) {
				this._renderRemoteLoading(t('remote.file_connecting', 'Connecting file transfer session…'));
			}
			this._setDropzoneEnabled(false);
		});
		client.on('file_connect_error', (data: FileSheetConnectErrorEvent) => {
			this._renderRemoteError(
				data.error || t('remote.file_connect_failed', 'Could not open file transfer session')
			);
			this._setDropzoneEnabled(false);
		});
		// The file transfer session signs in on its own; a device with 2FA asks again
		client.on('file_2fa_required', () => {
			this._renderRemoteTfa('');
			this._setDropzoneEnabled(false);
		});
		client.on('file_2fa_error', (error: unknown) => {
			this._renderRemoteTfa(String(error || t('remote.2fa_invalid', 'Enter a valid 6-digit code')));
		});
		client.on('file_login_error', (error: unknown) => {
			this._renderRemoteError(
				String(error || t('remote.file_connect_failed', 'Could not open file transfer session'))
			);
			this._setDropzoneEnabled(false);
		});
		client.on('file_dir', (data: FileSheetDirEvent) => {
			this._remoteEntries = data.entries || [];
			this._selectedRemote = null;
			part(this._el, '.ft-remote-path').textContent = data.path || '/';
			this._renderRemoteList();
			this._setDropzoneEnabled(true);
			this._updateDropzoneTarget();
			this._updateRemoteHiddenButton();
		});
		client.on('file_browse_timeout', () => {
			this._renderRemoteError(t('remote.file_timeout', 'Remote device did not respond.'));
			this._setDropzoneEnabled(false);
		});
		client.on('file_transfer_start', (data: FileSheetTransferEvent) => {
			this._addTransfer(data);
		});
		client.on('file_transfer_progress', (data: FileSheetTransferEvent) => {
			this._updateTransfer(data);
		});
		client.on('file_transfer_complete', (data: FileSheetTransferEvent) => {
			this._completeTransfer(data);
		});
		client.on('file_transfer_error', (data: FileSheetTransferEvent) => {
			this._errorTransfer(data);
		});
		client.on('file_transfer_cancelled', (data: FileSheetTransferEvent) => {
			this._removeTransfer(data.id);
		});
		client.on('file_transfer_overwrite_prompt', (data: FileSheetTransferEvent) => {
			this._showOverwriteDialog(data);
		});
		client.on('file_action', () => {
			setTimeout(() => {
				const ft = this._session && this._session.client && this._session.client.fileTransfer;
				if (ft) ft.browseDir(ft.currentPath || '');
			}, 400);
		});
	}

	async _pickLocalFolder(): Promise<void> {
		try {
			await this._local.pickRoot();
			part(this._el, '.ft-local-path').textContent = this._local.currentPath;
			this._localEntries = await this._local.listCurrent();
			this._selectedLocal = null;
			this._renderLocalList();
			this._syncSaveDownloadHook();
			this._updateLocalToolbarState();
		} catch (e) {
			if (errorField(e, 'name') !== 'AbortError' && errorField(e, 'message') !== 'unsupported')
				console.warn('[FileModal]', e);
		}
	}

	async _localGoUp(): Promise<void> {
		if (!this._local.hasRoot) return;
		await this._local.goUp();
		part(this._el, '.ft-local-path').textContent = this._local.currentPath;
		this._localEntries = await this._local.listCurrent();
		this._selectedLocal = null;
		this._renderLocalList();
	}

	async _localGoHome(): Promise<void> {
		if (!this._local.hasRoot) return;
		await this._local.goHome();
		part(this._el, '.ft-local-path').textContent = this._local.currentPath;
		this._localEntries = await this._local.listCurrent();
		this._selectedLocal = null;
		this._renderLocalList();
	}

	_renderLocalList(): void {
		const list = this._el?.querySelector('.ft-local-list');
		if (!list) return;
		if (!this._local || !this._local.hasRoot) {
			list.innerHTML = '';
			return;
		}
		list.innerHTML = '';
		if (!this._localEntries.length) {
			list.innerHTML = '<div class="ft-empty">' + escapeHtml(t('remote.file_empty', 'No files')) + '</div>';
			return;
		}
		this._localEntries.forEach((entry) => {
			const row = document.createElement('div');
			row.className = 'ft-row' + (entry.isDir ? ' ft-row-dir' : '');
			// Keyboard: Tab reaches each row, Enter opens a folder or selects a file
			row.tabIndex = 0;
			row.setAttribute('role', 'button');
			row.addEventListener('keydown', (ev) => {
				if (ev.key === 'Enter' || ev.key === ' ') {
					ev.preventDefault();
					row.click();
				}
			});
			if (this._selectedLocal === entry) row.classList.add('selected');
			row.innerHTML =
				'<span class="material-icons ft-row-icon">' +
				(entry.isDir ? 'folder' : 'insert_drive_file') +
				'</span>' +
				'<span class="ft-row-name">' +
				escapeHtml(entry.name) +
				'</span>' +
				'<span class="ft-row-size">' +
				(entry.isDir ? '' : formatSize(entry.size)) +
				'</span>';
			row.addEventListener('click', () => {
				if (entry.isDir) {
					this._local
						.enterDir(entry)
						.then(() => {
							part(this._el, '.ft-local-path').textContent = this._local.currentPath;
							return this._local.listCurrent();
						})
						.then((entries) => {
							this._localEntries = entries;
							this._selectedLocal = null;
							this._renderLocalList();
						});
				} else {
					this._selectedLocal = entry;
					this._renderLocalList();
				}
			});
			row.addEventListener('dblclick', () => {
				if (entry.isDir) this._uploadLocalFolder(entry);
				else this._uploadLocalEntry(entry);
			});
			row.addEventListener('contextmenu', (e) => {
				e.preventDefault();
				this._showLocalContextMenu(e, entry);
			});
			list.appendChild(row);
		});
	}

	_renderRemoteLoading(msg?: string): void {
		part(this._el, '.ft-remote-list').innerHTML =
			'<div class="ft-empty">' + escapeHtml(msg || t('remote.file_loading', 'Loading…')) + '</div>';
	}

	_renderRemoteError(msg: string): void {
		part(this._el, '.ft-remote-list').innerHTML =
			'<div class="ft-empty ft-error"><span>' +
			escapeHtml(msg) +
			'</span>' +
			'<button type="button" class="btn btn-sm ft-retry">' +
			escapeHtml(t('actions.retry', 'Retry')) +
			'</button></div>';
		this._el?.querySelector('.ft-retry')?.addEventListener('click', () => {
			const ft = this._session?.client?.fileTransfer;
			if (ft) ft.browseDir(ft.currentPath || '');
		});
	}

	/** The code form for the file transfer session's second factor, in the remote pane */
	_renderRemoteTfa(error: string): void {
		const list = part(this._el, '.ft-remote-list');
		list.innerHTML =
			'<form class="ft-empty ft-tfa" novalidate>' +
			'<label><span>' +
			escapeHtml(t('remote.2fa_hint', 'Enter the 6-digit code from your authenticator app')) +
			'</span>' +
			'<input class="form-input" name="code" inputmode="numeric" autocomplete="one-time-code" maxlength="7"></label>' +
			(error ? '<span class="ft-error" role="alert">' + escapeHtml(error) + '</span>' : '') +
			'<button type="submit" class="btn btn-sm">' +
			escapeHtml(t('remote.verify', 'Verify')) +
			'</button>' +
			'</form>';
		const form = part<HTMLFormElement>(list, 'form');
		const input = part<HTMLInputElement>(form, 'input');
		form.addEventListener('submit', (ev) => {
			ev.preventDefault();
			const code = input.value.replace(/\s/g, '');
			if (!/^\d{6}$/.test(code)) {
				this._renderRemoteTfa(t('remote.2fa_invalid', 'Enter a valid 6-digit code'));
				return;
			}
			const client = this._session && this._session.client;
			if (client && typeof client.submitFile2FA === 'function') client.submitFile2FA(code);
			this._renderRemoteLoading(t('remote.file_connecting', 'Connecting file transfer session…'));
		});
		input.focus();
	}

	_buildRemoteFullPath(entry: FileSheetEntry): string {
		const ft = this._session?.client?.fileTransfer;
		const sep = (ft?.currentPath || '').includes('\\') ? '\\' : '/';
		const base = ft?.currentPath || '';
		return base ? base.replace(/[\\/]+$/, '') + sep + entry.name : entry.name;
	}

	_renderRemoteList(): void {
		const list = part(this._el, '.ft-remote-list');
		list.innerHTML = '';
		if (!this._remoteEntries.length) {
			list.innerHTML = '<div class="ft-empty">' + escapeHtml(t('remote.file_empty', 'No files')) + '</div>';
			return;
		}
		const ft = this._session?.client?.fileTransfer;
		this._remoteEntries.forEach((entry) => {
			const row = document.createElement('div');
			row.className = 'ft-row' + (entry.isDir ? ' ft-row-dir' : '');
			// Keyboard: Tab reaches each row, Enter opens a folder or selects a file
			row.tabIndex = 0;
			row.setAttribute('role', 'button');
			row.addEventListener('keydown', (ev) => {
				if (ev.key === 'Enter' || ev.key === ' ') {
					ev.preventDefault();
					row.click();
				}
			});
			if (this._selectedRemote === entry) row.classList.add('selected');
			const icon = RDFileTransfer.getFileIcon(entry);
			row.innerHTML =
				'<span class="material-icons ft-row-icon">' +
				icon +
				'</span>' +
				'<span class="ft-row-name">' +
				escapeHtml(entry.name) +
				'</span>' +
				'<span class="ft-row-size">' +
				(entry.isDir ? '' : formatSize(entry.size)) +
				'</span>';
			row.addEventListener('click', () => {
				if (entry.isDir) {
					const sep = (ft?.currentPath || '').includes('\\') ? '\\' : '/';
					const base = ft?.currentPath || '';
					const next = base ? base.replace(/[\\/]+$/, '') + sep + entry.name : entry.name;
					ft?.browseDir(next);
				} else {
					this._selectedRemote = entry;
					this._renderRemoteList();
				}
			});
			row.addEventListener('dblclick', () => {
				if (entry.isDir) this._downloadRemoteFolder(entry);
				else this._downloadRemoteEntry(entry);
			});
			row.addEventListener('contextmenu', (e) => {
				e.preventDefault();
				this._showRemoteContextMenu(e, entry);
			});
			list.appendChild(row);
		});
	}

	_remoteGoUp(): void {
		this._session?.client?.fileTransfer?.browseParent();
	}

	_remoteGoHome(): void {
		this._session?.client?.fileTransfer?.browseDir('');
	}

	_remoteNewFolder(): void {
		const ft = this._session?.client?.fileTransfer;
		if (!ft) return;
		const name = window.prompt(t('remote.file_new_folder_prompt', 'Enter folder name:'));
		if (!name || !name.trim()) return;
		const sep = (ft.currentPath || '').includes('\\') ? '\\' : '/';
		const base = ft.currentPath || '';
		const path = base ? base.replace(/[\\/]+$/, '') + sep + name.trim() : name.trim();
		ft.createDir?.(path);
	}

	_toggleRemoteHidden(): void {
		const ft = this._session?.client?.fileTransfer;
		if (!ft) return;
		ft.setShowHidden?.(!ft.showHidden);
		this._updateRemoteHiddenButton();
	}

	_hideContextMenu(): void {
		if (this._contextMenu) {
			this._contextMenu.remove();
			this._contextMenu = null;
		}
	}

	_showRemoteContextMenu(e: MouseEvent, entry: FileSheetEntry): void {
		this._hideContextMenu();
		const menu = document.createElement('div');
		menu.className = 'ft-context-menu';
		menu.style.left = e.clientX + 'px';
		menu.style.top = e.clientY + 'px';

		const addItem = (label: string, icon: string, action: () => void): void => {
			const btn = document.createElement('button');
			btn.type = 'button';
			btn.className = 'ft-context-item';
			btn.innerHTML =
				'<span class="material-icons">' + icon + '</span><span>' + escapeHtml(label) + '</span>';
			btn.addEventListener('click', () => {
				this._hideContextMenu();
				action();
			});
			menu.appendChild(btn);
		};

		if (entry.isDir) {
			addItem(t('remote.file_open', 'Open'), 'folder_open', () => {
				const ft = this._session?.client?.fileTransfer;
				const sep = (ft?.currentPath || '').includes('\\') ? '\\' : '/';
				const base = ft?.currentPath || '';
				const next = base ? base.replace(/[\\/]+$/, '') + sep + entry.name : entry.name;
				ft?.browseDir(next);
			});
			addItem(t('remote.file_download_folder', 'Download folder'), 'folder_zip', () => {
				this._downloadRemoteFolder(entry);
			});
		} else {
			addItem(t('remote.file_download', 'Download'), 'download', () => {
				this._downloadRemoteEntry(entry);
			});
		}
		addItem(t('remote.file_rename', 'Rename'), 'drive_file_rename_outline', () => {
			this._remoteRename(entry);
		});
		addItem(t('actions.delete', 'Delete'), 'delete_outline', () => {
			this._remoteDelete(entry);
		});

		getOverlayHost().appendChild(menu);
		this._contextMenu = menu;
	}

	_remoteRename(entry: FileSheetEntry): void {
		const ft = this._session?.client?.fileTransfer;
		if (!ft) return;
		const newName = window.prompt(t('remote.file_rename_prompt', 'Enter new name:'), entry.name);
		if (!newName || !newName.trim() || newName.trim() === entry.name) return;
		ft.rename?.(this._buildRemoteFullPath(entry), newName.trim());
	}

	_remoteDelete(entry: FileSheetEntry): void {
		const ft = this._session?.client?.fileTransfer;
		if (!ft) return;
		if (!window.confirm(t('remote.file_delete_confirm', 'Are you sure you want to delete this item?')))
			return;
		const path = this._buildRemoteFullPath(entry);
		if (entry.isDir) ft.removeDir?.(path, true);
		else ft.removeFile?.(path);
	}

	_showOverwriteDialog(data: FileSheetTransferEvent): void {
		const dlg = this._el?.querySelector<HTMLElement>('.ft-overwrite-dialog');
		if (!dlg) return;
		const msg = t(
			'remote.file_overwrite_message',
			'A file named "{name}" already exists on the remote computer. Overwrite it?'
		);
		part(dlg, '.ft-overwrite-message').textContent = msg.replace('{name}', data.fileName || 'file');
		dlg.hidden = false;
		const ft = this._session?.client?.fileTransfer;
		const id = data.id;
		const bind = (sel: string, skip: boolean, all: boolean): void => {
			const btn = part(dlg, sel);
			const clone = btn.cloneNode(true);
			btn.parentNode?.replaceChild(clone, btn);
			clone.addEventListener('click', () => {
				this._hideOverwriteDialog();
				if (ft) ft.confirmOverwrite?.(id, skip, all);
			});
		};
		bind('.ft-overwrite-skip', true, false);
		bind('.ft-overwrite-skip-all', true, true);
		bind('.ft-overwrite-confirm', false, false);
		bind('.ft-overwrite-all', false, true);
	}

	_hideOverwriteDialog(): void {
		const dlg = this._el && this._el.querySelector<HTMLElement>('.ft-overwrite-dialog');
		if (dlg) dlg.hidden = true;
	}

	_showLocalContextMenu(e: MouseEvent, entry: LocalEntry): void {
		this._hideContextMenu();
		const menu = document.createElement('div');
		menu.className = 'ft-context-menu';
		menu.style.left = e.clientX + 'px';
		menu.style.top = e.clientY + 'px';

		const addItem = (label: string, icon: string, action: () => void): void => {
			const btn = document.createElement('button');
			btn.type = 'button';
			btn.className = 'ft-context-item';
			btn.innerHTML =
				'<span class="material-icons">' + icon + '</span><span>' + escapeHtml(label) + '</span>';
			btn.addEventListener('click', () => {
				this._hideContextMenu();
				action();
			});
			menu.appendChild(btn);
		};

		if (entry.isDir) {
			addItem(t('remote.file_open', 'Open'), 'folder_open', () => {
				this._local
					.enterDir(entry)
					.then(() => {
						part(this._el, '.ft-local-path').textContent = this._local.currentPath;
						return this._local.listCurrent();
					})
					.then((entries) => {
						this._localEntries = entries;
						this._selectedLocal = null;
						this._renderLocalList();
					});
			});
			addItem(t('remote.file_upload_folder', 'Upload folder'), 'drive_folder_upload', () => {
				this._uploadLocalFolder(entry);
			});
		} else {
			addItem(t('remote.file_upload', 'Upload'), 'upload', () => {
				this._uploadLocalEntry(entry);
			});
		}

		getOverlayHost().appendChild(menu);
		this._contextMenu = menu;
	}

	_sendSelectedLocal(): void {
		if (!this._selectedLocal) return;
		if (this._selectedLocal.isDir) this._uploadLocalFolder(this._selectedLocal);
		else this._uploadLocalEntry(this._selectedLocal);
	}

	async _uploadLocalEntry(entry: LocalEntry): Promise<void> {
		const ft = this._session?.client?.fileTransfer;
		if (!ft) return;
		const file = await this._local.readFile(entry);
		if (file) ft.uploadFile(file, ft.currentPath || '');
	}

	async _uploadLocalFolder(entry: LocalEntry | null | undefined): Promise<void> {
		const ft = this._session?.client?.fileTransfer;
		if (!ft || !entry || !entry.isDir || !this._remoteReady) return;
		try {
			let walk = await this._local.walkFolder(entry);
			if (!walk || (!walk.files.length && !walk.dirs.length)) {
				// Empty folder — still create remote root
				walk = walk || { rootName: entry.name, dirs: [], files: [] };
				walk.rootName = walk.rootName || entry.name;
			}
			ft.uploadFolder?.(walk, ft.currentPath || '');
		} catch (e) {
			console.warn('[FileModal] folder upload failed:', e);
		}
	}

	_uploadFiles(fileList: ArrayLike<File> | null | undefined): void {
		const ft = this._session?.client?.fileTransfer;
		if (!ft || !fileList || !this._remoteReady) return;
		for (const file of Array.from(fileList)) ft.uploadFile(file, ft.currentPath || '');
	}

	_uploadNativeFiles(files: readonly (FileSheetUpload | null | undefined)[] | null | undefined): void {
		const ft = this._session?.client?.fileTransfer;
		if (!ft || !files || !files.length || !this._remoteReady) return;
		for (const file of files) {
			if (file) ft.uploadFile(file, ft.currentPath || '');
		}
	}

	uploadNativePaths(paths: string[] | null | undefined): void {
		if (!paths || !paths.length) return;
		if (!this._remoteReady) {
			this._pendingNativePaths = (this._pendingNativePaths || []).concat(paths);
			return;
		}
		if (!LocalFiles.isDesktopBridge()) {
			RDDesktopDnd.openPaths(paths)
				.then((files) => {
					this._uploadNativeFiles(files);
				})
				.catch((e: unknown) => {
					console.warn('[FileModal] native drop upload failed:', e);
				});
			return;
		}

		const ft = this._session && this._session.client && this._session.client.fileTransfer;
		if (!ft) return;

		// Classify each path: files upload directly; directories become folder jobs.
		let chain: Promise<unknown> = Promise.resolve();
		paths.forEach((path) => {
			chain = chain.then(() => {
				return LocalFiles.walkDesktopPath(path).then((walk) => {
					if (walk.isFile && walk.files.length === 1) {
						const invoke = tauriInvoke();
						if (!invoke) throw new TypeError('No desktop bridge');
						return invoke('desktop_open_file', {
							path: walk.files[0]?.path
						}).then((info) => {
							const file = LocalFiles.createNativeUploadFile(info);
							if (file) ft.uploadFile(file, ft.currentPath || '');
						});
					}
					walk.rootName = walk.rootName || path.split(/[\\/]/).pop() || 'folder';
					ft.uploadFolder?.(walk, ft.currentPath || '');
				});
			});
		});
		chain.catch((e: unknown) => {
			console.warn('[FileModal] native drop upload failed:', e);
		});
	}

	_downloadSelectedRemote(): void {
		if (!this._selectedRemote) return;
		if (this._selectedRemote.isDir) this._downloadRemoteFolder(this._selectedRemote);
		else this._downloadRemoteEntry(this._selectedRemote);
	}

	_downloadRemoteEntry(entry: FileSheetEntry): void {
		const ft = this._session?.client?.fileTransfer;
		if (!ft) return;
		ft.downloadFile(ft.currentPath || '', entry);
	}

	async _downloadRemoteFolder(entry: FileSheetEntry | null | undefined): Promise<void> {
		const ft = this._session?.client?.fileTransfer;
		if (!ft || !entry || !entry.isDir) return;
		try {
			let localRoot: string | null = null;
			if (LocalFiles.isDesktopBridge()) {
				if (this._local.hasRoot && this._local.currentPath) {
					localRoot = this._local.currentPath;
				} else {
					const picked = await LocalFiles.pickFolder();
					const pickedPath =
						typeof picked === 'object' && picked !== null ? Reflect.get(picked, 'path') : undefined;
					if (!picked || !pickedPath) return;
					localRoot = String(pickedPath);
				}
			} else {
				// The browser saves the folder as <name>.zip: into the local folder when one
				// is open here, else as a download
				ft.downloadFolderAsZip?.(ft.currentPath || '', entry);
				return;
			}
			ft.downloadFolder?.(ft.currentPath || '', entry, localRoot);
		} catch (e) {
			if (e && errorField(e, 'message') !== 'AbortError')
				console.warn('[FileModal] folder download failed:', e);
		}
	}

	_ensureTransferMeta(data: FileSheetTransferEvent): FileSheetTransferMeta {
		const id = data.id;
		let meta = this._transferMeta.get(id);
		if (!meta) {
			meta = {
				id: id,
				fileName: data.fileName || 'file',
				type: data.type || 'download',
				phase: 'pending',
				percent: 0,
				transferred: 0,
				total: data.fileSize || 0,
				error: null,
				resumable: false,
				isFolder: !!data.isFolder,
				currentFile: data.currentFile || null
			};
			this._transferMeta.set(id, meta);
		}
		return meta;
	}

	_transferStatusText(meta: FileSheetTransferMeta): string {
		if (meta.error) return meta.error;
		if (meta.phase === 'pending') return t('remote.file_status_waiting', 'Waiting…');
		if (meta.phase === 'saving') return t('remote.file_status_saving', 'Saving…');
		if (meta.phase === 'done') {
			if (meta.isFolder) {
				return meta.type === 'upload'
					? t('remote.file_status_folder_uploaded', 'Folder uploaded')
					: t('remote.file_status_folder_downloaded', 'Folder downloaded');
			}
			return meta.type === 'upload'
				? t('remote.file_status_uploaded', 'Uploaded')
				: t('remote.file_status_downloaded', 'Downloaded');
		}
		let progress: string;
		if (meta.total > 0) {
			progress = (meta.percent || 0) + '% · ' + formatSize(meta.transferred) + ' / ' + formatSize(meta.total);
		} else {
			progress = t('remote.file_status_transferring', 'Transferring…');
		}
		if (meta.isFolder && meta.currentFile) {
			const cur = t('remote.file_status_current_file', 'Current: {name}').replace('{name}', meta.currentFile);
			return progress + ' · ' + cur;
		}
		return progress;
	}

	_renderTransferRowHtml(meta: FileSheetTransferMeta): string {
		const rowClass = ['ft-transfer-row'];
		if (meta.phase === 'done') rowClass.push('done');
		if (meta.error) rowClass.push('error');
		if (meta.phase === 'pending') rowClass.push('pending');
		if (meta.isFolder) rowClass.push('folder');

		let icon = meta.type === 'upload' ? 'upload' : 'download';
		if (meta.isFolder) icon = meta.type === 'upload' ? 'drive_folder_upload' : 'folder_zip';
		if (meta.phase === 'pending') icon = 'hourglass_empty';
		if (meta.error && meta.resumable) icon = 'replay';

		const indeterminate =
			meta.phase === 'pending' || (meta.isFolder && !meta.total && meta.phase !== 'done' && !meta.error);
		const fillWidth = meta.error ? 0 : Math.min(100, meta.percent || 0);
		const fillClass = 'ft-transfer-fill' + (indeterminate ? ' indeterminate' : '');
		const fillStyle = indeterminate ? '' : ' style="width:' + fillWidth + '%"';
		const canCancel = !meta.error && meta.phase !== 'done' && meta.phase !== 'saving';
		const canResume = meta.error && meta.resumable && !meta.isFolder;
		const titleName =
			meta.isFolder && meta.currentFile ? meta.fileName + ' / ' + meta.currentFile : meta.fileName;

		return (
			'<div class="' +
			rowClass.join(' ') +
			'" data-id="' +
			escapeHtml(String(meta.id)) +
			'">' +
			'<span class="material-icons ft-transfer-icon">' +
			icon +
			'</span>' +
			'<div class="ft-transfer-info">' +
			'<div class="ft-transfer-name" title="' +
			escapeHtml(titleName) +
			'">' +
			escapeHtml(meta.fileName) +
			(meta.isFolder
				? ' <span class="ft-transfer-folder-tag">' +
					escapeHtml(t('remote.file_folder_tag', 'folder')) +
					'</span>'
				: '') +
			'</div>' +
			'<div class="ft-transfer-bar"><div class="' +
			fillClass +
			'"' +
			fillStyle +
			'></div></div>' +
			'<div class="ft-transfer-status">' +
			escapeHtml(this._transferStatusText(meta)) +
			'</div>' +
			'</div>' +
			(canResume
				? '<button type="button" class="btn btn-sm ft-transfer-resume">' +
					escapeHtml(t('remote.file_resume', 'Resume')) +
					'</button>'
				: '') +
			(canCancel
				? '<button type="button" class="btn btn-sm ft-transfer-cancel">' +
					escapeHtml(t('common.cancel', 'Cancel')) +
					'</button>'
				: '') +
			'</div>'
		);
	}

	_scheduleTransferQueuePaint(immediate?: boolean): void {
		if (immediate) {
			if (this._queuePaintTimer) {
				clearTimeout(this._queuePaintTimer);
				this._queuePaintTimer = null;
			}
			this._queuePaintPending = false;
			this._paintTransferQueue();
			return;
		}
		if (this._queuePaintTimer) {
			this._queuePaintPending = true;
			return;
		}
		this._queuePaintTimer = setTimeout(() => {
			this._queuePaintTimer = null;
			this._queuePaintPending = false;
			this._paintTransferQueue();
		}, 150);
	}

	_paintTransferQueue(): void {
		if (!this._el) return;
		const list = this._el.querySelector('.ft-queue-list');
		const empty = this._el.querySelector<HTMLElement>('.ft-queue-empty');
		if (!list) return;
		const rows = Array.from(this._transferMeta.values());
		if (!rows.length) {
			list.innerHTML = '';
			if (empty) empty.style.display = '';
			return;
		}
		if (empty) empty.style.display = 'none';
		list.innerHTML = rows.map((m) => this._renderTransferRowHtml(m)).join('');
		list.querySelectorAll('.ft-transfer-cancel').forEach((btn) => {
			btn.addEventListener('click', () => {
				const row = btn.closest<HTMLElement>('.ft-transfer-row');
				const id = Number(row && row.dataset.id);
				const ft = this._session?.client?.fileTransfer;
				if (id && ft) ft.cancelTransfer(id);
			});
		});
		list.querySelectorAll('.ft-transfer-resume').forEach((btn) => {
			btn.addEventListener('click', () => {
				const row = btn.closest<HTMLElement>('.ft-transfer-row');
				const id = Number(row && row.dataset.id);
				const ft = this._session?.client?.fileTransfer;
				if (id && ft) {
					const meta = this._transferMeta.get(id);
					if (meta) {
						meta.error = null;
						meta.phase = 'pending';
						meta.resumable = false;
					}
					ft.resumeTransfer?.(id);
					this._scheduleTransferQueuePaint(true);
				}
			});
		});
	}

	_addTransfer(data: FileSheetTransferEvent): void {
		// Child transfers inside a folder job do not get their own queue row.
		if (data.folderJobId && !data.isFolder) return;
		const meta = this._ensureTransferMeta(data);
		meta.fileName = data.fileName || meta.fileName;
		meta.type = data.type || meta.type;
		meta.total = data.fileSize || meta.total;
		meta.phase = 'pending';
		meta.percent = 0;
		meta.error = null;
		meta.resumable = false;
		meta.isFolder = !!data.isFolder || meta.isFolder;
		meta.currentFile = data.currentFile || null;
		this._scheduleTransferQueuePaint(true);
	}

	_updateTransfer(data: FileSheetTransferEvent): void {
		if (data.folderJobId && !data.isFolder) return;
		const meta = this._ensureTransferMeta(data);
		if (data.phase === 'saving') {
			meta.phase = 'saving';
			meta.percent = 100;
			this._scheduleTransferQueuePaint(true);
			return;
		}
		meta.phase = 'transferring';
		meta.percent = data.percent || 0;
		meta.transferred = data.transferred || 0;
		meta.total = data.fileSize != null ? data.fileSize : meta.total;
		if (data.isFolder) meta.isFolder = true;
		if (data.currentFile != null) meta.currentFile = data.currentFile;
		// Progress fires many times per second — throttle full queue DOM rebuilds.
		this._scheduleTransferQueuePaint(false);
	}

	_completeTransfer(data: FileSheetTransferEvent): void {
		if (data.folderJobId && !data.isFolder) return;
		const meta = this._ensureTransferMeta(data);
		meta.phase = 'done';
		meta.percent = 100;
		meta.transferred = meta.total || meta.transferred;
		meta.error = null;
		meta.resumable = false;
		meta.currentFile = null;
		if (data.isFolder) meta.isFolder = true;
		this._scheduleTransferQueuePaint(true);
	}

	_errorTransfer(data: FileSheetTransferEvent): void {
		if (data.folderJobId && !data.isFolder) return;
		const meta = this._ensureTransferMeta(data);
		let errMsg = data.error || t('remote.file_error', 'Failed');
		if (errMsg === 'Remote did not start transfer') {
			errMsg = t('remote.file_transfer_stalled', 'Remote did not start transfer');
		}
		meta.error = errMsg;
		meta.phase = 'error';
		meta.resumable = !!data.resumable;
		if (data.isFolder) meta.isFolder = true;
		if (data.currentFile != null) meta.currentFile = data.currentFile;
		this._scheduleTransferQueuePaint(true);
	}

	_removeTransfer(id: number): void {
		this._transferMeta.delete(id);
		this._scheduleTransferQueuePaint(true);
	}
}

try {
	page().__fileTransferModal = new FileTransferModal();
} catch (err) {
	console.error('[FileModal] failed to initialize — clipboard/Cliprdr must still work:', err);
	page().__fileTransferModal = null;
}

/** The sheet this module created when it loaded (null when that failed) */
export function fileTransferModal(): FileTransferModal | null {
	return page().__fileTransferModal ?? null;
}

// Keep modal/context menus inside the fullscreen element when FS toggles.
document.addEventListener('fullscreenchange', () => {
	const modal = page().__fileTransferModal;
	if (modal && typeof modal._ensureMountedInHost === 'function') {
		modal._ensureMountedInHost();
	}
	if (modal && modal._contextMenu && modal._contextMenu.parentNode) {
		getOverlayHost().appendChild(modal._contextMenu);
	}
});
