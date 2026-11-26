/**
 * Settings › Sign-in without a DOM: password rules, LDAP and OIDC values
 * as the form holds them and as the server takes them (old settings.js
 * populateLdapForm / collectLdapData, populateOidcForm / collectOidcData,
 * isValidOidcRedirectUrlClient).
 */

import { tf } from '#lib/i18n/i18n.svelte.ts';

export interface PasswordRule {
	id: 'length';
	met: boolean;
}

/** The server's password rule: at least 8 characters, nothing else */
export function passwordRules(password: string): PasswordRule[] {
	return [{ id: 'length', met: password.length >= 8 }];
}

const str = (v: unknown, fallback = ''): string =>
	v === null || v === undefined || v === '' ? fallback : String(v);
const num = (v: unknown, fallback: number): number => {
	const n = parseInt(String(v ?? ''), 10);
	return Number.isFinite(n) && n > 0 ? n : fallback;
};
/** A number field as typed; an empty one is no value (the server's default) */
const typed = (v: unknown): number | null =>
	v === null || v === undefined || v === '' || (typeof v === 'number' && Number.isNaN(v)) ? null : Number(v);

/** A whole number from min to max, or empty when empty may be */
export function wholeIn(v: unknown, min: number, max: number, emptyOk = false): boolean {
	const n = typed(v);
	if (n === null) return emptyOk;
	return Number.isInteger(n) && n >= min && n <= max;
}

export interface LdapValues {
	enabled: boolean;
	host: string;
	port: number | null;
	use_tls: boolean;
	start_tls: boolean;
	skip_tls_verify: boolean;
	conn_timeout_sec: number | null;
	direct_bind: boolean;
	bind_dn: string;
	bind_password: string;
	base_dn: string;
	user_filter: string;
	direct_bind_dn: string;
	user_attr_id: string;
	user_attr_email: string;
	user_attr_name: string;
	group_base_dn: string;
	group_filter: string;
	group_attr_name: string;
	default_role: string;
	group_role_map: string;
	/** Auto-create users: a person the directory accepts gets an account */
	allow_signup: boolean;
}

export function ldapFromServer(d: Record<string, unknown>): LdapValues {
	return {
		enabled: !!d.enabled,
		host: str(d.host),
		port: num(d.port, 389),
		use_tls: !!d.use_tls,
		start_tls: !!d.start_tls,
		skip_tls_verify: !!d.skip_tls_verify,
		conn_timeout_sec: num(d.conn_timeout_sec, 10),
		direct_bind: !!d.direct_bind,
		bind_dn: str(d.bind_dn),
		bind_password: str(d.bind_password),
		base_dn: str(d.base_dn),
		user_filter: str(d.user_filter),
		direct_bind_dn: str(d.direct_bind_dn),
		user_attr_id: str(d.user_attr_id),
		user_attr_email: str(d.user_attr_email),
		user_attr_name: str(d.user_attr_name),
		group_base_dn: str(d.group_base_dn),
		group_filter: str(d.group_filter),
		group_attr_name: str(d.group_attr_name),
		default_role: str(d.default_role, 'viewer'),
		group_role_map: str(d.group_role_map),
		allow_signup: d.allow_signup !== false
	};
}

/**
 * What PUT /api/settings/ldap and the test take: trimmed text, numbers as
 * typed (the server refuses one out of range; an empty one is its default)
 */
export function ldapPayload(v: LdapValues) {
	return {
		enabled: v.enabled,
		host: v.host.trim(),
		port: typed(v.port),
		use_tls: v.use_tls,
		start_tls: v.start_tls,
		skip_tls_verify: v.skip_tls_verify,
		bind_dn: v.bind_dn.trim(),
		bind_password: v.bind_password.trim(),
		base_dn: v.base_dn.trim(),
		user_filter: v.user_filter.trim(),
		user_attr_id: v.user_attr_id.trim(),
		user_attr_email: v.user_attr_email.trim(),
		user_attr_name: v.user_attr_name.trim(),
		group_base_dn: v.group_base_dn.trim(),
		group_filter: v.group_filter.trim(),
		group_attr_name: v.group_attr_name.trim(),
		default_role: v.default_role.trim(),
		group_role_map: v.group_role_map.trim(),
		direct_bind: v.direct_bind,
		direct_bind_dn: v.direct_bind_dn.trim(),
		conn_timeout_sec: typed(v.conn_timeout_sec),
		allow_signup: v.allow_signup
	};
}

