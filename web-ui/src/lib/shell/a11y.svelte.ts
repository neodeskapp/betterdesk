/**
 * Accessibility settings: six options, the same as the server keeps per
 * account (neodesk-server/console/accessibility).
 *
 * - Reduce motion and More contrast follow the system by default
 *   (prefers-reduced-motion, prefers-contrast: more); "on" and "off"
 *   override it.
 * - Underline links, Readable font, Turn off single-key shortcuts and
 *   Pause live updates are off by default.
 *
 * The browser keeps a copy (neodesk_accessibility_v2) for the pages before
 * sign-in and for the first paint: /console-boot.js applies it before the
 * page draws, with the same rules as applyToRoot. After sign-in the
 * account's settings (in the bootstrap) win and replace the copy. Each
 * change is saved right away (for the account when signed in) and
 * announced; a change the server refuses goes back.
 *
 * On <html>: data-a11y-reduce-motion and data-a11y-more-contrast say
 * whether the option applies now ("true" or "false", the system's setting
 * already worked in), data-a11y-underline-links and
 * data-a11y-readable-font the two switches. styles/a11y.css does the rest.
 */

import { saveAccessibility } from '#lib/api/accessibility.ts';
import { t } from '#lib/i18n/i18n.svelte.ts';
import { setLivePaused } from '#lib/state/live-updates.svelte.ts';
import { announce } from '#lib/ui/announce.svelte.ts';
import { toast } from '#lib/ui/toasts.svelte.ts';
import { PREF, readPref, writePref } from '#lib/util/storage.ts';

export const STORAGE_KEY = PREF.a11y;
/** The panel of earlier versions kept its 20 settings here */
export const V1_KEY = 'neodesk_accessibility_v1';

export const FOLLOW = ['system', 'on', 'off'] as const;
export type Follow = (typeof FOLLOW)[number];

export interface A11ySettings {
	reduceMotion: Follow;
	moreContrast: Follow;
	underlineLinks: boolean;
	readableFont: boolean;
	noSingleKeyShortcuts: boolean;
	pauseLiveUpdates: boolean;
}

export type FollowKey = 'reduceMotion' | 'moreContrast';
export type FlagKey = Exclude<keyof A11ySettings, FollowKey>;

export const DEFAULTS: Readonly<A11ySettings> = Object.freeze({
	reduceMotion: 'system',
	moreContrast: 'system',
	underlineLinks: false,
	readableFont: false,
	noSingleKeyShortcuts: false,
	pauseLiveUpdates: false
});

export const FLAG_KEYS: readonly FlagKey[] = [
	'underlineLinks',
	'readableFont',
	'noSingleKeyShortcuts',
	'pauseLiveUpdates'
];

/** The system setting each choice follows */
const SYSTEM_QUERY: Readonly<Record<FollowKey, string>> = {
	reduceMotion: '(prefers-reduced-motion: reduce)',
	moreContrast: '(prefers-contrast: more)'
};

function follow(value: unknown): Follow {
	return (FOLLOW as readonly unknown[]).includes(value) ? (value as Follow) : 'system';
}

/** Clean settings from storage, the server or a control: unknown values back to the default */
export function sanitize(candidate: unknown): A11ySettings {
	const src = candidate && typeof candidate === 'object' ? (candidate as Record<string, unknown>) : {};
	const clean: A11ySettings = { ...DEFAULTS };
	clean.reduceMotion = follow(src.reduceMotion);
	clean.moreContrast = follow(src.moreContrast);
	for (const key of FLAG_KEYS) clean[key] = src[key] === true;
	return clean;
}

/**
 * The settings of the old panel that still exist: Reduce motion (or Pause
 * animations), Underline links, Readable font (or Dyslexia spacing) and any
 * contrast mode as More contrast. What was off now follows the system.
 */
export function fromV1(candidate: unknown): A11ySettings {
	const old = candidate && typeof candidate === 'object' ? (candidate as Record<string, unknown>) : {};
	const contrast = old.contrast;
	return {
		...DEFAULTS,
		reduceMotion: old.reduceMotion === true || old.pauseAnimations === true ? 'on' : 'system',
		moreContrast: typeof contrast === 'string' && contrast !== 'normal' && contrast !== '' ? 'on' : 'system',
		underlineLinks: old.underlineLinks === true,
		readableFont: old.readableFont === true || old.dyslexiaFont === true
	};
}

