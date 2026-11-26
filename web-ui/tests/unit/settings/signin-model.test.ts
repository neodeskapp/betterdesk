/**
 * Directory (LDAP) and single sign-on as drafts (lib/settings/signin.ts):
 * the group to role map as rows, the private networks as a list, and the
 * checks per field the server makes too (auth/settings_check.go), said at
 * the field before anything is sent.
 */
import { describe, expect, test } from 'vitest';
import {
	formatRoleMap,
	ldapFromServer,
	ldapProblems,
	oidcFromServer,
	oidcProblems,
	parseRoleMap,
	privateNetworkProblem,
	roleMapProblems
} from '../../../src/lib/settings/signin.ts';

const ROLES = ['viewer', 'operator', 'admin'];

describe('the group to role map', () => {
	test('a stored map becomes one row per entry; a DN keeps its = and commas', () => {
		expect(parseRoleMap('CN=Admins,DC=ex,DC=com=admin|CN=Ops,DC=ex,DC=com=operator')).toEqual([
			{ group: 'CN=Admins,DC=ex,DC=com', role: 'admin' },
			{ group: 'CN=Ops,DC=ex,DC=com', role: 'operator' }
		]);
		expect(parseRoleMap('neodesk-admins=admin\nneodesk-ops = operator\n\n')).toEqual([
			{ group: 'neodesk-admins', role: 'admin' },
			{ group: 'neodesk-ops', role: 'operator' }
		]);
		expect(parseRoleMap('')).toEqual([]);
	});

	test('rows go back in the form the server reads; empty rows are dropped', () => {
		const rows = [
			{ group: 'CN=Admins,DC=ex', role: 'admin' },
			{ group: ' ', role: '' },
			{ group: 'Ops', role: 'operator' }
		];
		expect(formatRoleMap(rows, '|')).toBe('CN=Admins,DC=ex=admin|Ops=operator');
		expect(formatRoleMap(rows, '\n')).toBe('CN=Admins,DC=ex=admin\nOps=operator');
	});

	test('a row without a group or with a role that does not exist says so on that row', () => {
		const p = roleMapProblems(
			[
				{ group: 'Ops', role: 'operator' },
				{ group: '', role: 'admin' },
				{ group: 'X', role: 'wizard' },
				{ group: '', role: '' }
			],
			ROLES
		);
		expect(p[0]).toBe('');
		expect(p[1]).toMatch(/group/i);
		expect(p[2]).toMatch(/wizard/);
		expect(p[3]).toBe('');
	});
});

describe('directory (LDAP) checks', () => {
	const on = (o: Record<string, unknown>) => ({
		...ldapFromServer({ enabled: true, host: 'ldap.example.com', base_dn: 'dc=x' }),
		...o
	});

	test('nothing is checked while directory sign-in is off', () => {
		expect(ldapProblems(ldapFromServer({ enabled: false, host: 'ldap://x:389' }), ROLES)).toEqual({});
	});

	test('the host alone, the port and timeout in range, the base DN or the template', () => {
		expect(ldapProblems(on({}), ROLES)).toEqual({});
		expect(ldapProblems(on({ host: '' }), ROLES).host).toMatch(/host name/);
		expect(ldapProblems(on({ host: 'ldaps://ldap.example.com' }), ROLES).host).toMatch(/without ldap/);
		expect(ldapProblems(on({ host: 'ldap.example.com:636' }), ROLES).host).toMatch(/without ldap/);
		expect(ldapProblems(on({ host: '2001:db8::1' }), ROLES)).toEqual({});
		expect(ldapProblems(on({ port: 70000 }), ROLES).port).toBeTruthy();
		expect(ldapProblems(on({ conn_timeout_sec: 0 }), ROLES).conn_timeout_sec).toBeTruthy();
		expect(ldapProblems(on({ base_dn: '' }), ROLES).base_dn).toBeTruthy();
		expect(ldapProblems(on({ user_filter: '(uid=x)' }), ROLES).user_filter).toMatch(/\{\{username\}\}/);
		const direct = on({ direct_bind: true, base_dn: '' });
		expect(ldapProblems(direct, ROLES).base_dn).toBeUndefined();
		expect(ldapProblems(direct, ROLES).direct_bind_dn).toBeTruthy();
		expect(ldapProblems({ ...direct, direct_bind_dn: 'uid=x,dc=ex' }, ROLES).direct_bind_dn).toMatch(
			/\{\{username\}\}/
		);
	});

	test('the default role and the map name roles that exist', () => {
		expect(ldapProblems(on({ default_role: 'wizard' }), ROLES).default_role).toMatch(/wizard/);
		expect(ldapProblems(on({ group_role_map: 'Ops=wizard' }), ROLES).group_role_map).toBeTruthy();
	});
});

describe('single sign-on checks', () => {
	const origin = 'https://desk.example.com';
	const on = (o: Record<string, unknown>) => ({
		...oidcFromServer(
			{
				enabled: true,
				client_id: 'neodesk',
				issuer_url: 'https://login.example.com',
				redirect_url: `${origin}/api/auth/oidc/callback`
			},
			origin
		),
		...o
	});

	test('a complete provider has no problems; off checks only the networks', () => {
		expect(oidcProblems(on({}), ROLES)).toEqual({});
		expect(oidcProblems({ ...on({ client_id: '' }), enabled: false }, ROLES)).toEqual({});
		expect(
			oidcProblems({ ...on({}), enabled: false, allowed_private_cidrs: '8.8.8.0/24' }, ROLES)
				.allowed_private_cidrs
		).toBeTruthy();
	});

	test('client ID, issuer or endpoints, addresses and the callback', () => {
		expect(oidcProblems(on({ client_id: ' ' }), ROLES).client_id).toBeTruthy();
		expect(oidcProblems(on({ issuer_url: '' }), ROLES).issuer_url).toBeTruthy();
		expect(oidcProblems(on({ issuer_url: 'login.example.com' }), ROLES).issuer_url).toMatch(/https:\/\//);
		const manual = on({ auto_discovery: false, issuer_url: '' });
		expect(Object.keys(oidcProblems(manual, ROLES)).sort()).toEqual(['authorization_url', 'token_url']);
		expect(oidcProblems(on({ redirect_url: `${origin}/callback` }), ROLES).redirect_url).toBeTruthy();
		expect(oidcProblems(on({ panel_url: 'desk' }), ROLES).panel_url).toBeTruthy();
	});
});

describe('the private networks single sign-on may reach', () => {
	test('addresses and ranges in the private networks only', () => {
		for (const ok of ['10.20.0.0/16', '192.168.50.10', '172.16.0.0/12', 'fd00::/8', 'fc00::1'])
			expect(privateNetworkProblem(ok), ok).toBe('');
		for (const bad of ['8.8.8.0/24', '10.0.0.0/33', '192.168.1', 'example.com', '2001:db8::/32'])
			expect(privateNetworkProblem(bad), bad).toMatch(/not a private network/i);
	});
});
