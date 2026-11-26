/**
 * The users list's view without a DOM: the filters and the sort it keeps
 * in the URL, what each filter keeps, the CSV the export writes, and what
 * the results of an action for several accounts add up to. Unit tested in
 * tests/unit/people/user-list.test.ts.
 *
 * URL: ?search= ?role= ?org=<id or none> ?state= ?2fa= ?method=
 * ?inactive=30|60|90|never ?sort=<column>&dir=asc|desc. A value the page
 * doesn't know is left out, so an old or edited link still opens.
 */

import type { BulkResult, ConsoleUser, PendingInvite, UserOrg } from '#lib/api/users.ts';
import type { SortState } from '#lib/ui/table.ts';
import { filterInactive, filterUsers, inactiveFilter, providerOf, type InactiveFilter } from './users.ts';

/** The states of the state filter */
export const STATE_FILTERS = [
	'active',
	'disabled',
	'invited',
	'invite_expired',
	'locked',
	'expired',
	'end_date',
	'new_password'
] as const;
export type StateFilter = (typeof STATE_FILTERS)[number];

export const TWO_FACTOR_FILTERS = ['on', 'off', 'required'] as const;
export type TwoFactorFilter = (typeof TWO_FACTOR_FILTERS)[number];

/** Sign-in methods: a local password, LDAP/AD, single sign-on */
export const METHOD_FILTERS = ['local', 'ldap', 'oidc'] as const;
export type MethodFilter = (typeof METHOD_FILTERS)[number];

/** The columns the list sorts by */
export const SORT_COLUMNS = ['username', 'email', 'role', 'two_factor', 'last_login', 'created'] as const;

/** The organization filter's value for accounts in no organization */
export const NO_ORG = 'none';

export interface UserListView {
	search: string;
	role: string;
	/** An organization id, NO_ORG, or '' for any */
	org: string;
	state: StateFilter | '';
	twoFactor: TwoFactorFilter | '';
	method: MethodFilter | '';
	inactive: InactiveFilter | '';
	sort: SortState | null;
}

export const EMPTY_VIEW: UserListView = {
	search: '',
	role: '',
	org: '',
	state: '',
	twoFactor: '',
	method: '',
	inactive: '',
	sort: null
};

const PARAMS = ['search', 'role', 'org', 'state', '2fa', 'method', 'inactive', 'sort', 'dir'] as const;

function oneOf<T extends string>(list: readonly T[], value: string | null): T | '' {
	return (list as readonly string[]).includes(value ?? '') ? (value as T) : '';
}

/** The view a URL's query names */
export function viewFromParams(params: Pick<URLSearchParams, 'get'>): UserListView {
	const role = params.get('role')?.trim() ?? '';
	const org = params.get('org')?.trim() ?? '';
	const column = oneOf(SORT_COLUMNS, params.get('sort'));
	return {
		search: params.get('search')?.trim() ?? '',
		role: /^[a-z_]{1,40}$/.test(role) ? role : '',
		org: org.length <= 100 ? org : '',
		state: oneOf(STATE_FILTERS, params.get('state')),
		twoFactor: oneOf(TWO_FACTOR_FILTERS, params.get('2fa')),
		method: oneOf(METHOD_FILTERS, params.get('method')),
		inactive: inactiveFilter(params.get('inactive')),
		sort: column ? { column, dir: params.get('dir') === 'desc' ? 'desc' : 'asc' } : null
	};
}

/** The URL with the view's query, every other part kept; a filter at its default is left out */
export function urlWithView(url: { readonly href: string }, view: UserListView): URL {
	const next = new URL(url.href);
	for (const p of PARAMS) next.searchParams.delete(p);
	const set = (key: string, value: string) => {
		if (value) next.searchParams.set(key, value);
	};
	set('search', view.search.trim());
	set('role', view.role);
	set('org', view.org);
	set('state', view.state);
	set('2fa', view.twoFactor);
	set('method', view.method);
	set('inactive', view.inactive);
	if (view.sort) {
		set('sort', view.sort.column);
		set('dir', view.sort.dir);
	}
	return next;
}

/** The two views show the same list in the same order */
export function sameView(a: UserListView, b: UserListView): boolean {
	return (
		a.search.trim() === b.search.trim() &&
		a.role === b.role &&
		a.org === b.org &&
		a.state === b.state &&
		a.twoFactor === b.twoFactor &&
		a.method === b.method &&
		a.inactive === b.inactive &&
		a.sort?.column === b.sort?.column &&
		a.sort?.dir === b.sort?.dir
	);
}

/** The views differ in the search text alone (typing replaces the history entry instead of adding one) */
export function onlySearchDiffers(a: UserListView, b: UserListView): boolean {
	return a.search.trim() !== b.search.trim() && sameView({ ...a, search: '' }, { ...b, search: '' });
}

/** Some filter or the search leaves accounts out */
export function isFiltered(view: UserListView): boolean {
	return !!(
		view.search.trim() ||
		view.role ||
		view.org ||
		view.state ||
		view.twoFactor ||
		view.method ||
		view.inactive
	);
}

/** The view without search and filters; the sort stays */
export function withoutFilters(view: UserListView): UserListView {
	return { ...EMPTY_VIEW, sort: view.sort };
}

