/**
 * Contrast of every colour pair the console uses, in every built-in
 * palette (WCAG 1.4.3 and 1.4.11): text 4.5:1 on each surface, borders,
 * the focus ring and chart series 3:1. The palettes are the server's
 * light and dark (config/theme-palettes.json) and every block in the
 * stylesheets that sets its own --bg and --text (the contrast modes).
 * A token or palette change that drops a pair below its limit fails here.
 */
import fs from 'node:fs';
import path from 'node:path';
import { describe, expect, test } from 'vitest';

const ROOT = path.resolve(__dirname, '../..');
const read = (p: string) => fs.readFileSync(path.join(ROOT, p), 'utf8');

type Rgba = [number, number, number, number];

/** Custom properties declared in a CSS block */
function declarations(block: string): Record<string, string> {
	const out: Record<string, string> = {};
	for (const m of block.matchAll(/(--[\w-]+)\s*:\s*([^;]+);/g)) out[m[1]!] = m[2]!.trim();
	return out;
}

/** The blocks of a stylesheet: selector and body (no nesting needed here) */
function blocks(css: string): { selector: string; body: string }[] {
	const clean = css.replace(/\/\*[\s\S]*?\*\//g, '');
	return [...clean.matchAll(/([^{}]+)\{([^{}]*)\}/g)].map((m) => ({ selector: m[1]!.trim(), body: m[2]! }));
}

function parseColor(value: string): Rgba | null {
	const v = value.trim().toLowerCase();
	if (v === 'transparent') return [0, 0, 0, 0];
	let m = /^#([0-9a-f]{3})$/.exec(v);
	if (m) return [...[...m[1]!].map((c) => parseInt(c + c, 16)), 1] as Rgba;
	m = /^#([0-9a-f]{6})([0-9a-f]{2})?$/.exec(v);
	if (m) {
		const n = m[1]!;
		return [
			parseInt(n.slice(0, 2), 16),
			parseInt(n.slice(2, 4), 16),
			parseInt(n.slice(4, 6), 16),
			m[2] ? parseInt(m[2], 16) / 255 : 1
		];
	}
	m = /^rgba?\(([^)]+)\)$/.exec(v);
	if (m) {
		const p = m[1]!
			.split(/[\s,/]+/)
			.filter(Boolean)
			.map(Number);
		return [p[0]!, p[1]!, p[2]!, p[3] ?? 1];
	}
	return null;
}

/** Resolve var() and the color-mix the tokens use, to a colour */
function resolve(value: string, vars: Record<string, string>, depth = 0): Rgba | null {
	if (depth > 20) return null;
	const v = value.trim();
	const ref = /^var\((--[\w-]+)\s*(?:,\s*(.+))?\)$/.exec(v);
	if (ref) {
		const own = vars[ref[1]!];
		if (own !== undefined) return resolve(own, vars, depth + 1);
		return ref[2] ? resolve(ref[2], vars, depth + 1) : null;
	}
	const mix = /^color-mix\(in srgb,\s*(.+?)\s+(\d+(?:\.\d+)?)%,\s*transparent\)$/.exec(v);
	if (mix) {
		const c = resolve(mix[1]!, vars, depth + 1);
		return c ? [c[0], c[1], c[2], c[3] * (Number(mix[2]) / 100)] : null;
	}
	return parseColor(v);
}

function over(top: Rgba, under: Rgba): Rgba {
	const a = top[3];
	return [0, 1, 2].map((i) => top[i]! * a + under[i]! * (1 - a)).concat(1) as Rgba;
}

function luminance([r, g, b]: Rgba): number {
	const f = (c: number) => {
		const s = c / 255;
		return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
	};
	return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b);
}

function ratio(fg: Rgba, bg: Rgba): number {
	const base = bg[3] < 1 ? over(bg, [255, 255, 255, 1]) : bg;
	const front = fg[3] < 1 ? over(fg, base) : fg;
	const [a, b] = [luminance(front), luminance(base)].sort((x, y) => y - x);
	return (a! + 0.05) / (b! + 0.05);
}

// The palette variables the server writes (boot.go VARS), by palette key
const PALETTE_VARS: Record<string, string> = {
	bgPrimary: '--bg-primary',
	bgSecondary: '--bg-secondary',
	bgTertiary: '--bg-tertiary',
	bgElevated: '--bg-elevated',
	textPrimary: '--text-primary',
	textSecondary: '--text-secondary',
	accentBlue: '--accent-blue',
	accentBlueHover: '--accent-blue-hover',
	accentGreen: '--accent-green',
	accentRed: '--accent-red',
	accentRedHover: '--accent-red-hover',
	accentYellow: '--accent-yellow',
	accentPurple: '--accent-purple',
	borderPrimary: '--border-primary',
	borderSecondary: '--border-secondary'
};

