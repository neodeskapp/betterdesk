/**
 * The user's page without a DOM (lib/people/user-page.ts): what a role
 * allows in sentences, grouped like the permissions page; how the account
 * reaches devices; what its sign-in looks like.
 */
import { describe, expect, test } from 'vitest';
import type { UserDetail } from '../../../src/lib/api/users.ts';
import {
	accessLines,
	auditHref,
	orgsLine,
	permissionSentences,
	signInFacts
} from '../../../src/lib/people/user-page.ts';

const T: Record<string, string> = {
	'users.can_device_view': 'Can see devices',
	'users.can_device_connect': 'Can connect to devices',
	'users.can_user_create': 'Can add users',
	'permissions.group_connect': 'Connect and control',
	'permissions.group_manage': 'Manage devices',
	'permissions.group_people': 'People and organizations',
	'permissions.group_other': 'Other',
	'permissions.perm_chat_team': 'Team chat'
};
const t = (key: string, params?: Record<string, string | number>) => {
	const text = T[key] ?? key;
	return params ? `${text} ${JSON.stringify(params)}` : text;
};

describe('what a role allows', () => {
	test('sentences in the groups and order of the permissions page, never permission ids', () => {
		expect(permissionSentences(['user.create', 'device.connect', 'device.view'], t)).toEqual([
			{ id: 'connect', label: 'Connect and control', sentences: ['Can connect to devices'] },
			{ id: 'manage', label: 'Manage devices', sentences: ['Can see devices'] },
			{ id: 'people', label: 'People and organizations', sentences: ['Can add users'] }
		]);
	});

	test('a permission without a sentence uses its label; one no group names goes under Other', () => {
		const groups = permissionSentences(['chat.team', 'future.thing'], t);
		expect(groups.map((g) => g.id)).toEqual(['support', 'other']);
		expect(groups[0]!.sentences).toEqual(['Team chat']);
		expect(groups[1]).toEqual({ id: 'other', label: 'Other', sentences: ['future.thing'] });
	});

	test('no permission, no groups', () => {
		expect(permissionSentences([], t)).toEqual([]);
	});
});

const base = { role: 'operator', user_groups: [], folder_ids: [], peer_grants: [], strategy_guid: '' };

describe('how the account reaches devices', () => {
	test('all devices: nothing limits it', () => {
		expect(accessLines(base, { count: 0, restricted: false }, (g) => g, t)).toEqual([
			'users.page_scope_all',
			'users.page_scope_all_why'
		]);
	});

	test('a count, and how it comes about', () => {
		const u = { ...base, user_groups: ['g1', 'g2'], folder_ids: ['7'], peer_grants: ['1', '2', '3'] };
		const names: Record<string, string> = { g1: 'Support', g2: 'Lab' };
		expect(accessLines(u, { count: 12, restricted: true }, (g) => names[g] ?? g, t)).toEqual([
			'users.page_scope_many {"count":12}',
			'users.page_scope_groups {"groups":"Support, Lab"}',
			'users.page_scope_folders_one {"count":1}',
			'users.page_scope_direct_many {"count":3}'
		]);
	});

	test('nothing given says what to do; a failed count says so', () => {
		expect(accessLines(base, { count: 0, restricted: true }, (g) => g, t)).toEqual([
			'users.page_scope_none',
			'users.page_scope_nothing_given'
		]);
		expect(accessLines(base, 'error', (g) => g, t)[0]).toBe('users.page_scope_unknown');
		expect(accessLines(base, undefined, (g) => g, t)).toEqual([]);
	});

	test('the organizations line', () => {
		expect(orgsLine([{ id: '1', name: 'Acme', role: '' }], t)).toBe('users.page_orgs {"orgs":"Acme"}');
		expect(orgsLine([], t)).toBe('users.page_orgs_none');
		expect(orgsLine('error', t)).toBe('users.page_orgs_unknown');
	});
});

function detail(over: Partial<UserDetail> = {}): UserDetail {
	return {
		auth_provider: 'local',
		two_factor_state: 'off',
		totp_enabled: false,
		two_factor_required: false,
		disabled: false,
		locked: false,
		locked_until: null,
		must_change_password: false,
		password_reset_until: null,
		invite: null,
		last_login: '2026-11-01 10:00:00',
		expires_at: null,
		expired: false,
		...over
	} as UserDetail;
}

describe('the sign-in state', () => {
	const keys = (u: UserDetail) => signInFacts(u).map((f) => f.key);

	test('a local account with a password and two-factor sign-in off', () => {
		expect(keys(detail())).toEqual(['users.page_method_password', 'users.page_2fa_off']);
	});

	test('LDAP and single sign-on say so and leave two-factor sign-in to the provider', () => {
		expect(keys(detail({ auth_provider: 'ldap' }))).toEqual(['users.page_method_ldap']);
		expect(keys(detail({ auth_provider: 'oidc' }))).toEqual(['users.page_method_sso']);
	});

	test('what waits or stops the sign-in, with its date', () => {
		const facts = signInFacts(
			detail({
				two_factor_state: 'required',
				disabled: true,
				locked: true,
				locked_until: '2026-11-26T10:00:00Z',
				must_change_password: true,
				password_reset_until: '2026-11-27T10:00:00Z',
				invite: { expires_at: '2026-11-28T10:00:00Z', expired: false },
				last_login: null,
				expires_at: '2026-12-31T00:00:00Z'
			})
		);
		expect(facts.map((f) => `${f.key}${f.date ? ' ' + f.date : ''}`)).toEqual([
			'users.page_method_password',
			'users.page_2fa_required',
			'users.page_disabled',
			'users.page_locked 2026-11-26T10:00:00Z',
			'users.page_must_change',
			'users.page_reset_waiting 2026-11-27T10:00:00Z',
			'users.page_invite_waiting 2026-11-28T10:00:00Z',
			'users.page_expires 2026-12-31T00:00:00Z'
		]);
	});

	test('an expired invite and a passed end date are warnings', () => {
		const facts = signInFacts(
			detail({
				invite: { expires_at: '2026-11-01T00:00:00Z', expired: true },
				last_login: null,
				expires_at: '2026-11-02T00:00:00Z',
				expired: true
			})
		);
		expect(facts.filter((f) => f.tone === 'warning').map((f) => f.key)).toEqual([
			'users.page_invite_expired',
			'users.page_expired'
		]);
	});
});

test('the audit log link searches for the account', () => {
	expect(auditHref('a b')).toBe('/security-audit?tab=audit&q=a%20b');
});
