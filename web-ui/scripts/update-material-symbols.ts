/**
 * Maintainer-only: rebuild the console's self-hosted Material Symbols
 * subset. Nothing in the server or the console runs it.
 *
 * Usage (in web-ui, Node 22.18 or newer runs TypeScript as it is; Python 3
 * with fonttools and brotli for the font itself):
 *   node scripts/update-material-symbols.ts
 *
 * The icon names come from scripts/icon-names.ts, which scans the console
 * (src and the remote viewer's scripts in static) and the icons the server
 * sends it (the navigation in neodesk-server/console/access/nav.go). Its
 * header says which ways of writing a name it reads. Names Material
 * Symbols doesn't have are reported and left out. The font is built by
 * scripts/build-material-symbols.py from the pinned source (the commit and
 * SHA-256 are there), which also writes MATERIAL_SYMBOLS_VERSION.txt and the
 * ?v= cache busters in src/lib/styles/base.css and src/app.html.
 * tests/unit/icons.test.ts and icon-font.test.ts check the result.
 */
/* eslint-disable no-console -- a command-line tool that reports its progress */

import crypto from 'node:crypto';
import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { collectCandidateIcons } from './icon-names.ts';

const UI = path.join(path.dirname(fileURLToPath(import.meta.url)), '..');
const BUILD = path.join(UI, 'scripts', 'build-material-symbols.py');
const CACHE = path.join(UI, 'scripts', '.cache');

const NAME_RE = /^[a-z][a-z0-9_]{1,48}$/;

/** A pin of the build script (COMMIT, CODEPOINTS_SHA256) */
function pin(name: string): string {
	const value = new RegExp(`^${name} = "([^"]+)"`, 'm').exec(fs.readFileSync(BUILD, 'utf8'))?.[1];
	if (!value) throw new Error(`${name} is not pinned in ${BUILD}`);
	return value;
}

/** The names Material Symbols has, from the codepoints of the pinned commit */
export async function loadValidIconNames(): Promise<Set<string>> {
	const commit = pin('COMMIT');
	const file = path.join(CACHE, `MaterialSymbolsOutlined-${commit.slice(0, 12)}.codepoints`);
	fs.mkdirSync(CACHE, { recursive: true });
	if (!fs.existsSync(file)) {
		const url =
			`https://raw.githubusercontent.com/google/material-design-icons/${commit}/variablefont/` +
			'MaterialSymbolsOutlined%5BFILL%2CGRAD%2Copsz%2Cwght%5D.codepoints';
		console.log(`Downloading ${url}`);
		const res = await fetch(url);
		if (!res.ok) throw new Error(`Failed to fetch codepoints: HTTP ${res.status}`);
		fs.writeFileSync(file, Buffer.from(await res.arrayBuffer()));
	}
	const bytes = fs.readFileSync(file);
	const sha = crypto.createHash('sha256').update(bytes).digest('hex');
	if (sha !== pin('CODEPOINTS_SHA256')) throw new Error(`${file}: SHA-256 ${sha} is not the pinned one`);
	const valid = new Set<string>();
	for (const line of bytes.toString('utf8').split(/\r?\n/)) {
		const name = line.split(/\s+/)[0];
		if (name && NAME_RE.test(name)) valid.add(name);
	}
	return valid;
}

/** Candidates that are real Material Symbols names, sorted; the rest is reported. */
export function collectIcons(valid: Set<string>, candidates = collectCandidateIcons()): string[] {
	const icons: string[] = [];
	const skipped: string[] = [];
	for (const [name, files] of candidates) {
		if (valid.has(name)) icons.push(name);
		else skipped.push(`${name} (${[...files].join(', ')})`);
	}
	icons.sort((a, b) => a.localeCompare(b));
	if (skipped.length) {
		// An icon place holds a name the font doesn't know: a typo, or a word the scan shouldn't read.
		// icons.test.ts fails until it is fixed.
		console.warn(`Not Material Symbols names, left out:\n  ${skipped.join('\n  ')}`);
	}
	return icons;
}

async function main(): Promise<void> {
	const icons = collectIcons(await loadValidIconNames());
	if (icons.length < 20 || !icons.includes('expand_more') || !icons.includes('devices')) {
		throw new Error(
			`The scan found ${icons.length} icons without expand_more or devices; check icon-names.ts.`
		);
	}
	console.log(`Collected ${icons.length} Material Symbols names.`);
	const list = path.join(CACHE, 'console-icon-names.txt');
	fs.writeFileSync(list, icons.join('\n') + '\n', 'utf8');
	const python = process.env.PYTHON || (process.platform === 'win32' ? 'python' : 'python3');
	const run = spawnSync(python, [BUILD, '--names', list], { cwd: UI, stdio: 'inherit' });
	if (run.status !== 0) throw new Error(`${path.basename(BUILD)} failed (exit ${run.status})`);
	console.log('Done. Review the font and the version file, then commit them with the CSS and app.html.');
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
	main().catch((err: unknown) => {
		console.error(err);
		process.exit(1);
	});
}
