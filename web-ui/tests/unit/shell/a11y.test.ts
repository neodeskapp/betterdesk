import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import {
	DEFAULTS,
	STORAGE_KEY,
	V1_KEY,
	a11y,
	applies,
	applyAccountA11y,
	applyToRoot,
	fromV1,
	initA11y,
	leaveAccountA11y,
	load,
	sanitize,
	setA11y
} from '../../../src/lib/shell/a11y.svelte.ts';
import { liveUpdates } from '../../../src/lib/state/live-updates.svelte.ts';
import { announcer, resetAnnouncer } from '../../../src/lib/ui/announce.svelte.ts';
import { toast } from '../../../src/lib/ui/toasts.svelte.ts';
import { writePref } from '../../../src/lib/util/storage.ts';

const root = document.documentElement;

/** The system's answers to the media queries the settings follow */
let system: Record<string, boolean> = {};
function stubSystem(answers: Record<string, boolean>) {
	system = answers;
	vi.stubGlobal('matchMedia', (query: string) => ({
		matches: system[query] ?? false,
		media: query,
		addEventListener: () => {},
		removeEventListener: () => {}
	}));
}

beforeEach(() => {
	localStorage.clear();
	// storage.ts keeps a copy for blocked storage too
	writePref(STORAGE_KEY, null);
	writePref(V1_KEY, null);
	stubSystem({});
	leaveAccountA11y();
	a11y.settings = { ...DEFAULTS };
	applyToRoot(a11y.settings);
	resetAnnouncer();
});
afterEach(() => {
	vi.unstubAllGlobals();
	vi.useRealTimers();
});

describe('the settings', () => {
	test('six options; motion and contrast follow the system, the rest is off', () => {
		expect(STORAGE_KEY).toBe('neodesk_accessibility_v2');
		expect(DEFAULTS).toEqual({
			reduceMotion: 'system',
			moreContrast: 'system',
			underlineLinks: false,
			readableFont: false,
			noSingleKeyShortcuts: false,
			pauseLiveUpdates: false
		});
		expect(sanitize(null)).toEqual(DEFAULTS);
		expect(
			sanitize({ reduceMotion: 'sometimes', moreContrast: 'off', readableFont: 'yes', fontScale: 140 })
		).toEqual({ ...DEFAULTS, moreContrast: 'off' });
	});

	test('"Follow system" takes the system setting, "On" and "Off" override it', () => {
		stubSystem({ '(prefers-reduced-motion: reduce)': true, '(prefers-contrast: more)': false });
		expect(applies(DEFAULTS, 'reduceMotion')).toBe(true);
		expect(applies({ ...DEFAULTS, reduceMotion: 'off' }, 'reduceMotion')).toBe(false);
		expect(applies(DEFAULTS, 'moreContrast')).toBe(false);
		expect(applies({ ...DEFAULTS, moreContrast: 'on' }, 'moreContrast')).toBe(true);
	});

	test('<html> gets what applies now', () => {
		stubSystem({ '(prefers-contrast: more)': true });
		applyToRoot({ ...DEFAULTS, reduceMotion: 'on', underlineLinks: true });
		expect(root.dataset.a11yReduceMotion).toBe('true');
		expect(root.dataset.a11yMoreContrast).toBe('true');
		expect(root.dataset.a11yUnderlineLinks).toBe('true');
		expect(root.dataset.a11yReadableFont).toBe('false');
		applyToRoot({ ...DEFAULTS, moreContrast: 'off', readableFont: true });
		expect(root.dataset.a11yReduceMotion).toBe('false');
		expect(root.dataset.a11yMoreContrast).toBe('false');
		expect(root.dataset.a11yReadableFont).toBe('true');
	});

	test('Pause live updates pauses the pollers', () => {
		applyToRoot({ ...DEFAULTS, pauseLiveUpdates: true });
		expect(liveUpdates.paused).toBe(true);
		applyToRoot(DEFAULTS);
		expect(liveUpdates.paused).toBe(false);
	});
});

