import * as v from 'valibot';
import { describe, expect, test } from 'vitest';
import { ConsoleUser, EffectiveScope, UserOrg, UserGroupRow } from '../../../src/lib/api/users.ts';
import {
	assignableRoles,
	availableOrgs,
	eachLimited,
	filterUsers,
	mayChangeUser,
	passwordProblem,
	passwordStrength,
	providerOf,
	roleChoices,
	scopeText,
	splitIds,
	usernameProblem
} from '../../../src/lib/people/users.ts';

const user = (over: Record<string, unknown> = {}) =>
	v.parse(ConsoleUser, { id: 5, username: 'anna', role: 'operator', email: 'anna@example.invalid', ...over });

describe('roles a person may give (canAssignUserRole on the server)', () => {
	test('super admins and the old admin name give every role, global admins three, everyone else none', () => {
		expect(assignableRoles('super_admin')).toContain('super_admin');
		expect(assignableRoles('admin')).toEqual(assignableRoles('super_admin'));
		expect(assignableRoles('global_admin')).toEqual(['viewer', 'operator', 'pro']);
		for (const r of ['server_admin', 'operator', 'viewer', 'pro', '', null, undefined]) {
			expect(assignableRoles(r)).toEqual([]);
		}
		// guest_issuer is never offered: the server never assigns it from the console
		expect(assignableRoles('super_admin')).not.toContain('guest_issuer');
	});

	test('who may edit, reset or delete an account: its own, a role the editor may give, guest issuers for global admins', () => {
		const me = user({ id: 5, role: 'global_admin' });
		expect(mayChangeUser('global_admin', '5', me)).toBe(true);
		expect(mayChangeUser('global_admin', '5', user({ id: 6, role: 'operator' }))).toBe(true);
		expect(mayChangeUser('global_admin', '5', user({ id: 7, role: 'guest_issuer' }))).toBe(true);
		for (const role of ['super_admin', 'server_admin', 'global_admin'])
			expect(mayChangeUser('global_admin', '5', user({ id: 8, role }))).toBe(false);
		expect(mayChangeUser('super_admin', '1', user({ id: 8, role: 'super_admin' }))).toBe(true);
		expect(mayChangeUser('server_admin', '2', user({ id: 9, role: 'viewer' }))).toBe(false);
	});

	test('a role the editor may not give stays visible and locked, so the current role is clear', () => {
		const target = user({ role: 'server_admin' });
		const choices = roleChoices('global_admin', target);
		expect(choices.map((c) => c.role)).toEqual(['viewer', 'operator', 'server_admin', 'pro']);
		expect(choices.find((c) => c.role === 'server_admin')?.disabled).toBe(true);
		expect(choices.find((c) => c.role === 'viewer')?.disabled).toBe(false);
	});

	test('LDAP accounts take their role from the directory: every choice is locked', () => {
		const choices = roleChoices('super_admin', user({ auth_provider: 'LDAP' }));
		expect(choices.length).toBeGreaterThan(1);
		expect(choices.every((c) => c.disabled)).toBe(true);
		// OIDC accounts can be changed
		expect(roleChoices('super_admin', user({ auth_provider: 'oidc' })).some((c) => !c.disabled)).toBe(true);
	});

	test('a new account starts as viewer', () => {
		const choices = roleChoices('global_admin', null);
		expect(choices.every((c) => !c.disabled)).toBe(true);
		expect(providerOf(user({ auth_provider: '' }))).toBe('local');
	});
});

describe('passwords and usernames (the server rules)', () => {
	test('the strength line scores weak, medium, strong', () => {
		expect(passwordStrength('abc')).toBe('weak');
		expect(passwordStrength('abcdefgh')).toBe('weak');
		expect(passwordStrength('abcdefgH1')).toBe('medium');
		expect(passwordStrength('abcdefgH1!xyz')).toBe('strong');
	});

	test('a password is refused when missing (and required) or under 8 characters, not when weak (L94)', () => {
		expect(passwordProblem('', true)).toBe('users.password_required');
		expect(passwordProblem('', false)).toBeNull();
		expect(passwordProblem('Ab1!', true)).toBe('users.password_too_short');
		expect(passwordProblem('sixsix7', true)).toBe('users.password_too_short');
		expect(passwordProblem('abcdefgh', false)).toBeNull();
		expect(passwordProblem('Correct-Horse-9', true)).toBeNull();
	});

	test('usernames: 3 to 64 letters, digits and _ . @ -, as the server checks them', () => {
		expect(usernameProblem('')).toBe('users.username_required');
		expect(usernameProblem('ab')).toBe('users.invalid_username');
		expect(usernameProblem('anna.k')).toBeNull();
		expect(usernameProblem('anna@example.org')).toBeNull();
		expect(usernameProblem('anna-k_2')).toBeNull();
		expect(usernameProblem('a'.repeat(64))).toBeNull();
		expect(usernameProblem('a'.repeat(65))).toBe('users.invalid_username');
		expect(usernameProblem('anna k')).toBe('users.invalid_username');
		expect(usernameProblem('anna+k')).toBe('users.invalid_username');
		expect(usernameProblem('änna')).toBe('users.invalid_username');
	});

	test('direct device IDs split on commas, semicolons and spaces', () => {
		expect(splitIds(' 123, 456;789  1011 ,,')).toEqual(['123', '456', '789', '1011']);
		expect(splitIds('')).toEqual([]);
	});
});

