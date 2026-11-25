/**
 * Every console address the code writes lands on a page or on a redirect
 * (console structure plan, step 10). The addresses come from the Go server
 * (mail texts, the command line's output, notifications, the setup guide,
 * the navigation), from the console's language files and from the web
 * app's own links (link(), href, goto). A page is a route of src/routes; a
 * redirect is an entry of lib/redirects.ts. On /settings the ?tab= has to
 * be a tab (or a redirect) and a #<id> an element of the settings list.
 *
 * What counts as a console address: a path whose first part is a page of
 * the console or an old address of the redirect table, at the start of a
 * text, after a space, a quote, a bracket or a host. Route registrations
 * of the Go server (r.Get("/..."), and paths under /api) are not
 * addresses.
 */
import fs from 'node:fs';
import path from 'node:path';
import { describe, expect, test } from 'vitest';
import { REDIRECTS, redirectTarget } from '../../src/lib/redirects.ts';
import { ID_ALIASES } from '../../src/lib/settings/reveal.ts';
import { isTab, placeOfId } from '../../src/lib/settings/tabs.ts';

const webUi = path.resolve(__dirname, '../..');
const server = path.resolve(webUi, '../neodesk-server');

function walk(
	dir: string,
	keep: (file: string) => boolean,
	skip: (dir: string) => boolean = () => false
): string[] {
	const out: string[] = [];
	for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
		const full = path.join(dir, entry.name);
		if (entry.isDirectory()) {
			if (!skip(full) && entry.name !== 'node_modules') out.push(...walk(full, keep, skip));
		} else if (keep(full)) out.push(full);
	}
	return out;
}

/* ── The pages: one pattern per route folder ── */

const routesDir = path.join(webUi, 'src/routes');
const pagePatterns = walk(routesDir, (f) => /\+page\.(svelte|ts)$/.test(f)).map((file) => {
	const segments = path
		.relative(routesDir, path.dirname(file))
		.split(path.sep)
		.filter((s) => s && !/^\(.*\)$/.test(s));
	let source = '^';
	for (const s of segments) {
		if (/^\[\[.+\]\]$/.test(s)) source += '(?:/[^/]+)?';
		else if (/^\[\.\.\..+\]$/.test(s)) source += '(?:/.*)?';
		else if (/^\[.+\]$/.test(s)) source += '/[^/]+';
		else source += '/' + s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
	}
	return new RegExp((source === '^' ? '^/' : source) + '/?$', 'i');
});
const firstParts = new Set<string>();
for (const file of walk(routesDir, (f) => /\+page\.(svelte|ts)$/.test(f))) {
	const first = path
		.relative(routesDir, path.dirname(file))
		.split(path.sep)
		.find((s) => s && !/^\(.*\)$/.test(s));
	if (first && !first.startsWith('[')) firstParts.add(first);
}
for (const r of REDIRECTS) firstParts.add(r.from.split('/')[1]!);

/** Why an address does not land, or null when it does */
function problem(address: string): string | null {
	const url = new URL(address, 'http://console.test');
	if (redirectTarget(url.pathname, url.search, url.hash) !== null) return null;
	if (!pagePatterns.some((re) => re.test(url.pathname))) return 'no page and no redirect';
	if (url.pathname.toLowerCase() === '/settings') {
		const tab = url.searchParams.get('tab');
		if (tab !== null && !isTab(tab.toLowerCase())) return `no settings tab "${tab}"`;
		const id = decodeURIComponent(url.hash.slice(1));
		if (id && !placeOfId(id) && !Object.hasOwn(ID_ALIASES, id) && !isTab(id) && id !== 'server')
			return `no setting "#${id}"`;
	}
	return null;
}

/* ── Finding addresses in text ── */

const PARTS = [...firstParts].sort((a, b) => b.length - a.length).map((p) => p.replace(/[-]/g, '\\-'));
const ADDRESS = new RegExp(
	String.raw`(?:^|[\s"'(\x60=>]|https?://[^/\s"'\x60]+|\}\}|%s)(/(?:${PARTS.join('|')})(?![A-Za-z0-9_-])(?:/[A-Za-z0-9_.%{}:-]+)*(?:\?[A-Za-z0-9_=&%.-]*)?(?:#[A-Za-z0-9_-]+)?)`,
	'g'
);

interface Found {
	where: string;
	address: string;
}

/** Placeholders of the code stand for one value: /devices/{id}, /devices?open=%s */
const tidy = (a: string) =>
	a
		.replace(/\{[^}]*\}/g, 'x')
		.replace(/%[sdvq]/g, 'x')
		.replace(/[.:]+$/, '');

function addressesIn(text: string, where: string): Found[] {
	const out: Found[] = [];
	for (const m of text.matchAll(ADDRESS)) {
		// "/d/" + id: a value follows
		const next = text[(m.index ?? 0) + m[0].length];
		// A one-letter part alone is a command-line switch (cmd /d /s) or a prefix, not a page
		if (/^\/[a-z]$/.test(m[1]!) && next !== '/') continue;
		out.push({ where, address: tidy(m[1]! + (next === '/' ? '/x' : '')) });
	}
	return out;
}

