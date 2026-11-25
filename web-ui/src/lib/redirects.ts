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
 * only that tab moved (any letter case; the new address leaves it out),
 * other parameters it needs with their values (any letter case; the new
 * address leaves them out), the #ids it needs when only some settings of a
 * page moved (any letter case; the new address leaves the old tab out), and
 * the new address. The other
 * parameters of the old address come along after the new one's own; the
 * new address's #id wins over the old one. Old addresses stay for two
 * releases, then they are reviewed.
 *
 * A browser never sends the #id to the server, so an entry with #ids is
 * the app's alone: it follows them for links inside the app and for an
 * address it is opened with (routes/+layout.svelte); the server skips them.
 */

export interface Redirect {
	readonly from: string;
	readonly tab?: string;
	readonly query?: Readonly<Record<string, string>>;
	readonly hash?: readonly string[];
	readonly to: string;
}

/** The old ids of your password on Settings › Sign-in */
const PASSWORD_IDS = [
	'account-password',
	'current-password',
	'new-password',
	'confirm-password',
	'password-form'
];

export const REDIRECTS: readonly Redirect[] = [
	// Server section (plan step 6)
	{ from: '/settings', tab: 'backup', to: '/server/backups' },
	{ from: '/settings', tab: 'updates', to: '/server/updates' },
	{ from: '/settings', tab: 'advanced', to: '/server/config-files' },
	// Activity (plan step 9): Security checks leave Security and audit, which
	// keeps the audit log; Billing is one page with its tabs
	{ from: '/security-audit', tab: 'overview', to: '/security-checks?tab=checks' },
	{ from: '/security-audit', tab: 'hardening', to: '/security-checks?tab=hardening' },
	{ from: '/security-audit', tab: 'vulnerabilities', to: '/security-checks?tab=hardening' },
	{ from: '/commercialization', to: '/billing' },
	// Branding (plan step 5): Client app branding is a part of Settings › Branding
	{ from: '/client-branding', to: '/settings?tab=brand#client-app' },
	// Your account (plan step 2): your password, two-factor sign-in, sessions
	// and API tokens left Settings › Sign-in, the Audit log and /api-tokens
	{ from: '/settings', hash: PASSWORD_IDS, to: '/account#password' },
	{ from: '/settings', hash: ['auth-panel-totp'], to: '/account#two-factor' },
	{ from: '/security-audit', tab: 'sessions', to: '/account#sessions' },
	{ from: '/api-tokens', to: '/account#api-tokens' },
	// Add computers (plan step 8): every way a computer gets in is a tab of
	// Devices › Add computers; Enter a pairing code opens its dialog there
	{ from: '/keys', to: '/devices/add?tab=manual' },
	{ from: '/tokens', to: '/devices/add?tab=tokens' },
	{ from: '/pairing', to: '/devices/add?tab=pairing' },
	{ from: '/agents', to: '/devices/add?tab=agents' },
	{ from: '/generator', to: '/devices/add?tab=agents' },
	{ from: '/', query: { activateAgent: '1' }, to: '/devices/add?tab=pairing&pair=1' }
];

const same = (a: string, b: string) => a.toLowerCase() === b.toLowerCase();

/** "#account-password" → "account-password" (decoded; a broken escape stays as it is) */
function decodeId(hash: string): string {
	const raw = hash.replace(/^#/, '');
	try {
		return decodeURIComponent(raw);
	} catch {
		return raw;
	}
}

/**
 * The new address of an old one (path without the base, the query with or
 * without "?", the #id with or without "#"), or null when it did not move.
 */
export function redirectTarget(path: string, search = '', hash = ''): string | null {
	const old = new URLSearchParams(search);
	const tab = old.get('tab');
	const oldId = decodeId(hash);
	for (const r of REDIRECTS) {
		if (!same(path, r.from)) continue;
		if (r.tab !== undefined && (tab === null || !same(tab, r.tab))) continue;
		if (r.hash && !r.hash.some((id) => same(id, oldId))) continue;
		const needs = Object.entries(r.query ?? {});
		if (needs.some(([key, value]) => !same(old.get(key) ?? '', value) || !old.has(key))) continue;
		const dropTab = r.tab !== undefined || r.hash !== undefined;
		const [address = '', ownHash = ''] = r.to.split('#');
		const [toPath = '', toQuery = ''] = address.split('?');
		const params = new URLSearchParams(toQuery);
		const own = new Set(params.keys());
		for (const [key, value] of old) {
			if (own.has(key) || (dropTab && key === 'tab') || Object.hasOwn(r.query ?? {}, key)) continue;
			params.append(key, value);
		}
		const query = params.toString();
		const id = ownHash || hash.replace(/^#/, '');
		return `${toPath}${query ? `?${query}` : ''}${id ? `#${id}` : ''}`;
	}
	return null;
}
