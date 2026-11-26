/**
 * The users list's view: filters and sort in the URL, what each filter
 * keeps, the CSV of the export and the template, and the summary of an
 * action for several accounts.
 */
import * as v from 'valibot';
import { describe, expect, test } from 'vitest';
import { BulkResult, ConsoleUser, PendingInvite } from '../../../src/lib/api/users.ts';
import {
	EMPTY_VIEW,
	accountStates,
	applyView,
	csvCell,
	exportCsv,
	importTemplate,
	isFiltered,
	onlySearchDiffers,
	sameView,
	summarize,
	toCsv,
	urlWithView,
	viewFromParams,
	withoutFilters,
	type UserListView
} from '../../../src/lib/people/userList.ts';

const BOM = String.fromCharCode(0xfeff);

const user = (over: Record<string, unknown> = {}) =>
	v.parse(ConsoleUser, { id: 5, username: 'anna', role: 'operator', email: 'anna@example.invalid', ...over });
const invite = (over: Record<string, unknown> = {}) =>
	v.parse(PendingInvite, { user_id: 5, expires_at: '2026-12-01T00:00:00Z', ...over });

describe('the view in the URL', () => {
	test('every filter and the sort go into the query and come back the same', () => {
		const view: UserListView = {
			search: 'ann',
			role: 'operator',
			org: 'o1',
			state: 'locked',
			twoFactor: 'required',
			method: 'ldap',
			inactive: '90',
			sort: { column: 'last_login', dir: 'desc' }
		};
		const url = urlWithView(new URL('https://desk.example/users?keep=1#user-groups'), view);
		expect(url.search).toBe(
			'?keep=1&search=ann&role=operator&org=o1&state=locked&2fa=required&method=ldap&inactive=90&sort=last_login&dir=desc'
		);
		expect(url.hash).toBe('#user-groups');
		expect(viewFromParams(url.searchParams)).toEqual(view);
	});

	test('a filter at its default leaves the query; clearing the filters keeps the sort', () => {
		const url = urlWithView(new URL('https://desk.example/users?role=viewer&state=locked'), {
			...EMPTY_VIEW,
			role: 'viewer'
		});
		expect(url.search).toBe('?role=viewer');
		const sorted = { ...EMPTY_VIEW, role: 'viewer', sort: { column: 'email', dir: 'asc' as const } };
		expect(withoutFilters(sorted)).toEqual({ ...EMPTY_VIEW, sort: { column: 'email', dir: 'asc' } });
		expect(isFiltered(sorted)).toBe(true);
		expect(isFiltered(withoutFilters(sorted))).toBe(false);
	});

	test('values the page does not know are left out, so an edited link still opens', () => {
		const view = viewFromParams(
			new URLSearchParams('state=sleepy&2fa=maybe&method=kerberos&inactive=7&sort=password&role=<b>&dir=up')
		);
		expect(view).toEqual(EMPTY_VIEW);
		expect(viewFromParams(new URLSearchParams('sort=role&dir=sideways')).sort).toEqual({
			column: 'role',
			dir: 'asc'
		});
	});

	test('typing in the search alone replaces the history entry; any other change adds one', () => {
		expect(onlySearchDiffers({ ...EMPTY_VIEW, search: 'a' }, { ...EMPTY_VIEW, search: 'ab' })).toBe(true);
		expect(
			onlySearchDiffers({ ...EMPTY_VIEW, search: 'a' }, { ...EMPTY_VIEW, search: 'a', role: 'pro' })
		).toBe(false);
		expect(sameView({ ...EMPTY_VIEW, search: ' a ' }, { ...EMPTY_VIEW, search: 'a' })).toBe(true);
		expect(sameView(EMPTY_VIEW, { ...EMPTY_VIEW, sort: { column: 'email', dir: 'asc' } })).toBe(false);
	});
});