describe('search and cells', () => {
	const label = (r: string) => ({ operator: 'Remote operator', viewer: 'Viewer' })[r] ?? r;
	const list = [user(), user({ id: 6, username: 'ben', role: 'viewer', email: '' })];

	test('every word must match the username, email or role name', () => {
		expect(filterUsers(list, '', label)).toHaveLength(2);
		expect(filterUsers(list, 'remote', label).map((u) => u.username)).toEqual(['anna']);
		expect(filterUsers(list, 'BEN viewer', label).map((u) => u.username)).toEqual(['ben']);
		expect(filterUsers(list, 'ben operator', label)).toEqual([]);
		expect(filterUsers(list, 'example.invalid', label).map((u) => u.username)).toEqual(['anna']);
	});

	test('device scope: loading, failed, all, none, a count', () => {
		expect(scopeText(undefined)).toEqual({ key: '', muted: true });
		expect(scopeText('error').key).toBe('users.scope_unknown');
		expect(scopeText({ count: 30, restricted: false }).key).toBe('users.scope_all');
		expect(scopeText({ count: 0, restricted: true }).key).toBe('users.scope_none');
		expect(scopeText({ count: 1, restricted: true }).key).toBe('users.scope_count_one');
		expect(scopeText({ count: 4, restricted: true })).toMatchObject({
			key: 'users.scope_count_many',
			params: { count: 4 }
		});
	});

	test('organizations that can still be added leave out the ones the account is in', () => {
		const all = [{ id: 'a' }, { id: 'b' }, { id: 'c' }];
		expect(availableOrgs(all, [{ id: 'b', name: 'B', role: 'user' }]).map((o) => o.id)).toEqual(['a', 'c']);
	});

	test('per-row requests run at most `limit` at a time and all finish', async () => {
		let running = 0;
		let peak = 0;
		const done: number[] = [];
		await eachLimited([1, 2, 3, 4, 5, 6, 7, 8, 9], 4, async (n) => {
			running++;
			peak = Math.max(peak, running);
			await new Promise((r) => setTimeout(r, 5));
			done.push(n);
			running--;
		});
		expect(peak).toBe(4);
		expect(done.sort((a, b) => a - b)).toEqual([1, 2, 3, 4, 5, 6, 7, 8, 9]);
	});
});

describe('users API schemas', () => {
	test('the list keeps the two-factor state and turns nulls into clean values', () => {
		const u = v.parse(ConsoleUser, {
			id: 2,
			username: 'demo',
			role: 'operator',
			email: null,
			auth_provider: null,
			user_groups: null,
			folder_ids: [3, '4'],
			peer_grants: ['900100001'],
			strategy_guid: null,
			totp_enabled: true
		});
		expect(u).toMatchObject({
			email: '',
			auth_provider: '',
			user_groups: [],
			folder_ids: ['3', '4'],
			strategy_guid: '',
			totp_enabled: true
		});
		// Older servers leave the field out: the page then shows no two-factor column
		expect(v.parse(ConsoleUser, { id: 1, username: 'x', role: 'viewer' }).totp_enabled).toBeUndefined();
	});

	test('organizations of a user read either name the Go server sends', () => {
		expect(v.parse(UserOrg, { org_id: 'o1', org_name: 'Clinic', role: 'admin' })).toEqual({
			id: 'o1',
			name: 'Clinic',
			role: 'admin'
		});
		expect(v.parse(UserOrg, { id: 'o2', name: 'Lab' })).toEqual({ id: 'o2', name: 'Lab', role: '' });
	});

	test('scope and group rows default missing numbers', () => {
		expect(v.parse(EffectiveScope, {})).toEqual({ count: 0, restricted: false });
		expect(v.parse(UserGroupRow, { guid: 'g', name: null }).member_count).toBe(0);
		expect(v.parse(UserGroupRow, { guid: 'g', name: null }).name).toBe('');
	});
});
