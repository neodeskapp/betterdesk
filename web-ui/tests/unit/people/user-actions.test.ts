/**
 * The actions on one account (lib/people/user-actions.ts), which the users
 * row menu and the user's page both show: grouped account, access,
 * sign-in security and the ones that end something, each offered by the
 * signed-in person's permissions and the server's rules.
 */
import { afterEach, describe, expect, test } from 'vitest';
import type { ConsoleUser } from '../../../src/lib/api/users.ts';
import {
	mayViewAs,
	signInLink,
	userActionGroups,
	type ActionRights
} from '../../../src/lib/people/user-actions.ts';
import { session } from '../../../src/lib/state/session.svelte.ts';

function account(over: Partial<ConsoleUser> = {}): ConsoleUser {
	return {
		id: 5,
		username: 'olga',
		display_name: 'Olga Ober',
		role: 'operator',
		email: 'olga@example.test',
		auth_provider: 'local',
		last_login: '2026-11-01 10:00:00',
		disabled: false,
		locked: false,
		must_change_password: false,
		two_factor_required: false,
		two_factor_state: 'off',
		two_factor_due: false,
		totp_enabled: false,
		...over
	} as ConsoleUser;
}

const ADMIN: ActionRights = {
	meId: '1',
	myRole: 'super_admin',
	canEdit: true,
	canDelete: true,
	canSecurity: true,
	canWithdraw: true,
	canViewAs: true,
	canTeamChat: true
};
const VIEWER: ActionRights = {
	meId: '9',
	myRole: 'server_admin',
	canEdit: false,
	canDelete: false,
	canSecurity: false,
	canWithdraw: false,
	canViewAs: false,
	canTeamChat: false
};
const FACTS = { mailReady: true, invited: false };

/** group: action ids, as one line per group */
function shape(groups: ReturnType<typeof userActionGroups>): string[] {
	return groups.map((g) => `${g.id}: ${g.actions.map((a) => a.id).join(' ')}`);
}

