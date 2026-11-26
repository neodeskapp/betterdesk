import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { expect, test } from 'vitest';

// The accessibility panel lost 14 of its 20 controls (profiles, font size,
// spacing sliders, colour filters, saturation, highlights, dim media,
// large cursor, reading guide and mask, strong focus, pause animations,
// yellow on black). Nothing of them may be left half working: no CSS
// rule, variable, attribute, boot script part or string.
const repo = resolve(__dirname, '../../..');

function files(dir: string, ext: RegExp): string[] {
	const out: string[] = [];
	for (const name of readdirSync(dir)) {
		const p = join(dir, name);
		if (statSync(p).isDirectory()) out.push(...files(p, ext));
		else if (ext.test(name)) out.push(p);
	}
	return out;
}

const REMOVED = [
	/data-a11y-(contrast|font-scaled|active|filtered|strong-focus|highlight-links|highlight-headings|hide-media|large-cursor|pause-animations|reading-guide|reading-mask|dyslexia)\b/,
	/a11y(Contrast|FontScaled|Active|Filtered|StrongFocus|HighlightLinks|HighlightHeadings|HideMedia|LargeCursor|PauseAnimations|ReadingGuide|ReadingMask|Dyslexia)\b/,
	/--bd-a11y-/,
	/--layer-reading/,
	/--font-scale/,
	/nd-a11y-(modal|filter-defs|reading-guide|reading-mask)/,
	/bd-a11y-(protanopia|deuteranopia|tritanopia|achromatopsia)/
];

test('no code of the removed options is left in the web app or the boot script', () => {
	const sources = [
		...files(join(repo, 'web-ui/src'), /\.(svelte|ts|css|html)$/),
		join(repo, 'neodesk-server/console/routes/console/boot.go')
	];
	const found: string[] = [];
	for (const f of sources) {
		const text = readFileSync(f, 'utf8');
		for (const re of REMOVED) {
			const m = re.exec(text);
			if (m) found.push(`${f.slice(repo.length + 1)}: ${m[0]}`);
		}
	}
	expect(found).toEqual([]);
}, 60_000);

test('the strings of the removed options are gone from every language', () => {
	const kept = [
		'title',
		'reduce_motion',
		'reduce_motion_hint',
		'more_contrast',
		'more_contrast_hint',
		'follow_system',
		'choice_on',
		'choice_off',
		'underline_links',
		'underline_links_hint',
		'readable_font',
		'readable_font_hint',
		'single_key_shortcuts',
		'single_key_shortcuts_hint',
		'pause_live_updates',
		'pause_live_updates_hint',
		'subtitle_account',
		'subtitle_browser',
		'saved',
		'save_failed',
		'done'
	].sort();
	const dir = join(repo, 'neodesk-server/console/static/files/lang');
	for (const f of readdirSync(dir).filter((n) => n.endsWith('.json'))) {
		const strings = JSON.parse(readFileSync(join(dir, f), 'utf8')) as {
			accessibility: Record<string, string>;
		};
		expect([f, Object.keys(strings.accessibility).sort()]).toEqual([f, kept]);
	}
}, 60_000);