/**
 * A line that registers a route of the server, or one that calls the Go
 * API (relay, fetch, call, proxy, apiGet and apiPost, goapipath, an
 * organization's item path): its paths are not addresses for people.
 */
const ROUTE_CALL =
	/\.(Get|Post|Put|Patch|Delete|Head|Options|Handle|HandleFunc|Route|Group|Mount|Method|Any)\(|\b(goapipath\.|goapi\.|api(Get|Post|Put|Delete)\(|h\.(relay|fetch|call|proxy)\(|orgItemPath\()|\bpath :?=\s*$/;

function goAddresses(): Found[] {
	const files = walk(
		server,
		(f) => f.endsWith('.go') && !f.endsWith('_test.go'),
		// goapi is the console's client of the Go API: its paths are API paths
		(d) => /[\\/](testdata|contract|devseed|repotool|goapi|node_modules)$/.test(d)
	);
	const out: Found[] = [];
	for (const file of files) {
		const src = fs.readFileSync(file, 'utf8');
		const rel = path.relative(server, file).split(path.sep).join('/');
		// "..." and `...` literals; comments are not read
		const literal = /"((?:[^"\\\n]|\\.)*)"|`([^`]*)`/g;
		for (const m of src.matchAll(literal)) {
			const line = src.slice(src.lastIndexOf('\n', m.index) + 1, m.index);
			if (/^\s*\/\//.test(line) || ROUTE_CALL.test(line)) continue;
			const text = m[1] ?? m[2] ?? '';
			if (/^\/api(\/|$)/.test(text)) continue;
			const n = src.slice(0, m.index).split('\n').length;
			out.push(...addressesIn(text, `${rel}:${n}`));
		}
	}
	return out;
}

function langAddresses(): Found[] {
	const dir = path.join(server, 'console/static/files/lang');
	const out: Found[] = [];
	const walkJson = (v: unknown, key: string) => {
		if (typeof v === 'string') out.push(...addressesIn(v, `en.json ${key}`));
		else if (v && typeof v === 'object')
			for (const [k, x] of Object.entries(v)) walkJson(x, key ? `${key}.${k}` : k);
	};
	walkJson(JSON.parse(fs.readFileSync(path.join(dir, 'en.json'), 'utf8')), '');
	return out;
}

function webAddresses(): Found[] {
	const files = walk(path.join(webUi, 'src'), (f) => /\.(ts|svelte)$/.test(f));
	const out: Found[] = [];
	const call =
		/\b(?:link|goto|resolve|go2|navigate)\(\s*(['"`])(\/[^'"`$]*)\1|\bhref="(\/[^"{]*)"|\bhref:\s*'(\/[^']*)'/g;
	for (const file of files) {
		if (file.endsWith('redirects.ts')) continue;
		const src = fs.readFileSync(file, 'utf8');
		const rel = path.relative(webUi, file).split(path.sep).join('/');
		for (const m of src.matchAll(call)) {
			const address = m[2] ?? m[3] ?? m[4] ?? '';
			// resolve('/(console)/...') names a route folder, which the type check holds to its route
			if (/^\/(api|ws|_ui|fonts|img)(\/|$)|^\/\(/.test(address)) continue;
			const n = src.slice(0, m.index).split('\n').length;
			out.push({ where: `${rel}:${n}`, address: tidy(address) });
		}
	}
	return out;
}

const report = (found: Found[]) =>
	found.flatMap((f) => {
		const why = problem(f.address);
		return why ? [`${f.where}: ${f.address} (${why})`] : [];
	});

describe('every console address lands on a page or a redirect', () => {
	test('the rule itself: pages, redirects, settings tabs and ids', () => {
		expect(problem('/devices?open=900100001')).toBeNull();
		expect(problem('/d/abc123')).toBeNull();
		expect(problem('/settings?tab=backup')).toBeNull();
		expect(problem('/settings?tab=ldap#ldap-host')).toBeNull();
		expect(problem('/settings#public-endpoints')).toBeNull();
		expect(problem('/client-branding')).toBeNull();
		expect(problem('/no-such-page')).toBe('no page and no redirect');
		expect(problem('/settings?tab=nonsense')).toBe('no settings tab "nonsense"');
		expect(problem('/settings?tab=auth#nothing-like-this')).toBe('no setting "#nothing-like-this"');
	});

	test('finding addresses: in plain text, after a host and in a format; not under /api', () => {
		const found = addressesIn(
			'Open https://console.example/tokens or /settings?tab=auth#ldap-host, then %s/devices?open=%s. Not /api/devices or /tokensx.',
			'x'
		).map((f) => f.address);
		expect(found).toEqual(['/tokens', '/settings?tab=auth#ldap-host', '/devices?open=x']);
	});

	test('in the Go server', { timeout: 60_000 }, () => {
		const found = goAddresses();
		expect(found.length).toBeGreaterThan(50);
		expect(report(found)).toEqual([]);
	});

	test('in the language files', () => {
		expect(report(langAddresses())).toEqual([]);
	});

	test("in the web app's own links", { timeout: 60_000 }, () => {
		const found = webAddresses();
		expect(found.length).toBeGreaterThan(50);
		expect(report(found)).toEqual([]);
	});
});