export function isDefault(s: A11ySettings): boolean {
	return (Object.keys(DEFAULTS) as (keyof A11ySettings)[]).every((k) => s[k] === DEFAULTS[k]);
}

function systemPrefers(query: string): boolean {
	try {
		return window.matchMedia?.(query).matches ?? false;
	} catch {
		return false;
	}
}

/** Whether a choice applies now: on, off, or what the system says */
export function applies(s: A11ySettings, key: FollowKey): boolean {
	const choice = s[key];
	if (choice === 'system') return systemPrefers(SYSTEM_QUERY[key]);
	return choice === 'on';
}

/** Put the settings on <html> (the boot script does the same before first paint) */
export function applyToRoot(s: A11ySettings, root: HTMLElement = document.documentElement): void {
	root.dataset.a11yReduceMotion = String(applies(s, 'reduceMotion'));
	root.dataset.a11yMoreContrast = String(applies(s, 'moreContrast'));
	root.dataset.a11yUnderlineLinks = String(s.underlineLinks);
	root.dataset.a11yReadableFont = String(s.readableFont);
	setLivePaused(s.pauseLiveUpdates);
}

/** The browser's copy; the old panel's settings once, then its key goes */
export function load(): A11ySettings {
	const raw = readPref(STORAGE_KEY);
	if (raw !== null) {
		try {
			return sanitize(JSON.parse(raw));
		} catch {
			return { ...DEFAULTS };
		}
	}
	const old = readPref(V1_KEY);
	if (old === null) return { ...DEFAULTS };
	let migrated: A11ySettings;
	try {
		migrated = fromV1(JSON.parse(old));
	} catch {
		migrated = { ...DEFAULTS };
	}
	writePref(STORAGE_KEY, JSON.stringify(migrated));
	writePref(V1_KEY, null);
	return migrated;
}

class A11yState {
	settings = $state<A11ySettings>({ ...DEFAULTS });
	/** The panel is open */
	open = $state(false);
	/** Signed in: changes are saved for the account */
	account = $state(false);
}

export const a11y = new A11yState();

function keep(s: A11ySettings): void {
	a11y.settings = s;
	writePref(STORAGE_KEY, JSON.stringify(s));
	applyToRoot(s);
}

let started = false;

/** Read the browser's copy and apply it, and follow the system and other tabs */
export function initA11y(): void {
	a11y.settings = load();
	applyToRoot(a11y.settings);
	if (started) return;
	started = true;
	window.addEventListener('storage', (e) => {
		if (e.key !== STORAGE_KEY) return;
		a11y.settings = load();
		applyToRoot(a11y.settings);
	});
	for (const query of Object.values(SYSTEM_QUERY)) {
		try {
			window.matchMedia?.(query).addEventListener('change', () => applyToRoot(a11y.settings));
		} catch {
			/* no media queries: the choices stay as they are */
		}
	}
}

/**
 * The signed-in account's settings from the bootstrap. They replace the
 * browser's copy; an account that never saved any (null) keeps the copy.
 */
export function applyAccountA11y(saved: unknown): void {
	a11y.account = true;
	if (saved === null || saved === undefined) return;
	keep(sanitize(saved));
}

/** Signed out: changes stay in this browser */
export function leaveAccountA11y(): void {
	a11y.account = false;
}

let queue: Promise<unknown> = Promise.resolve();
/** Counts the changes, so a refused one goes back only when no later one came */
let changes = 0;

/**
 * Change one setting: it applies at once, is saved (for the account when
 * signed in) and announced. When the server refuses, the old value comes
 * back and an error toast says so. Resolves to whether it was saved.
 */
export function setA11y<K extends keyof A11ySettings>(key: K, value: A11ySettings[K]): Promise<boolean> {
	const before = $state.snapshot(a11y.settings);
	const next = sanitize({ ...before, [key]: value });
	const mine = ++changes;
	keep(next);
	const run = async (): Promise<boolean> => {
		if (a11y.account) {
			try {
				await saveAccessibility(next);
			} catch {
				if (changes === mine) keep(before);
				toast.error(t('accessibility.save_failed'));
				return false;
			}
		}
		announce(t('accessibility.saved'));
		return true;
	};
	const result = queue.then(run, run);
	queue = result;
	return result;
}