describe('the settings of the old panel', () => {
	test('what still exists is taken over', () => {
		expect(
			fromV1({ reduceMotion: true, underlineLinks: true, contrast: 'yellow-black', fontScale: 140 })
		).toEqual({
			...DEFAULTS,
			reduceMotion: 'on',
			moreContrast: 'on',
			underlineLinks: true
		});
		expect(fromV1({ pauseAnimations: true, dyslexiaFont: true, contrast: 'normal' })).toEqual({
			...DEFAULTS,
			reduceMotion: 'on',
			readableFont: true
		});
		expect(fromV1({ readableFont: true, contrast: 'high-light' })).toMatchObject({
			readableFont: true,
			moreContrast: 'on'
		});
		expect(fromV1('x')).toEqual(DEFAULTS);
	});

	test('load moves them to the new key once and drops the old one', () => {
		localStorage.setItem(
			V1_KEY,
			JSON.stringify({ reduceMotion: true, contrast: 'high-dark', largeCursor: true })
		);
		expect(load()).toEqual({ ...DEFAULTS, reduceMotion: 'on', moreContrast: 'on' });
		expect(localStorage.getItem(V1_KEY)).toBeNull();
		expect(JSON.parse(localStorage.getItem(STORAGE_KEY)!)).toEqual({
			...DEFAULTS,
			reduceMotion: 'on',
			moreContrast: 'on'
		});
		// The new key wins from then on
		localStorage.setItem(V1_KEY, JSON.stringify({ underlineLinks: true }));
		expect(load().underlineLinks).toBe(false);
	});

	test('start applies the stored copy', () => {
		localStorage.setItem(STORAGE_KEY, JSON.stringify({ ...DEFAULTS, readableFont: true }));
		initA11y();
		expect(a11y.settings.readableFont).toBe(true);
		expect(root.dataset.a11yReadableFont).toBe('true');
	});
});

describe('saving a change', () => {
	test('signed out: kept in this browser and announced', async () => {
		vi.useFakeTimers();
		const fetchMock = vi.fn();
		vi.stubGlobal('fetch', fetchMock);
		expect(await setA11y('underlineLinks', true)).toBe(true);
		expect(fetchMock).not.toHaveBeenCalled();
		expect(JSON.parse(localStorage.getItem(STORAGE_KEY)!).underlineLinks).toBe(true);
		expect(root.dataset.a11yUnderlineLinks).toBe('true');
		await vi.advanceTimersByTimeAsync(200);
		expect(announcer.polite).toBe('accessibility.saved');
	});

	test('signed in: saved for the account, the whole set', async () => {
		vi.useFakeTimers();
		applyAccountA11y(null);
		const fetchMock = vi.fn(async () => Response.json({ success: true, data: {} }));
		vi.stubGlobal('fetch', fetchMock);
		expect(await setA11y('moreContrast', 'on')).toBe(true);
		const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
		expect(url).toBe('/api/accessibility');
		expect(init.method).toBe('PUT');
		expect(JSON.parse(String(init.body))).toEqual({ ...DEFAULTS, moreContrast: 'on' });
		await vi.advanceTimersByTimeAsync(200);
		expect(announcer.polite).toBe('accessibility.saved');
	});

	test('a refused save brings the old value back and says so', async () => {
		vi.useFakeTimers();
		applyAccountA11y(null);
		vi.stubGlobal(
			'fetch',
			vi.fn(async () => Response.json({ success: false, error: 'no' }, { status: 500 }))
		);
		const error = vi.spyOn(toast, 'error');
		expect(await setA11y('readableFont', true)).toBe(false);
		expect(a11y.settings.readableFont).toBe(false);
		expect(root.dataset.a11yReadableFont).toBe('false');
		expect(JSON.parse(localStorage.getItem(STORAGE_KEY)!).readableFont).toBe(false);
		expect(error).toHaveBeenCalledWith('accessibility.save_failed');
		await vi.advanceTimersByTimeAsync(200);
		expect(announcer.polite).not.toBe('accessibility.saved');
		error.mockRestore();
	});
});

describe('after sign-in', () => {
	test("the account's settings win and replace the browser's copy", () => {
		localStorage.setItem(STORAGE_KEY, JSON.stringify({ ...DEFAULTS, underlineLinks: true }));
		initA11y();
		applyAccountA11y({ ...DEFAULTS, readableFont: true, pauseLiveUpdates: true });
		expect(a11y.settings).toEqual({ ...DEFAULTS, readableFont: true, pauseLiveUpdates: true });
		expect(root.dataset.a11yUnderlineLinks).toBe('false');
		expect(JSON.parse(localStorage.getItem(STORAGE_KEY)!)).toEqual({
			...DEFAULTS,
			readableFont: true,
			pauseLiveUpdates: true
		});
	});

	test('an account that never saved keeps the copy', () => {
		localStorage.setItem(STORAGE_KEY, JSON.stringify({ ...DEFAULTS, underlineLinks: true }));
		initA11y();
		applyAccountA11y(null);
		expect(a11y.settings.underlineLinks).toBe(true);
		expect(a11y.account).toBe(true);
	});
});
