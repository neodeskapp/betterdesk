/**
 * Per-browser preferences in localStorage. Never tokens or secrets.
 * Storage can be blocked (private windows, policies), so every access is
 * wrapped and falls back to a value kept for this page only.
 */

const memory = new Map<string, string>();

export function readPref(key: string): string | null {
	try {
		const v = window.localStorage.getItem(key);
		return v ?? memory.get(key) ?? null;
	} catch {
		return memory.get(key) ?? null;
	}
}

export function writePref(key: string, value: string | null): void {
	if (value === null) memory.delete(key);
	else memory.set(key, value);
	try {
		if (value === null) window.localStorage.removeItem(key);
		else window.localStorage.setItem(key, value);
	} catch {
		/* kept for this page only */
	}
}

export function readJsonPref<T>(key: string, fallback: T): T {
	const raw = readPref(key);
	if (!raw) return fallback;
	try {
		return JSON.parse(raw) as T;
	} catch {
		return fallback;
	}
}

export function writeJsonPref(key: string, value: unknown): void {
	writePref(key, JSON.stringify(value));
}

/** Keys the console uses. Old pages use the same ones, so switching back and forth keeps them. */
export const PREF = {
	theme: 'nd-theme-choice',
	a11y: 'neodesk_accessibility_v2',
	sidebarWidth: 'nd_ux35_sidebar_width',
	sidebarCollapsed: 'nd_ux35_sidebar_collapsed',
	navMoreOpen: 'nd_nav_more_open',
	lang: 'neodesk_lang',
	presence: 'nd_my_presence'
} as const;