/** The account has an invite it hasn't used (an account that signed in has none) */
function openInvite(u: ConsoleUser, invite: PendingInvite | undefined): PendingInvite | undefined {
	return invite && !u.last_login ? invite : undefined;
}

/** Every state of the filter the account is in */
export function accountStates(u: ConsoleUser, invite?: PendingInvite): StateFilter[] {
	const out: StateFilter[] = [];
	const inv = openInvite(u, invite);
	if (!u.disabled && !u.expired && !inv) out.push('active');
	if (u.disabled) out.push('disabled');
	if (inv && !inv.expired) out.push('invited');
	if (inv?.expired) out.push('invite_expired');
	if (u.locked) out.push('locked');
	if (u.expired) out.push('expired');
	if (u.expires_at && !u.expired) out.push('end_date');
	if (u.must_change_password) out.push('new_password');
	return out;
}

/** The account's two-factor sign-in: on, off, or required and not set up */
export function twoFactorOf(u: Pick<ConsoleUser, 'two_factor_state' | 'totp_enabled'>): TwoFactorFilter {
	if (u.two_factor_state === 'on' || u.totp_enabled) return 'on';
	return u.two_factor_state === 'required' ? 'required' : 'off';
}

/** The account's sign-in method as the filter names it ('oidc' for single sign-on) */
export function methodOf(u: Pick<ConsoleUser, 'auth_provider'>): string {
	return providerOf(u);
}

export interface ViewFacts {
	roleLabel: (role: string) => string;
	invites: ReadonlyMap<string, PendingInvite>;
	/** The account's organizations; undefined while they load, 'error' when they couldn't */
	orgsOf: (id: string) => UserOrg[] | 'error' | undefined;
	now?: number;
}

/**
 * The accounts the view keeps. With an organization filter, an account
 * whose organizations haven't loaded (or failed to) is left out.
 */
export function applyView(
	users: readonly ConsoleUser[],
	view: UserListView,
	facts: ViewFacts
): ConsoleUser[] {
	let out = filterUsers(users, view.search, facts.roleLabel);
	if (view.role) out = out.filter((u) => u.role === view.role);
	if (view.org) {
		out = out.filter((u) => {
			const orgs = facts.orgsOf(String(u.id));
			if (!Array.isArray(orgs)) return false;
			return view.org === NO_ORG ? orgs.length === 0 : orgs.some((o) => String(o.id) === view.org);
		});
	}
	if (view.state) {
		const state = view.state;
		out = out.filter((u) => accountStates(u, facts.invites.get(String(u.id))).includes(state));
	}
	if (view.twoFactor) out = out.filter((u) => twoFactorOf(u) === view.twoFactor);
	if (view.method) out = out.filter((u) => methodOf(u) === view.method);
	return filterInactive(out, view.inactive, facts.now);
}

/* ── CSV ── */

/**
 * One cell of a CSV file. A value that starts with = + - @ (or a tab or a
 * carriage return) gets a single quote in front, so a spreadsheet shows it
 * as text instead of running it as a formula. A cell with a comma, a
 * semicolon, a quote, a line break or spaces at its ends goes in quotes.
 */
export function csvCell(value: string): string {
	let text = value ?? '';
	if (/^[=+\-@\t\r]/.test(text)) text = `'${text}`;
	return /[",;\r\n]|^\s|\s$/.test(text) ? `"${text.replace(/"/g, '""')}"` : text;
}

/** A CSV file: a UTF-8 byte order mark (Excel reads it as UTF-8 then), commas, CRLF */
export function toCsv(rows: readonly (readonly string[])[]): string {
	return '\uFEFF' + rows.map((r) => r.map(csvCell).join(',')).join('\r\n') + '\r\n';
}

/** The columns of the import, in the template's order */
export const IMPORT_COLUMNS = [
	'username',
	'email',
	'first name',
	'last name',
	'role',
	'organization',
	'language'
];

/** The template of an import: the column names alone */
export function importTemplate(): string {
	return toCsv([IMPORT_COLUMNS]);
}

export interface ExportColumn {
	label: string;
	value: (u: ConsoleUser) => string;
}

/** The export of the accounts shown: a heading line, then one line per account */
export function exportCsv(users: readonly ConsoleUser[], columns: readonly ExportColumn[]): string {
	return toCsv([columns.map((c) => c.label), ...users.map((u) => columns.map((c) => c.value(u)))]);
}

/* ── Actions on several accounts ── */

export interface BulkSummary {
	changed: BulkResult[];
	unchanged: BulkResult[];
	refused: BulkResult[];
	failed: BulkResult[];
}

/** The results of an action, by what happened */
export function summarize(results: readonly BulkResult[]): BulkSummary {
	const out: BulkSummary = { changed: [], unchanged: [], refused: [], failed: [] };
	for (const r of results) out[r.outcome].push(r);
	return out;
}

/** Saves a CSV file in the browser's downloads */
export function saveCsv(filename: string, text: string): void {
	const url = URL.createObjectURL(new Blob([text], { type: 'text/csv;charset=utf-8' }));
	const a = document.createElement('a');
	a.href = url;
	a.download = filename;
	document.body.append(a);
	a.click();
	a.remove();
	setTimeout(() => URL.revokeObjectURL(url), 1000);
}
