/**
 * Console access: the "Role or group default" choice says what that default
 * is right now (PATTERN.md rule 2). The level is worked out as the server
 * does (console/access/policy_resolve.go mergeUserPolicy): the highest row
 * among the person's groups for the section or its catalog group, else the
 * role's limit, capped at the role's limit; a feature is on when any group
 * turns it on, off when the groups only turn it off, else on.
 */
import { cleanup, render, screen, within } from '@testing-library/svelte';
import { afterEach, beforeAll, beforeEach, describe, expect, test, vi } from 'vitest';
import { setStrings } from '../../../src/lib/i18n/i18n.svelte.ts';
import { inheritedFeature, inheritedSection } from '../../../src/lib/people/access-inherited.ts';
import { session } from '../../../src/lib/state/session.svelte.ts';
import AccessSheet from '../../../src/routes/(console)/users/AccessSheet.svelte';

describe('what the default gives', () => {
	test('without group rows the role decides', () => {
		expect(inheritedSection('users', 'administration', [], 'write')).toEqual({
			level: 'write',
			source: 'role'
		});
	});
	test('the highest group row for the section or its catalog group, capped at the role', () => {
		const rows = [
			{ resource_key: 'users', access_level: 'none' },
			{ resource_key: 'group:administration', access_level: 'write' }
		];
		expect(inheritedSection('users', 'administration', rows, 'read')).toEqual({
			level: 'read',
			source: 'group'
		});
		expect(inheritedSection('users', 'administration', [rows[0]!], 'write')).toEqual({
			level: 'none',
			source: 'group'
		});
	});
	test('features: any group on wins, groups only off make it off, else on', () => {
		expect(inheritedFeature('audio', [], false)).toEqual({ enabled: true, source: 'default' });
		expect(
			inheritedFeature(
				'audio',
				[
					{ feature_key: 'audio', enabled: false },
					{ feature_key: 'audio', enabled: true }
				],
				false
			)
		).toEqual({ enabled: true, source: 'group' });
		expect(inheritedFeature('audio', [{ feature_key: 'audio', enabled: false }], false)).toEqual({
			enabled: false,
			source: 'group'
		});
		expect(inheritedFeature('lock', [{ feature_key: 'lock', enabled: true }], true).enabled).toBe(false);
	});
});

describe('the sheet', () => {
	const catalog = {
		groups: [{ key: 'core', label: 'Core' }],
		sections: [
			{ key: 'dashboard', label: 'Dashboard', group: 'core' },
			{ key: 'devices', label: 'Devices', group: 'core' }
		],
		features: [{ key: 'audio', label: 'Audio', hardLock: false }]
	};
	const userPolicy = {
		sections: [{ resource_key: 'devices', access_level: 'none' }],
		features: [{ feature_key: 'audio', enabled: false }],
		effective: {
			sections: {
				dashboard: { access: 'read', source: 'role', role_ceiling: 'read' },
				devices: { access: 'none', source: 'user', role_ceiling: 'write' }
			},
			features: { audio: { enabled: false, source: 'user', hard_lock: false } },
			groups: [{ guid: 'g1', name: 'Helpdesk' }]
		}
	};
	const groupPolicy = {
		sections: [{ resource_key: 'devices', access_level: 'read' }],
		features: [],
		effective: { sections: {}, features: {}, groups: [] }
	};

	beforeAll(() => {
		setStrings('en', 'ltr', {
			users: {
				access_none: 'None',
				access_read: 'Read',
				access_write: 'Full',
				access_now: 'Now: {level}, {source}',
				access_source_user: 'set for this person',
				access_source_group: 'from a group',
				access_source_role: 'from the role',
				access_source_default: 'default',
				access_policy_inherit: 'Role or group default'
			},
			common: { on: 'On', off: 'Off' },
			actions: { save: 'Save', cancel: 'Cancel', close: 'Close' }
		} as never);
	});

	beforeEach(() => {
		session.user = { id: 1, username: 'demo', role: 'admin', preferredLanguage: null, avatarVersion: null };
		session.permissions = ['user.view', 'user.edit'];
		vi.stubGlobal('fetch', async (input: string) => {
			const url = new URL(input, location.origin);
			const data =
				url.pathname === '/api/panel/access/catalog'
					? catalog
					: url.pathname === '/api/panel/access/groups/g1'
						? groupPolicy
						: userPolicy;
			const payload = { success: true, data };
			return {
				ok: true,
				status: 200,
				redirected: false,
				url: url.href,
				headers: new Headers({ 'content-type': 'application/json' }),
				json: async () => payload,
				text: async () => JSON.stringify(payload)
			} as unknown as Response;
		});
	});

	afterEach(() => {
		cleanup();
		vi.unstubAllGlobals();
		session.permissions = [];
		session.user = null;
	});

	async function rowOf(selector: string): Promise<HTMLElement> {
		return vi.waitFor(() => {
			const el = document.querySelector<HTMLElement>(selector);
			if (!el) throw new Error(`no ${selector}`);
			return el;
		});
	}

	function show() {
		render(AccessSheet, {
			subject: { kind: 'user', id: 7 },
			name: 'hank',
			role: 'operator',
			roleLabel: (r: string) => r,
			onclose: () => {}
		});
	}

	test('a section the role decides names the role’s level in the default choice', async () => {
		show();
		const row = await rowOf('[data-access="dashboard"]');
		expect(within(row).getByRole('option', { name: 'Default: Read, from the role' })).toBeTruthy();
	});

	test('a section set for the person names what the groups would give instead', async () => {
		show();
		const row = await rowOf('[data-access="devices"]');
		await vi.waitFor(() =>
			expect(within(row).getByRole('option', { name: 'Default: Read, from a group' })).toBeTruthy()
		);
	});

	test('a feature set for the person names the default it would go back to', async () => {
		show();
		const row = await rowOf('[data-feature="audio"]');
		await vi.waitFor(() => expect(within(row).getByRole('option', { name: 'Default: On' })).toBeTruthy());
		expect(screen.getAllByRole('combobox').length).toBeGreaterThan(0);
	});
});
