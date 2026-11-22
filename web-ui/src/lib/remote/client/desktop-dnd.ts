/**
 * Native OS file drag-and-drop bridge for RdClient desktop (Tauri tauri://drag-drop).
 * HTML5 DataTransfer drops in WebView2 do not expose reliable paths or bytes.
 */
import { LocalFiles, type NativeUploadFile } from './local-files.ts';
import * as log from './log.ts';
import { page, tauriInvoke } from './page.ts';

const DROP_DEBOUNCE_MS = 400;

/** Where a native drop landed, in the webview's coordinates */
export interface RDDropPosition {
	x: number;
	y: number;
}

/** RDDesktopDnd.bind: who gets the dropped paths */
export interface RDDesktopDndBindOptions {
	/** (paths, position): position is the drop event's, normally an RDDropPosition or null */
	onCliprdrSync?: (paths: string[], position: unknown) => void;
	onUploadPaths?: (paths: string[]) => void;
}

function isDesktopBridge(): boolean {
	return page().__BETTERDESK_RDCLIENT_DESKTOP__ === true && tauriInvoke() !== null;
}

function desktopInvoke(cmd: string, args?: Record<string, unknown>): Promise<unknown> {
	const invoke = tauriInvoke();
	if (!invoke) throw new TypeError('No desktop bridge');
	return invoke(cmd, args || {});
}

/** A field of an event payload (undefined when the payload is no object) */
function field(obj: unknown, key: string): unknown {
	return typeof obj === 'object' && obj !== null ? Reflect.get(obj, key) : undefined;
}

function normalizePaths(paths: unknown): string[] {
	if (!Array.isArray(paths) || !paths.length) return [];
	const out: string[] = [];
	for (const p of paths) {
		if (typeof p === 'string' && p.trim()) out.push(p.trim());
	}
	return out;
}

function debugLog(...args: unknown[]): void {
	if (page().BetterDesk?.debugRelay) {
		log.debugLog('[RDDesktopDnd]', ...args);
	}
}

function normalizePosition(position: unknown): RDDropPosition | null {
	if (!position) return null;
	const x = field(position, 'x') != null ? field(position, 'x') : field(position, 'X');
	const y = field(position, 'y') != null ? field(position, 'y') : field(position, 'Y');
	if (x == null || y == null) return null;
	return { x: Number(x), y: Number(y) };
}

function shouldEmitDrop(paths: string[]): boolean {
	const key = paths.join('\0');
	const now = Date.now();
	const w = page();
	if (
		w.__rdDeskNativeDropLastKey === key &&
		w.__rdDeskNativeDropLastAt &&
		now - w.__rdDeskNativeDropLastAt < DROP_DEBOUNCE_MS
	) {
		debugLog('debounced duplicate drop', paths.length, 'path(s)');
		return false;
	}
	w.__rdDeskNativeDropLastKey = key;
	w.__rdDeskNativeDropLastAt = now;
	return true;
}

function emitNativeDrop(paths: string[], position: unknown): void {
	debugLog('tauri://drag-drop payload paths=', paths);
	if (!paths.length) return;
	if (!shouldEmitDrop(paths)) return;
	log.debugInfo('[RDDesktopDnd] native file drop:', paths.length, 'path(s)');
	window.dispatchEvent(
		new CustomEvent('rd-desk-native-drop', {
			detail: { paths: paths, position: normalizePosition(position) }
		})
	);
}

function payloadPaths(payload: unknown): string[] {
	if (!payload) return [];
	return normalizePaths(field(payload, 'paths') || field(payload, 'Paths'));
}

/**
 * Register tauri://drag-drop here (not only in the init script) so __TAURI__
 * is guaranteed to exist when viewer scripts load.
 */
function installNativeDropListener(): boolean {
	const w = page();
	if (!isDesktopBridge() || w.__rdDeskNativeDropTauriBound) return false;
	const ev = w.__TAURI__?.event;
	if (!ev || typeof ev.listen !== 'function') return false;

	w.__rdDeskNativeDropTauriBound = true;
	ev.listen('tauri://drag-drop', (e) => {
		debugLog('tauri://drag-drop event received', e);
		const payload: unknown = e && e.payload ? e.payload : e;
		emitNativeDrop(payloadPaths(payload), payload && field(payload, 'position'));
	})
		.then(() => {
			debugLog('tauri://drag-drop listener registered');
		})
		.catch((err: unknown) => {
			page().__rdDeskNativeDropTauriBound = false;
			console.warn('[RDDesktopDnd] drag-drop listen failed:', err);
		});
	return true;
}

function ensureNativeDropListener(): void {
	if (installNativeDropListener()) return;
	const w = page();
	if (w.__rdDeskNativeDropRetryTimer) return;
	let attempts = 0;
	w.__rdDeskNativeDropRetryTimer = setInterval(() => {
		attempts += 1;
		if (installNativeDropListener() || attempts >= 40) {
			clearInterval(page().__rdDeskNativeDropRetryTimer ?? undefined);
			page().__rdDeskNativeDropRetryTimer = null;
		}
	}, 250);
}

if (document.readyState === 'loading') {
	document.addEventListener('DOMContentLoaded', ensureNativeDropListener, { once: true });
} else {
	ensureNativeDropListener();
}

export const RDDesktopDnd = {
	isSupported: isDesktopBridge,
	ensureNativeDropListener: ensureNativeDropListener,

	/**
	 * Open dropped/picked paths as native upload file handles.
	 */
	openPaths(paths: readonly string[] | null | undefined): Promise<NativeUploadFile[]> {
		const list = normalizePaths(paths);
		if (!list.length || !isDesktopBridge()) return Promise.resolve([]);
		return desktopInvoke('desktop_open_paths', { paths: list }).then((infos) => {
			if (!infos) return [];
			if (!Array.isArray(infos)) throw new TypeError('The desktop shell did not answer with a list');
			return infos
				.map(LocalFiles.createNativeUploadFile)
				.filter((file): file is NativeUploadFile => Boolean(file));
		});
	},

	/**
	 * Wire native drop events into cliprdr and/or file-transfer upload.
	 */
	bind(opts?: RDDesktopDndBindOptions | null): void {
		if (!isDesktopBridge()) {
			debugLog('bind() skipped — isDesktopBridge() is false');
			return;
		}
		if (page().__rdDeskNativeDropListener) return;
		ensureNativeDropListener();
		page().__rdDeskNativeDropListener = true;
		window.addEventListener('rd-desk-native-drop', (e) => {
			const detail = field(e, 'detail');
			const paths = normalizePaths(e && detail && field(detail, 'paths'));
			const position = e && detail ? field(detail, 'position') : null;
			debugLog('rd-desk-native-drop received, paths=', paths, 'position=', position);
			if (!paths.length) return;
			if (opts && typeof opts.onCliprdrSync === 'function') {
				opts.onCliprdrSync(paths, position);
			}
			if (opts && typeof opts.onUploadPaths === 'function') {
				opts.onUploadPaths(paths);
			}
		});
	}
};