/** The keys of what is wrong with LDAP numbers, by field; the server checks the rest */
export function ldapNumberProblems(v: Pick<LdapValues, 'enabled' | 'port' | 'conn_timeout_sec'>) {
	if (!v.enabled) return {};
	return {
		port: wholeIn(v.port, 1, 65535, true) ? undefined : 'settings.signin.check_port_range',
		conn_timeout_sec: wholeIn(v.conn_timeout_sec, 1, 120, true)
			? undefined
			: 'settings.signin.check_timeout_range'
	};
}

export interface SessionValues {
	expiry_days: number | null;
	sliding: boolean;
	max_days: number | null;
}

/** The keys of what is wrong with the client session days, by field */
export function sessionProblems(v: SessionValues) {
	const expiry = wholeIn(v.expiry_days, 1, 365) ? undefined : 'settings.signin.check_days_range';
	let max = wholeIn(v.max_days, 1, 365) ? undefined : 'settings.signin.check_days_range';
	if (!expiry && !max && v.sliding && Number(v.max_days) < Number(v.expiry_days))
		max = 'settings.signin.check_max_below_duration';
	return { expiry_days: expiry, max_days: max };
}

export interface OidcValues {
	enabled: boolean;
	display_name: string;
	issuer_url: string;
	allowed_private_cidrs: string;
	client_id: string;
	client_secret: string;
	redirect_url: string;
	panel_url: string;
	scopes: string;
	use_pkce: boolean;
	auto_discovery: boolean;
	allow_signup: boolean;
	prefer_sso: boolean;
	authorization_url: string;
	token_url: string;
	userinfo_url: string;
	claim_username: string;
	claim_email: string;
	claim_name: string;
	claim_groups: string;
	default_role: string;
	group_role_map: string;
}

export function oidcFromServer(d: Record<string, unknown>, origin: string): OidcValues {
	return {
		enabled: !!d.enabled,
		display_name: str(d.display_name),
		issuer_url: str(d.issuer_url),
		allowed_private_cidrs: str(d.allowed_private_cidrs),
		client_id: str(d.client_id),
		client_secret: str(d.client_secret),
		redirect_url: str(d.redirect_url),
		panel_url: str(d.panel_url, origin),
		scopes: str(d.scopes, 'openid profile email'),
		use_pkce: d.use_pkce !== false,
		auto_discovery: d.auto_discovery !== false,
		allow_signup: d.allow_signup !== false,
		prefer_sso: d.prefer_sso === true,
		authorization_url: str(d.authorization_url),
		token_url: str(d.token_url),
		userinfo_url: str(d.userinfo_url),
		claim_username: str(d.claim_username, 'preferred_username'),
		claim_email: str(d.claim_email, 'email'),
		claim_name: str(d.claim_name, 'name'),
		claim_groups: str(d.claim_groups, 'groups'),
		default_role: str(d.default_role, 'viewer'),
		group_role_map: str(d.group_role_map)
	};
}

export function oidcPayload(v: OidcValues, origin: string) {
	return {
		enabled: v.enabled,
		display_name: v.display_name.trim(),
		issuer_url: v.issuer_url.trim(),
		allowed_private_cidrs: v.allowed_private_cidrs.trim(),
		client_id: v.client_id.trim(),
		client_secret: v.client_secret.trim(),
		redirect_url: v.redirect_url.trim(),
		panel_url: v.panel_url.trim() || origin,
		scopes: v.scopes.trim(),
		use_pkce: v.use_pkce,
		auto_discovery: v.auto_discovery,
		allow_signup: v.allow_signup,
		prefer_sso: v.prefer_sso,
		authorization_url: v.authorization_url.trim(),
		token_url: v.token_url.trim(),
		userinfo_url: v.userinfo_url.trim(),
		claim_username: v.claim_username.trim(),
		claim_email: v.claim_email.trim(),
		claim_name: v.claim_name.trim(),
		claim_groups: v.claim_groups.trim(),
		default_role: v.default_role.trim(),
		group_role_map: v.group_role_map.trim()
	};
}

const CALLBACK_PATHS = ['/api/auth/oidc/callback', '/api/oidc/callback'];

/** The console's own callback address */
export function suggestedRedirect(origin: string): string {
	return origin.replace(/\/+$/, '') + '/api/auth/oidc/callback';
}

/** http(s) and one of the callback paths (the server checks the same) */
export function validOidcRedirect(raw: string): boolean {
	if (!raw || typeof raw !== 'string') return false;
	try {
		const u = new URL(raw.trim());
		if (u.protocol !== 'http:' && u.protocol !== 'https:') return false;
		const path = (u.pathname || '/').replace(/\/+$/, '') || '/';
		return CALLBACK_PATHS.includes(path);
	} catch {
		return false;
	}
}

