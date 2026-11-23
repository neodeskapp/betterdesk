/**
 * How the viewer shows a device's displays, remembered per device and
 * console account in this browser (localStorage
 * "neodesk_remote_displays_<user id>"): the layout, which display the
 * single layout shows, each display's zoom, and how displays in the
 * background stream. The 50 devices used last are kept.
 */
import { DISPLAY_LAYOUTS, type DisplayLayout, type DisplayZoom } from './client/display-layout.ts';
import { BACKGROUND_MODES, type BackgroundMode } from './client/display-streams.ts';

export interface DisplayPrefs {
	layout: DisplayLayout;
	/** The display the single layout shows, null for the one the device starts with */
	display: number | null;
	/** Zoom per display index; fit when not set */
	zoom: Record<number, DisplayZoom>;
	background: BackgroundMode;
}

export const DEFAULT_DISPLAY_PREFS: Readonly<DisplayPrefs> = Object.freeze({
	layout: 'single',
	display: null,
	zoom: {},
	background: 'live'
});

const KEEP = 50;

function oneOf<T extends string>(list: readonly T[], v: unknown): v is T {
	return typeof v === 'string' && (list as readonly string[]).includes(v);
}

export function sanitizeDisplayPrefs(raw: unknown): DisplayPrefs {
	const s = raw && typeof raw === 'object' ? (raw as Record<string, unknown>) : {};
	const zoom: Record<number, DisplayZoom> = {};
	if (s.zoom && typeof s.zoom === 'object') {
		for (const [k, v] of Object.entries(s.zoom)) {
			const idx = Number(k);
			if (Number.isInteger(idx) && idx >= 0 && idx < 64 && (v === 'fit' || v === '1:1')) zoom[idx] = v;
		}
	}
	const display = Number(s.display);
	return {
		layout: oneOf(DISPLAY_LAYOUTS, s.layout) ? s.layout : DEFAULT_DISPLAY_PREFS.layout,
		display: s.display !== null && Number.isInteger(display) && display >= 0 && display < 64 ? display : null,
		zoom,
		background: oneOf(BACKGROUND_MODES, s.background) ? s.background : DEFAULT_DISPLAY_PREFS.background
	};
}

export function displayPrefsKey(userId: string | number | null | undefined): string {
	return 'neodesk_remote_displays_' + (userId != null ? String(userId) : 'anonymous');
}

/** Every device's entry, most recently saved last */
function readAll(userId: string | number | null | undefined): Record<string, unknown> {
	try {
		const raw = localStorage.getItem(displayPrefsKey(userId));
		const all: unknown = raw ? JSON.parse(raw) : null;
		return all && typeof all === 'object' && !Array.isArray(all) ? (all as Record<string, unknown>) : {};
	} catch {
		return {};
	}
}

export function loadDisplayPrefs(userId: string | number | null | undefined, deviceId: string): DisplayPrefs {
	return sanitizeDisplayPrefs(readAll(userId)[deviceId]);
}

export function saveDisplayPrefs(
	userId: string | number | null | undefined,
	deviceId: string,
	prefs: DisplayPrefs
): DisplayPrefs {
	const clean = sanitizeDisplayPrefs(prefs);
	const entries = Object.entries(readAll(userId)).filter(([id]) => id !== deviceId);
	entries.push([deviceId, clean]);
	try {
		localStorage.setItem(displayPrefsKey(userId), JSON.stringify(Object.fromEntries(entries.slice(-KEEP))));
	} catch {
		/* storage blocked */
	}
	return clean;
}
