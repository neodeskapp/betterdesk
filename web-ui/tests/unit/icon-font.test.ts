/**
 * The console's icon font as it ships (static/fonts/MaterialSymbolsOutlined.woff2):
 * typed as text, every glyph name the console draws turns into one glyph
 * (the font's `liga` feature), and the font is the 20 px design of the
 * pinned Material Symbols source that scripts/build-material-symbols.py
 * names, the one the desktop client uses too.
 */
import fs from 'node:fs';
import path from 'node:path';
import { beforeAll, describe, expect, test } from 'vitest';
import { fontName, ligatureReader, woff2Tables } from '../../scripts/font-ligatures.ts';
import { collectCandidateIcons } from '../../scripts/icon-names.ts';

const root = path.resolve(__dirname, '../..');
const fonts = path.join(root, 'static/fonts');
const tables = woff2Tables(
	new Uint8Array(fs.readFileSync(path.join(fonts, 'MaterialSymbolsOutlined.woff2')))
);
const glyphOf = ligatureReader(tables);
const versionFile = fs.readFileSync(path.join(fonts, 'MATERIAL_SYMBOLS_VERSION.txt'), 'utf8');
const script = fs.readFileSync(path.join(root, 'scripts/build-material-symbols.py'), 'utf8');

function pinned(name: string): string {
	return new RegExp(`^${name} = "([^"]+)"`, 'm').exec(script)?.[1] ?? '';
}

function recorded(label: string): string {
	return new RegExp(`^${label}: (.+)$`, 'm').exec(versionFile)?.[1]?.trim() ?? '';
}

let scanned: Map<string, Set<string>>;

describe('the shipped icon font', () => {
	beforeAll(() => {
		scanned = collectCandidateIcons();
	}, 120_000);

	test('draws one glyph for every icon name the console uses', () => {
		const missing = [...scanned.keys()].filter((name) => glyphOf(name) === null);
		// Run `node scripts/update-material-symbols.ts` in web-ui when this fails
		expect(missing).toEqual([]);
		expect(scanned.size).toBeGreaterThan(100);
	});

	test('a name it does not hold stays letters', () => {
		expect(glyphOf('devices')).not.toBeNull();
		expect(glyphOf('not_an_icon_name')).toBeNull();
	});

	test('is the 20 px instance of the source the build script pins, as the version file records', () => {
		expect(pinned('COMMIT')).toMatch(/^[0-9a-f]{40}$/);
		expect(pinned('FONT_SHA256')).toMatch(/^[0-9a-f]{64}$/);
		expect(recorded('Upstream commit')).toBe(pinned('COMMIT'));
		expect(recorded('Source SHA-256')).toBe(pinned('FONT_SHA256'));
		expect(recorded('Font version')).toBe(fontName(tables, 5));
		expect(fontName(tables, 5)).toMatch(/^Version 2\.978/);
		expect(recorded('Axes')).toBe('FILL=0, GRAD=0, opsz=20, wght=400');
	});

	test('ships no Rounded font: nothing draws it', () => {
		expect(fs.existsSync(path.join(fonts, 'MaterialSymbolsRounded.woff2'))).toBe(false);
	});
});