/* ── Checks per field, as the server makes them (auth/settings_check.go) ── */

/** The server's text for a refusal code (settings.signin.check_<code>), with {line} and {text} filled */
function check(code: string, fallback: string, params: Record<string, string | number> = {}): string {
	return tf(`settings.signin.check_${code}`, fallback, params);
}

/** One entry of a group to role map */
export interface RoleMapRow {
	group: string;
	role: string;
}

/**
 * A stored map as rows: entries separated by | or lines, each group=role
 * with the last = splitting them, so a DN keeps its own = and commas
 * (parseGroupRoleMap).
 */
export function parseRoleMap(raw: string): RoleMapRow[] {
	return String(raw || '')
		.replace(/\r\n/g, '\n')
		.split(/[|\n]/)
		.map((e) => e.trim())
		.filter(Boolean)
		.map((entry) => {
			const i = entry.lastIndexOf('=');
			return i <= 0
				? { group: entry, role: '' }
				: { group: entry.slice(0, i).trim(), role: entry.slice(i + 1).trim() };
		});
}

/** Rows back as the server stores them: LDAP joins with |, single sign-on with lines */
export function formatRoleMap(rows: readonly RoleMapRow[], separator: '|' | '\n'): string {
	return rows
		.filter((r) => r.group.trim() || r.role.trim())
		.map((r) => `${r.group.trim()}=${r.role.trim()}`)
		.join(separator);
}

/** What is wrong with each row ('' when nothing); an empty row is left out when saving */
export function roleMapProblems(rows: readonly RoleMapRow[], roles: readonly string[]): string[] {
	return rows.map((r, i) => {
		const group = r.group.trim();
		const role = r.role.trim();
		if (!group && !role) return '';
		const text = `${group}=${role}`;
		if (!group || !role)
			return check('map_line', 'Entry {line} of the role map needs a group and a role: {text}', {
				line: i + 1,
				text
			});
		if (!roles.includes(role))
			return check('map_role', 'Entry {line} of the role map names a role that does not exist: {text}', {
				line: i + 1,
				text
			});
		return '';
	});
}

function roleProblem(role: string, roles: readonly string[]): string | undefined {
	const r = role.trim();
	return r && !roles.includes(r)
		? check('role_invalid', 'This role does not exist: {text}', { text: r })
		: undefined;
}

function mapProblem(raw: string, roles: readonly string[]): string | undefined {
	return roleMapProblems(parseRoleMap(raw), roles).find(Boolean) || undefined;
}

/** A host name or address alone: no scheme, no port, no path, no spaces (validLDAPHost) */
function ldapHostProblem(raw: string): string | undefined {
	const host = raw.trim();
	if (!host) return check('host_required', "Enter the directory's host name or address.");
	const extra = check(
		'host_invalid',
		'Enter only the host name or address, without ldap:// and without a port.'
	);
	if (/[\s/\\@]/.test(host)) return extra;
	if (host.includes(':')) {
		try {
			new URL(`ldap://[${host.replace(/^\[|\]$/g, '')}]/`);
			return undefined;
		} catch {
			return extra;
		}
	}
	return undefined;
}

export type Problems<T> = Partial<Record<keyof T & string, string>>;

/**
 * What is wrong with directory settings that are switched on, by field.
 * The DN and filter syntax is the server's to check; its refusal names
 * the field too.
 */
export function ldapProblems(v: LdapValues, roles: readonly string[]): Problems<LdapValues> {
	if (!v.enabled) return {};
	const out: Problems<LdapValues> = {};
	const host = ldapHostProblem(v.host);
	if (host) out.host = host;
	const numbers = ldapNumberProblems(v);
	if (numbers.port) out.port = check('port_range', 'Enter a port from 1 to 65535.');
	if (numbers.conn_timeout_sec)
		out.conn_timeout_sec = check('timeout_range', 'Enter a whole number of seconds from 1 to 120.');
	if (v.direct_bind) {
		const template = v.direct_bind_dn.trim();
		if (!template)
			out.direct_bind_dn = check(
				'template_required',
				'Enter the DN template, with {{username}} where the name goes.'
			);
		else if (!template.includes('{{username}}'))
			out.direct_bind_dn = check(
				'template_placeholder',
				'The template needs {{username}} where the name goes.'
			);
	} else {
		if (!v.base_dn.trim())
			out.base_dn = check('base_dn_required', 'Enter the Base DN under which people are searched.');
		const filter = v.user_filter.trim();
		if (filter && !filter.includes('{{username}}'))
			out.user_filter = check(
				'user_filter_username',
				'The user filter needs {{username}} where the name goes.'
			);
	}
	const role = roleProblem(v.default_role, roles);
	if (role) out.default_role = role;
	const map = mapProblem(v.group_role_map, roles);
	if (map) out.group_role_map = map;
	return out;
}

