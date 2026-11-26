import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import { STORAGE_KEY, V1_KEY, applyToRoot, load } from '../../src/lib/shell/a11y.svelte.ts';
import { writePref } from '../../src/lib/util/storage.ts';

// /console-boot.js applies the accessibility settings before the first
// paint (neodesk-server/console/routes/console/boot.go). Run the script
// from its Go source and check that it puts on <html> what the app does.
const source = readFileSync(
	resolve(__dirname, '../../../neodesk-server/console/routes/console/boot.go'),
	'utf8'
);

function goString(name: string): string {
	const start = source.indexOf(`const ${name} = \``);
	if (start < 0) throw new Error(`no ${name} in boot.go`);
	const from = start + `const ${name} = \``.length;
	let text = '';
	let i = from;
	// A Go raw string, possibly joined with other constants: ` + name + `
	for (;;) {
		const end = source.indexOf('`', i);
		text += source.slice(i, end);
		const rest = source.slice(end + 1);
		const joined = /^ \+ (\w+) \+ `/.exec(rest);
		if (!joined) break;
		text += goString(joined[1]!);
		i = end + 1 + joined[0].length;
	}
	return text;
}

const boot = {
	lang: 'en',
	dir: 'ltr',
	themeMode: 'light',
	palettes: { light: null, dark: null },
	stringsUrl: '/api/console/strings/en',
	publicPaths: ['/login']
};
const script = goString('bootScriptHead') + JSON.stringify(boot) + ');';

const root = document.documentElement;
const ATTRS = ['a11yReduceMotion', 'a11yMoreContrast', 'a11yUnderlineLinks', 'a11yReadableFont'] as const;

function stubSystem(answers: Record<string, boolean>) {
	vi.stubGlobal('matchMedia', (query: string) => ({
		matches: answers[query] ?? false,
		media: query,
		addEventListener: () => {},
		removeEventListener: () => {}
	}));
}

function runBoot(): Record<string, string | undefined> {
	for (const a of ATTRS) root.removeAttribute(`data-${a.replace(/[A-Z]/g, (m) => '-' + m.toLowerCase())}`);
	new Function(script)();
	return Object.fromEntries(ATTRS.map((a) => [a, root.dataset[a]]));
}

function runApp(): Record<string, string | undefined> {
	for (const a of ATTRS) root.removeAttribute(`data-${a.replace(/[A-Z]/g, (m) => '-' + m.toLowerCase())}`);
	applyToRoot(load());
	return Object.fromEntries(ATTRS.map((a) => [a, root.dataset[a]]));
}

beforeEach(() => {
	vi.stubGlobal(
		'fetch',
		vi.fn(() => new Promise(() => {}))
	);
	localStorage.clear();
	writePref(STORAGE_KEY, null);
	writePref(V1_KEY, null);
});
afterEach(() => vi.unstubAllGlobals());

const cases: { name: string; v2?: unknown; v1?: unknown; system?: Record<string, boolean> }[] = [
	{ name: 'nothing stored', system: {} },
	{
		name: 'nothing stored, the system asks for less motion and more contrast',
		system: { '(prefers-reduced-motion: reduce)': true, '(prefers-contrast: more)': true }
	},
	{
		name: 'on overrides the system',
		v2: { reduceMotion: 'on', moreContrast: 'on', underlineLinks: true },
		system: {}
	},
	{
		name: 'off overrides the system',
		v2: { reduceMotion: 'off', moreContrast: 'off', readableFont: true },
		system: { '(prefers-reduced-motion: reduce)': true, '(prefers-contrast: more)': true }
	},
	{
		name: 'unknown values follow the system',
		v2: { reduceMotion: 'maybe', readableFont: 'yes' },
		system: { '(prefers-reduced-motion: reduce)': true }
	},
	{
		name: 'the old panel',
		v1: { reduceMotion: true, contrast: 'yellow-black', dyslexiaFont: true, fontScale: 140 },
		system: {}
	},
	{
		name: 'the old panel with pause animations',
		v1: { pauseAnimations: true, underlineLinks: true, contrast: 'normal' },
		system: {}
	}
];

for (const c of cases) {
	test(`the boot script and the app agree: ${c.name}`, () => {
		stubSystem(c.system ?? {});
		const store = () => {
			localStorage.clear();
			writePref(STORAGE_KEY, null);
			writePref(V1_KEY, null);
			if (c.v2) localStorage.setItem(STORAGE_KEY, JSON.stringify(c.v2));
			if (c.v1) localStorage.setItem(V1_KEY, JSON.stringify(c.v1));
		};
		store();
		const fromBoot = runBoot();
		store();
		const fromApp = runApp();
		expect(fromBoot).toEqual(fromApp);
		for (const a of ATTRS) expect(fromBoot[a]).toMatch(/^(true|false)$/);
	});
}

test('the old settings show before the app moves them', () => {
	stubSystem({});
	localStorage.setItem(
		V1_KEY,
		JSON.stringify({ reduceMotion: true, contrast: 'high-dark', underlineLinks: true })
	);
	expect(runBoot()).toEqual({
		a11yReduceMotion: 'true',
		a11yMoreContrast: 'true',
		a11yUnderlineLinks: 'true',
		a11yReadableFont: 'false'
	});
});

test('blocked storage: the defaults, no error', () => {
	stubSystem({ '(prefers-contrast: more)': true });
	const spy = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
		throw new Error('blocked');
	});
	expect(runBoot()).toEqual({
		a11yReduceMotion: 'false',
		a11yMoreContrast: 'true',
		a11yUnderlineLinks: 'false',
		a11yReadableFont: 'false'
	});
	spy.mockRestore();
});
