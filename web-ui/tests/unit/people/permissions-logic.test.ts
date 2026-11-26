/**
 * The logic of Roles and permissions without a DOM: the groups by what a
 * person does, the sentence that sums a role up, the notes for permissions
 * that need others, the view of what differs from the default, the
 * comparison with a second role, who has a permission, and the bodies of
 * the batch save and its undo.
 */
import { describe, expect, test } from 'vitest';
import {
	PERMISSION_GROUPS,
	batchBody,
	brokenBy,
	compareWith,
	differsFromDefault,
	liveEffective,
	missingNeeds,
	permRowId,
	permissionGroups,
	roleSummary,
	rolesWith,
	toggleValue,
	undoBody,
	type Ovr
} from '../../../src/lib/people/permissions.ts';

const t = (key: string, params?: Record<string, string | number>) =>
	params ? `${key}(${Object.values(params).join('|')})` : key;
const and = (items: string[]) => items.join(' + ');
const or = (items: string[]) => items.join(' / ');

const ALL = [
	'device.view',
	'device.connect',
	'device.edit',
	'device.delete',
	'chat.support',
	'user.view',
	'user.create',
	'server.keys',
	'audit.view',
	'billing.view',
	'future.thing'
];

describe('groups by what a person does', () => {
	test('the six groups in order, each permission once, and Other for the rest', () => {
		const groups = permissionGroups(ALL);
		expect(groups.map((g) => g.id)).toEqual([
			'connect',
			'manage',
			'support',
			'people',
			'server',
			'billing',
			'other'
		]);
		expect(groups.find((g) => g.id === 'connect')?.permissions).toEqual(['device.connect']);
		expect(groups.find((g) => g.id === 'manage')?.permissions).toEqual([
			'device.view',
			'device.edit',
			'device.delete'
		]);
		expect(groups.find((g) => g.id === 'other')?.permissions).toEqual(['future.thing']);
	});

	test('every permission of the server has exactly one group', () => {
		const listed = PERMISSION_GROUPS.flatMap((g) => g.permissions);
		expect(new Set(listed).size).toBe(listed.length);
		for (const p of ['agent.terminal', 'cdap.terminal', 'mesh.power', 'remote_target.connect'])
			expect(PERMISSION_GROUPS.find((g) => g.permissions.includes(p))?.id).toBe('connect');
		for (const p of ['cdap.view', 'enrollment.approve', 'device.signin_manage'])
			expect(PERMISSION_GROUPS.find((g) => g.permissions.includes(p))?.id).toBe('manage');
		expect(PERMISSION_GROUPS.find((g) => g.permissions.includes('branding.edit'))?.id).toBe('server');
	});

	test('a row id the search and #links can use', () => {
		expect(permRowId('remote_target.view')).toBe('perm-remote_target-view');
	});
});

const role = (permissions: string[], fixed: Record<string, string> = {}) => ({
	name: 'operator',
	level: 1,
	is_super_admin: false,
	is_server_level: false,
	permissions,
	fixed
});

describe('what the role has with unsaved changes', () => {
	test('saved values come from the server; changed ones from the draft; fixed ones are off', () => {
		const r = role(['device.view'], { 'user.create': 'assigns_no_role' });
		const server = new Set(['device.view', 'audit.view', 'user.create']);
		const loaded: Record<string, Ovr> = { 'device.delete': null, 'audit.view': true, 'device.view': null };
		const values: Record<string, Ovr> = { 'device.delete': true, 'audit.view': true, 'device.view': false };
		const live = liveEffective(r, server, loaded, values, ALL);
		expect([...live].sort()).toEqual(['audit.view', 'device.delete']);
	});

	test('back to the default takes the role default', () => {
		const r = role(['device.delete']);
		const live = liveEffective(r, new Set(), { 'device.delete': false }, { 'device.delete': null }, ALL);
		expect(live.has('device.delete')).toBe(true);
	});

	test('a super admin role has everything', () => {
		const live = liveEffective({ ...role([]), is_super_admin: true }, new Set(), {}, {}, ALL);
		expect(live.size).toBe(ALL.length);
	});
});

describe('switching a permission', () => {
	test('the role default clears the override; another value sets one; back to what was saved is no change', () => {
		expect(toggleValue(true, null, false)).toBe(true);
		expect(toggleValue(false, null, false)).toBe(null);
		expect(toggleValue(false, true, false)).toBe(null);
		expect(toggleValue(true, true, false)).toBe(true);
		expect(toggleValue(false, true, true)).toBe(false);
	});
});