/** 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16 as first address and prefix */
const PRIVATE_V4: readonly [number, number][] = [
	[0x0a000000, 8],
	[0xac100000, 12],
	[0xc0a80000, 16]
];

function v4(text: string): number | null {
	const parts = text.split('.');
	if (parts.length !== 4 || parts.some((p) => !/^\d{1,3}$/.test(p) || Number(p) > 255)) return null;
	return parts.reduce((n, p) => n * 256 + Number(p), 0);
}

function prefixOf(bits: string | undefined, max: number): number {
	if (bits === undefined) return max;
	return /^\d+$/.test(bits) && Number(bits) <= max ? Number(bits) : -1;
}

/**
 * What is wrong with one entry of the private networks single sign-on may
 * reach ('' when nothing): an address or a range inside 10.0.0.0/8,
 * 172.16.0.0/12, 192.168.0.0/16 or fc00::/7, as the server takes them.
 */
export function privateNetworkProblem(entry: string): string {
	const e = entry.trim();
	if (!e) return '';
	const wrong = check(
		'network_invalid',
		'This is not a private network: {text}. Use addresses or ranges in 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16 or fc00::/7.',
		{ text: e }
	);
	const [addr = '', bits, extra] = e.split('/');
	if (extra !== undefined) return wrong;
	if (addr.includes(':')) {
		let host: string;
		try {
			host = new URL(`http://[${addr}]/`).hostname.replace(/^\[|\]$/g, '');
		} catch {
			return wrong;
		}
		const prefix = prefixOf(bits, 128);
		return /^f[cd]/i.test(host) && prefix >= 7 ? '' : wrong;
	}
	const n = v4(addr);
	const prefix = prefixOf(bits, 32);
	if (n === null || prefix < 0) return wrong;
	const inside = PRIVATE_V4.some(
		([net, size]) => prefix >= size && Math.floor(n / 2 ** (32 - size)) === Math.floor(net / 2 ** (32 - size))
	);
	return inside ? '' : wrong;
}

/** The stored private networks as a list (the server splits at commas and spaces) */
export function splitNetworks(raw: string): string[] {
	return String(raw || '')
		.split(/[\s,]+/)
		.map((e) => e.trim())
		.filter(Boolean);
}

function urlProblem(raw: string): string | undefined {
	const v = raw.trim();
	if (!v) return undefined;
	const wrong = check('url_invalid', 'Enter a full address that starts with https:// or http://.');
	if (!/^https?:\/\//i.test(v) || /\s/.test(v)) return wrong;
	try {
		return new URL(v).hostname ? undefined : wrong;
	} catch {
		return wrong;
	}
}

/** What is wrong with single sign-on settings, by field; the networks are checked also while it is off */
export function oidcProblems(v: OidcValues, roles: readonly string[]): Problems<OidcValues> {
	const out: Problems<OidcValues> = {};
	const network = splitNetworks(v.allowed_private_cidrs).map(privateNetworkProblem).find(Boolean);
	if (network) out.allowed_private_cidrs = network;
	if (!v.enabled) return out;
	if (!v.client_id.trim())
		out.client_id = check('client_id_required', 'Enter the client ID from your identity provider.');
	const issuer = v.issuer_url.trim();
	const manual = !!v.authorization_url.trim() && !!v.token_url.trim();
	if (v.auto_discovery) {
		if (!issuer && !manual)
			out.issuer_url = check(
				'issuer_required',
				'Enter the issuer URL, or turn off Auto-discovery and enter the endpoints.'
			);
	} else {
		for (const f of ['authorization_url', 'token_url'] as const)
			if (!v[f].trim()) out[f] = check('endpoint_required', 'Without Auto-discovery, enter this endpoint.');
	}
	for (const f of ['issuer_url', 'authorization_url', 'token_url', 'userinfo_url', 'panel_url'] as const) {
		const p = urlProblem(v[f]);
		if (p && !out[f]) out[f] = p;
	}
	if (!validOidcRedirect(v.redirect_url))
		out.redirect_url = v.redirect_url.trim()
			? tf(
					'settings.oidc_redirect_url_invalid',
					'The redirect URL must be an http or https address ending in /api/auth/oidc/callback.'
				)
			: check('redirect_url_required', 'Enter the redirect URL.');
	const role = roleProblem(v.default_role, roles);
	if (role) out.default_role = role;
	const map = mapProblem(v.group_role_map, roles);
	if (map) out.group_role_map = map;
	return out;
}