const tokensCss = read('src/lib/styles/tokens.css');
const tokenBlocks = blocks(tokensCss);
const rootTokens = declarations(
	tokenBlocks
		.filter((b) => b.selector === ':root')
		.map((b) => b.body)
		.join('\n')
);
const darkTokens = declarations(
	tokenBlocks
		.filter((b) => b.selector.split(',').some((s) => s.trim() === ":root[data-theme='dark']"))
		.map((b) => b.body)
		.join('\n')
);
const palettes = JSON.parse(
	read('../neodesk-server/console/static/files/config/theme-palettes.json')
) as Record<string, Record<string, string>>;

interface Palette {
	name: string;
	vars: Record<string, string>;
}

const all: Palette[] = [];
for (const mode of ['light', 'dark'] as const) {
	const vars: Record<string, string> = { ...rootTokens, ...(mode === 'dark' ? darkTokens : {}) };
	for (const [k, name] of Object.entries(PALETTE_VARS))
		if (palettes[mode]![k]) vars[name] = palettes[mode]![k]!;
	all.push({ name: mode, vars });
}
// Contrast modes: every block of the console's stylesheets that sets its own surface and text
for (const file of ['src/lib/styles/a11y.css', 'src/lib/styles/tokens.css', 'src/lib/styles/base.css']) {
	if (!fs.existsSync(path.join(ROOT, file))) continue;
	for (const b of blocks(read(file))) {
		const own = declarations(b.body);
		if (!own['--bg'] || !own['--text'] || b.selector === ':root') continue;
		const dark = /dark|black/.test(b.selector);
		all.push({ name: `${file} ${b.selector}`, vars: { ...rootTokens, ...(dark ? darkTokens : {}), ...own } });
	}
}

const SURFACES = ['--bg', '--bg-subtle', '--hover', '--bg-float'];
const TEXTS = ['--text', '--text-muted', '--accent', '--success', '--danger', '--warning', '--info'];
const UI_PARTS = ['--border', '--focus', '--presence-away'];
// Charts sit on the page and on its side panels, never on a hovered row
const SERIES = ['--series-1', '--series-2', '--series-3'];
const CHART_SURFACES = ['--bg', '--bg-subtle'];

function color(p: Palette, token: string, under?: Rgba): Rgba {
	const c = resolve(`var(${token})`, p.vars);
	if (!c) throw new Error(`${p.name}: ${token} does not resolve`);
	return under && c[3] < 1 ? over(c, under) : c;
}

describe.each(all)('palette $name', (p) => {
	const failures: string[] = [];
	const check = (fg: string, bg: string, min: number, fgOn?: string) => {
		const surface = color(p, bg, bg === '--hover' ? color(p, '--bg') : undefined);
		const base = fgOn ? over(color(p, fgOn), color(p, '--bg')) : surface;
		const front = color(p, fg, base);
		const r = ratio(front, fgOn ? base : surface);
		if (r < min) failures.push(`${fg} on ${fgOn ?? bg}: ${r.toFixed(2)} < ${min}`);
	};

	test('text reaches 4.5:1 on every surface', () => {
		for (const s of SURFACES) for (const t of TEXTS) check(t, s, 4.5);
		// Text on the accent, and on the translucent tint of a selected item
		check('--on-accent', '--accent', 4.5);
		check('--on-accent', '--accent-hover', 4.5);
		check('--text', '--bg', 4.5, '--accent-tint');
		check('--text-muted', '--bg', 4.5, '--accent-tint');
		expect(failures).toEqual([]);
	});

	test('borders, the focus ring and chart colours reach 3:1 on every surface', () => {
		failures.length = 0;
		for (const s of SURFACES) for (const u of UI_PARTS) check(u, s, 3);
		for (const s of CHART_SURFACES) for (const u of SERIES) check(u, s, 3);
		expect(failures).toEqual([]);
	});
});

test('the check finds every palette: light, dark and the contrast modes', () => {
	expect(all.map((p) => p.name).slice(0, 2)).toEqual(['light', 'dark']);
	expect(all.length).toBeGreaterThanOrEqual(2);
	// It can fail: a pale grey on white is caught
	expect(ratio(parseColor('#999999')!, parseColor('#ffffff')!)).toBeLessThan(4.5);
	expect(ratio(parseColor('#000000')!, parseColor('#ffffff')!)).toBeCloseTo(21, 0);
});
