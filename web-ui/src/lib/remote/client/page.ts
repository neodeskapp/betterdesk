/**
 * The page globals the remote client reads. The viewer's loader
 * (../rdclient.ts) sets the NeoDesk object, t and the __ values before
 * a client starts; the RdClient desktop shell (Tauri) adds __TAURI__ and
 * __NEODESK_RDCLIENT_DESKTOP__; the dev mock reads and sets its own.
 * Everything is optional: a value that is not there reads as undefined.
 */
import type { ViewerCapabilities } from '../access.ts';
import type { FileTransferModal } from './file-modal.ts';

/** The desktop shell's bridge: commands of the Rust side and its events */
export interface TauriApi {
	core?: {
		invoke?: (cmd: string, args?: Record<string, unknown>) => Promise<unknown>;
	};
	event?: {
		listen?: (event: string, handler: (event: { payload: unknown }) => void) => Promise<() => void>;
	};
}

export interface PageGlobals {
	NeoDesk?: {
		csrfToken?: string;
		t?: (key: string) => string;
		/** Verbose relay and sign-in logging (set by hand in the browser console) */
		debugRelay?: boolean;
		/** Verbose CDAP session logging (set by hand in the browser console) */
		debugRemote?: boolean;
	};
	/** Translation: the key comes back when there is no string for it */
	t?: (key: string) => string;
	__capabilities?: ViewerCapabilities;
	__guestToken?: string;
	__initialDeviceId?: string;
	__mockRemote?: { scenario: string } | null;
	__TAURI__?: TauriApi;
	__NEODESK_RDCLIENT_DESKTOP__?: boolean;
	/** The viewer's touch detection (none in this console: the client treats it as a desktop) */
	DeviceCapabilities?: { isTouch(): boolean; isPhone(): boolean };
	/** A shared clipboard of several viewers (none in this console) */
	rdClipboardHub?: { setFromRemote(entry: unknown, deviceId: string): void };
	webkitAudioContext?: typeof AudioContext;
	/** The file transfer sheet; file-modal.ts creates it when it loads (null when that failed) */
	__fileTransferModal?: FileTransferModal | null;
	/*
	 * RDDesktopDnd's native drop state, on the page as desktop-dnd.js kept it:
	 * the last drop (a repeat within 400 ms is dropped), whether tauri://drag-drop
	 * is listened to, the retry timer while the bridge is missing, and whether
	 * bind() listens to rd-desk-native-drop
	 */
	__rdDeskNativeDropLastKey?: string;
	__rdDeskNativeDropLastAt?: number;
	__rdDeskNativeDropTauriBound?: boolean;
	__rdDeskNativeDropRetryTimer?: ReturnType<typeof setInterval> | null;
	__rdDeskNativeDropListener?: boolean;
}

/** The window with the globals above */
export function page(): Window & PageGlobals {
	return window as Window & PageGlobals;
}

/** The desktop shell's command bridge, when the page runs in it */
export function tauriInvoke(): ((cmd: string, args?: Record<string, unknown>) => Promise<unknown>) | null {
	const core = page().__TAURI__?.core;
	const invoke = core?.invoke;
	if (!core || typeof invoke !== 'function') return null;
	return (cmd, args) => invoke.call(core, cmd, args);
}