describe('the sentence that sums the role up', () => {
	test('names what the role can do and what it cannot, by group', () => {
		const live = new Set(['device.view', 'device.connect', 'chat.support', 'user.view', 'audit.view']);
		const s = roleSummary(live, ALL, t, and, or);
		expect(s).toBe(
			'permissions.sum_can(permissions.sum_connect_can + permissions.sum_manage_see + permissions.sum_support_can + permissions.sum_people_see + permissions.sum_server_see) ' +
				'permissions.sum_cant(permissions.sum_manage_cant_change / permissions.sum_people_cant_change / permissions.sum_server_cant_change / permissions.sum_billing_cant_any)'
		);
	});

	test('see and change in one group; nothing at all', () => {
		const s = roleSummary(
			new Set(['device.view', 'device.delete']),
			['device.view', 'device.delete'],
			t,
			and,
			or
		);
		expect(s).toBe('permissions.sum_can(permissions.sum_manage_both)');
		expect(roleSummary(new Set(), ALL, t, and, or)).toBe('permissions.sum_nothing');
	});

	test('guest links are said apart: making them is no connecting', () => {
		const s = roleSummary(
			new Set(['guest.create', 'device.view']),
			['guest.create', 'device.connect', 'device.view'],
			t,
			and,
			or
		);
		expect(s).toBe(
			'permissions.sum_can(permissions.sum_guest_create + permissions.sum_manage_see) permissions.sum_cant(permissions.sum_connect_cant)'
		);
	});

	test('joining is said apart: managing it changes no device', () => {
		const s = roleSummary(
			new Set(['device.view', 'enrollment.manage']),
			['device.view', 'device.edit', 'enrollment.manage', 'enrollment.approve'],
			t,
			and,
			or
		);
		expect(s).toBe(
			'permissions.sum_can(permissions.sum_manage_see + permissions.sum_enrollment_manage) permissions.sum_cant(permissions.sum_manage_cant_change)'
		);
	});

	test('changes without seeing', () => {
		const s = roleSummary(new Set(['device.delete']), ['device.view', 'device.delete'], t, and, or);
		expect(s).toBe(
			'permissions.sum_can(permissions.sum_manage_change) permissions.sum_cant(permissions.sum_manage_cant_see)'
		);
	});
});

const requires = {
	'device.delete': [{ permission: 'device.view', reason: 'devices_page' }],
	'device.edit': [{ permission: 'device.view', reason: 'devices_page' }]
};

describe('permissions that need others', () => {
	test('a permission that is on and needs one that is off says so; it can be turned on unless fixed', () => {
		const live = new Set(['device.delete']);
		expect(missingNeeds('device.delete', live, requires, {})).toEqual([
			{ permission: 'device.view', reason: 'devices_page', fixable: true }
		]);
		expect(missingNeeds('device.delete', live, requires, { 'device.view': 'pro_devices' })[0]?.fixable).toBe(
			false
		);
		expect(missingNeeds('device.delete', new Set(['device.delete', 'device.view']), requires, {})).toEqual(
			[]
		);
		// Off: nothing to say
		expect(missingNeeds('device.delete', new Set(), requires, {})).toEqual([]);
	});

	test('a permission that is off names the ones that are on and stop working', () => {
		expect(brokenBy('device.view', new Set(['device.delete', 'device.edit']), requires, ALL)).toEqual([
			'device.edit',
			'device.delete'
		]);
		expect(brokenBy('device.view', new Set(['device.view', 'device.delete']), requires, ALL)).toEqual([]);
	});
});

describe('views', () => {
	test('only what differs from the default: by value, or a stored override', () => {
		const r = role(['device.view'], { 'user.create': 'assigns_no_role' });
		const live = new Set(['device.delete']);
		const values: Record<string, Ovr> = { 'device.view': false, 'device.delete': true, 'audit.view': null };
		expect(ALL.filter((p) => differsFromDefault(p, r, live, values))).toEqual([
			'device.view',
			'device.delete'
		]);
		// A fixed permission with an old override still shows, so it can be reset
		expect(differsFromDefault('user.create', r, new Set(), { 'user.create': true })).toBe(true);
		expect(
			differsFromDefault('user.create', role(['user.create'], { 'user.create': 'x' }), new Set(), {})
		).toBe(false);
	});

	test('compare with a second role: its value and whether it differs', () => {
		expect(compareWith('device.view', new Set(['device.view']), new Set())).toEqual({
			other: false,
			differs: true
		});
		expect(compareWith('device.view', new Set(['device.view']), new Set(['device.view']))).toEqual({
			other: true,
			differs: false
		});
	});

	test('which roles have a permission: super admins always, the others by what they have, this one live', () => {
		const roles = [
			{ ...role([]), name: 'super_admin', is_super_admin: true },
			{ ...role([]), name: 'server_admin' },
			{ ...role([]), name: 'viewer' },
			{ ...role([]), name: 'operator' }
		];
		const effective = new Map([
			['server_admin', new Set(['server.keys'])],
			['viewer', new Set<string>()],
			['operator', new Set<string>()]
		]);
		expect(rolesWith('server.keys', roles, effective, 'viewer', new Set(['server.keys']))).toEqual([
			'super_admin',
			'server_admin',
			'viewer'
		]);
		expect(rolesWith('server.keys', roles, effective, 'server_admin', new Set())).toEqual(['super_admin']);
	});
});

describe('saving and undo', () => {
	test('the batch has one change per changed permission, null for back to the default', () => {
		const changes = [
			{ field: 'device.connect', label: '', from: '', to: '', fromValue: null, toValue: true },
			{ field: 'device.delete', label: '', from: '', to: '', fromValue: true, toValue: null }
		];
		expect(batchBody(changes)).toEqual([
			{ permission: 'device.connect', granted: true },
			{ permission: 'device.delete', granted: null }
		]);
	});

	test('undo sends back what the server says was stored before', () => {
		const previous = [
			{ permission: 'device.connect', granted: null },
			{ permission: 'device.delete', granted: true }
		];
		expect(undoBody(previous)).toEqual(previous);
		expect(undoBody(previous)).not.toBe(previous);
	});
});