describe('the actions on an account', () => {
	test('a super admin on an operator gets every group, the dangerous actions last', () => {
		expect(shape(userActionGroups(account(), ADMIN, { ...FACTS, teamChat: true }))).toEqual([
			'account: edit message copy_link',
			'access: orgs access sign_in_as',
			'security: reset_password require_change require_2fa sessions',
			'danger: disable delete'
		]);
	});

	test('read-only (user.view alone): only what shows something, nothing that changes the account', () => {
		const groups = userActionGroups(account(), VIEWER, { ...FACTS, teamChat: true });
		expect(shape(groups)).toEqual(['account: copy_link', 'access: orgs access']);
	});

	test('your own account: no disable, delete, Sign in as, message or security rules', () => {
		expect(
			shape(userActionGroups(account({ id: 1, role: 'super_admin' }), ADMIN, { ...FACTS, teamChat: true }))
		).toEqual(['account: edit copy_link', 'access: orgs access', 'security: reset_password sessions']);
	});

	test('an account above your own: nothing that changes it', () => {
		const global = { ...ADMIN, meId: '4', myRole: 'global_admin' };
		expect(shape(userActionGroups(account({ role: 'super_admin' }), global, FACTS))).toEqual([
			'account: copy_link',
			'access: orgs access sign_in_as'
		]);
	});

	test('LDAP and single sign-on accounts get no password or two-factor rules', () => {
		const ldap = shape(userActionGroups(account({ auth_provider: 'ldap' }), ADMIN, FACTS));
		expect(ldap).toContain('security: reset_password sessions');
	});

	test('the states turn actions around: locked, a new password due, two-factor on', () => {
		const u = account({
			locked: true,
			must_change_password: true,
			two_factor_required: true,
			two_factor_state: 'on',
			two_factor_due: true
		});
		expect(shape(userActionGroups(u, ADMIN, FACTS))).toContain(
			'security: reset_password require_change_off unlock require_2fa_off reset_2fa invalidate_codes sessions'
		);
		expect(shape(userActionGroups(account({ two_factor_state: 'on' }), ADMIN, FACTS))).toContain(
			'security: reset_password require_change require_2fa totp_off invalidate_codes sessions'
		);
	});

	test('an invited account: send again, and withdraw before disable', () => {
		const u = account({ last_login: null });
		expect(shape(userActionGroups(u, ADMIN, { mailReady: true, invited: true }))).toEqual([
			'account: edit resend_invite copy_link',
			'access: orgs access sign_in_as',
			'security: reset_password require_change require_2fa sessions',
			'danger: withdraw_invite disable delete'
		]);
		// Without mail settings no invite goes out again
		expect(shape(userActionGroups(u, ADMIN, { mailReady: false, invited: true }))[0]).toBe(
			'account: edit copy_link'
		);
	});

	test('a disabled account: enable in the account group, no Sign in as or message', () => {
		expect(shape(userActionGroups(account({ disabled: true }), ADMIN, { ...FACTS, teamChat: true }))).toEqual(
			[
				'account: edit enable copy_link',
				'access: orgs access',
				'security: reset_password require_change require_2fa sessions',
				'danger: delete'
			]
		);
	});

	test('Send a message needs chat.team and a person who can use Team chat', () => {
		const ids = (r: ActionRights, teamChat?: boolean) =>
			userActionGroups(account(), r, { ...FACTS, teamChat })[0]!.actions.map((a) => a.id);
		expect(ids(ADMIN, true)).toContain('message');
		expect(ids(ADMIN, false)).not.toContain('message');
		expect(ids(ADMIN, undefined)).not.toContain('message');
		expect(ids({ ...ADMIN, canTeamChat: false }, true)).not.toContain('message');
	});

	test('the list adds the way to the page first', () => {
		expect(
			userActionGroups(account(), VIEWER, { ...FACTS, withOpen: true })[0]!.actions.map((a) => a.id)
		).toEqual(['open', 'copy_link']);
	});

	test('the classes the e2e suite finds the items by stay', () => {
		const all = userActionGroups(account(), ADMIN, FACTS).flatMap((g) => g.actions.map((a) => a.cls));
		expect(all).toEqual(
			expect.arrayContaining([
				'act-edit',
				'act-orgs',
				'act-access',
				'act-reset',
				'act-sessions',
				'act-disable',
				'act-delete'
			])
		);
	});
});

describe('the sign-in link', () => {
	test('is the sign-in page with the username filled in, under the base', () => {
		expect(signInLink('https://desk.example', '', 'olga')).toBe('https://desk.example/login?username=olga');
		expect(signInLink('https://desk.example/', '/console', 'a b&c@x')).toBe(
			'https://desk.example/console/login?username=a%20b%26c%40x'
		);
	});
});

describe('Sign in as', () => {
	const access = (users: 'read' | 'write') => ({
		sections: { users } as Record<string, 'none' | 'read' | 'write'>,
		pages: [{ prefix: '/users', section: 'users' }],
		catalog: [{ key: 'users', read: 'user.view', write: 'user.edit' }]
	});
	afterEach(() => {
		session.permissions = [];
		session.access = null;
		session.path = '';
		session.impersonation = null;
		session.isSuperAdmin = false;
	});

	test('needs the Users section at write besides user.impersonate (A274), as the server asks', () => {
		session.permissions = ['user.view', 'user.edit', 'user.impersonate'];
		session.path = '/users';
		session.access = access('write');
		expect(mayViewAs()).toBe(true);
		session.path = '/users/5';
		expect(mayViewAs()).toBe(true);
		// A panel access row that leaves Users read-only takes it away, like the other writes
		session.access = access('read');
		expect(mayViewAs()).toBe(false);
		session.path = '/users';
		expect(mayViewAs()).toBe(false);
		// Without the permission write is no help
		session.access = access('write');
		session.permissions = ['user.view', 'user.edit'];
		expect(mayViewAs()).toBe(false);
	});

	test('is not offered while viewing as someone', () => {
		session.permissions = ['user.view', 'user.edit', 'user.impersonate'];
		session.path = '/users';
		session.access = access('write');
		session.impersonation = { active: true } as unknown as typeof session.impersonation;
		expect(mayViewAs()).toBe(false);
	});
});