describe('what the filters keep', () => {
	const invites = new Map([
		['2', invite({ user_id: 2 })],
		['3', invite({ user_id: 3, expired: true })],
		['7', invite({ user_id: 7 })]
	]);
	const users = [
		user({ id: 1, username: 'amy', role: 'viewer', last_login: '2026-11-25T10:00:00Z' }),
		user({ id: 2, username: 'ben', role: 'viewer' }),
		user({ id: 3, username: 'cat', role: 'operator' }),
		user({ id: 4, username: 'dan', disabled: true, last_login: '2026-01-01T00:00:00Z' }),
		user({
			id: 5,
			username: 'eve',
			locked: true,
			must_change_password: true,
			last_login: '2026-11-20T00:00:00Z'
		}),
		user({ id: 6, username: 'fay', expires_at: '2027-01-01T00:00:00Z', last_login: '2026-11-20T00:00:00Z' }),
		// Signed in once: the invite list's entry no longer counts
		user({
			id: 7,
			username: 'gus',
			expired: true,
			expires_at: '2026-11-01T00:00:00Z',
			last_login: '2026-10-01T00:00:00Z'
		}),
		user({
			id: 8,
			username: 'hal',
			auth_provider: 'ldap',
			totp_enabled: true,
			last_login: '2026-11-25T00:00:00Z'
		}),
		user({
			id: 9,
			username: 'ivy',
			auth_provider: 'oidc',
			two_factor_state: 'required',
			last_login: '2026-11-25T00:00:00Z'
		})
	];
	const orgs: Record<string, { id: string; name: string; role: string }[]> = {
		'1': [{ id: 'o1', name: 'Sales', role: 'user' }],
		'2': [],
		'3': [{ id: 'o2', name: 'Ops', role: 'user' }]
	};
	const facts = {
		roleLabel: (r: string) => r,
		invites,
		orgsOf: (id: string) => orgs[id],
		now: Date.parse('2026-11-26T12:00:00Z')
	};
	const names = (view: Partial<UserListView>) =>
		applyView(users, { ...EMPTY_VIEW, ...view }, facts)
			.map((u) => u.username)
			.join(' ');

	test('state: each account is in the states it has', () => {
		expect(names({ state: 'active' })).toBe('amy eve fay hal ivy');
		expect(names({ state: 'disabled' })).toBe('dan');
		expect(names({ state: 'invited' })).toBe('ben');
		expect(names({ state: 'invite_expired' })).toBe('cat');
		expect(names({ state: 'locked' })).toBe('eve');
		expect(names({ state: 'expired' })).toBe('gus');
		expect(names({ state: 'end_date' })).toBe('fay');
		expect(names({ state: 'new_password' })).toBe('eve');
		expect(accountStates(users[4]!)).toEqual(['active', 'locked', 'new_password']);
	});

	test('role, two-factor, sign-in method, last sign-in and search, also together', () => {
		expect(names({ role: 'operator' })).toBe('cat dan eve fay gus hal ivy');
		expect(names({ twoFactor: 'on' })).toBe('hal');
		expect(names({ twoFactor: 'required' })).toBe('ivy');
		expect(names({ method: 'ldap' })).toBe('hal');
		expect(names({ method: 'oidc' })).toBe('ivy');
		expect(names({ method: 'local' })).toBe('amy ben cat dan eve fay gus');
		expect(names({ inactive: '30' })).toBe('ben cat dan gus');
		expect(names({ inactive: 'never' })).toBe('ben cat');
		expect(names({ inactive: 'never', role: 'viewer' })).toBe('ben');
		expect(names({ search: 'amy', role: 'viewer' })).toBe('amy');
		expect(names({ search: 'amy', role: 'operator' })).toBe('');
	});

	test('organization: members of one, or of none; accounts whose organizations have not loaded are left out', () => {
		expect(names({ org: 'o1' })).toBe('amy');
		expect(names({ org: 'none' })).toBe('ben');
	});
});

describe('CSV', () => {
	test('cells that a spreadsheet would run as a formula start with a quote', () => {
		for (const v of ['=1+1', '+49 30 1234', '-2', '@SUM(A1)', '\t=cmd'])
			expect(csvCell(v).replace(/^"|"$/g, '').startsWith("'")).toBe(true);
		expect(csvCell('anna')).toBe('anna');
		expect(csvCell('a=b')).toBe('a=b');
	});

	test('separators, quotes and line breaks are quoted; the file has a byte order mark and CRLF', () => {
		expect(csvCell('Ade, Anna')).toBe('"Ade, Anna"');
		expect(csvCell('a;b')).toBe('"a;b"');
		expect(csvCell('say "hi"')).toBe('"say ""hi"""');
		expect(csvCell('two\nlines')).toBe('"two\nlines"');
		expect(
			toCsv([
				['a', 'b'],
				['c', '=d']
			])
		).toBe(BOM + "a,b\r\nc,'=d\r\n");
	});

	test('the export writes the columns given for each account; the template names the columns', () => {
		const out = exportCsv(
			[user({ username: '=evil', email: 'e@x.test' })],
			[
				{ label: 'Username', value: (u) => u.username },
				{ label: 'Email', value: (u) => u.email }
			]
		);
		expect(out).toBe(BOM + "Username,Email\r\n'=evil,e@x.test\r\n");
		expect(importTemplate()).toBe(BOM + 'username,email,first name,last name,role,organization,language\r\n');
	});
});

test('the results of an action for several accounts, by what happened', () => {
	const r = (id: number, outcome: string) => v.parse(BulkResult, { id, outcome });
	const s = summarize([r(1, 'changed'), r(2, 'refused'), r(3, 'changed'), r(4, 'unchanged'), r(5, 'failed')]);
	expect(s.changed.map((x) => x.id)).toEqual([1, 3]);
	expect(s.refused.map((x) => x.id)).toEqual([2]);
	expect(s.unchanged.map((x) => x.id)).toEqual([4]);
	expect(s.failed.map((x) => x.id)).toEqual([5]);
});
