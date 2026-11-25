/**
 * Old console addresses and where they lead now (console structure plan,
 * section 7). This is the one table: the app follows it for links inside
 * the app (routes/+layout.svelte), and the build writes it into
 * redirects.json (vite-plugins/routes.ts), which the server reads to answer
 * an old address it is asked for first: a bookmark, a link in a mail or one
 * typed in (neodesk-server/console/webui/redirects.go). No DOM, unit
 * tested; tests/unit/redirects.test.ts and the Go tests check that both
 * sides read it the same way.
 *
 * Each entry: the old path (any letter case), the ?tab= value it needs when
 * only that tab moved (any letter case; the new address leaves it out), and
 * the new address. The other parameters of the old address come along
 * after the new one's own; the new address's #id wins over the old one.
 * Old addresses stay for two releases, then they are reviewed.
 */

export interface Redirect {
	readonly from: string;
	readonly tab?: string;
	readonly to: string;
}

export const REDIRECTS: readonly Redirect[] = [
	// Server section (plan step 6)
	{ from: '/settings', tab: 'backup', to: '/server/backups' },
	{ from: '/settings', tab: 'updates', to: '/server/updates' },
	{ from: '/settings', tab: 'advanced', to: '/server/config-files' }
];

const same = (a: string, b: string) => a.toLowerCase() === b.toLowerCase();

/**
 * The new address of an old one (path without the base, the query with or
 * without "?", the #id with or without "#"), or null when it did not move.
 */
export function redirectTarget(path: string, search = '', hash = ''): string | null {
	const old = new URLSearchParams(search);
	const tab = old.get('tab');
	for (const r of REDIRECTS) {
		if (!same(path, r.from)) continue;
		if (r.tab !== undefined && (tab === null || !same(tab, r.tab))) continue;
		const [address = '', ownHash = ''] = r.to.split('#');
		const [toPath = '', toQuery = ''] = address.split('?');
		const params = new URLSearchParams(toQuery);
		const own = new Set(params.keys());
		for (const [key, value] of old) {
			if (own.has(key) || (r.tab !== undefined && key === 'tab')) continue;
			params.append(key, value);
		}
		const query = params.toString();
		const oldId = hash.replace(/^#/, '');
		const id = ownHash || oldId;
		return `${toPath}${query ? `?${query}` : ''}${id ? `#${id}` : ''}`;
	}
	return null;
}
