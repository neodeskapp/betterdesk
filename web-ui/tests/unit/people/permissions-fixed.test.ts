/**
 * Permissions the code fixes for a role (BUGS L109): the sheet shows them
 * off and locked, with the reason the server names, also when an override
 * was stored for them before; Reset still clears such an override. The
 * other ticks stay as they were.
 */
import { cleanup, render, screen, within } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

const pageState = vi.hoisted(() => ({ url: new URL('http://localhost/permissions?role=pro'), state: {} }));
vi.mock('$app/state', () => ({ page: pageState }));
vi.mock('$app/paths', () => ({ resolve: (p: string) => p || '/', asset: (p: string) => '/' + p }));
vi.mock('$app/navigation', () => ({ goto: vi.fn(), pushState: vi.fn(), replaceState: vi.fn() }));

import Page from '../../../src/routes/(console)/permissions/+page.svelte';
import { session } from '../../../src/lib/state/session.svelte.ts';

beforeEach(() => {
	session.isSuperAdmin = true;
	vi.stubGlobal('fetch', async (input: string) => {
		const url = new URL(input, location.origin);
		let data: unknown = { success: false };
		if (url.pathname === '/api/panel/roles')
			data = {
				success: true,
				data: {
					roles: [
						{
							name: 'pro',
							level: 0,
							permissions: ['audit.view'],
							fixed: { 'device.view': 'pro_devices', 'user.create': 'assigns_no_role' }
						}
					],
					all_permissions: ['device.view', 'user.create', 'audit.view']
				}
			};
		if (url.pathname === '/api/panel/roles/pro/permissions')
			data = { success: true, data: { role: 'pro', permissions: ['device.view', 'audit.view'] } };
		if (url.pathname === '/api/panel/role-permissions')
			data = { success: true, data: { overrides: [{ role: 'pro', permission: 'device.view', granted: true }] } };
		return {
			ok: true,
			status: 200,
			redirected: false,
			url: url.href,
			headers: new Headers({ 'content-type': 'application/json' }),
			json: async () => data,
			text: async () => JSON.stringify(data)
		} as unknown as Response;
	});
});

afterEach(() => {
	cleanup();
	vi.unstubAllGlobals();
});

const row = (perm: string) => document.querySelector(`[data-perm="${perm}"]`) as HTMLElement;

describe('ticks the code fixes', () => {
	test('show off and locked with the reason; the others stay as they are', async () => {
		render(Page);
		await screen.findAllByRole('checkbox');
		const view = within(row('device.view')).getByRole('checkbox');
		expect(view).toBeDisabled();
		expect(view).not.toBeChecked();
		expect(within(row('device.view')).getByText('permissions.fixed_pro_devices')).toBeTruthy();
		// The stored override can still be cleared
		expect(within(row('device.view')).getByRole('button', { name: /permissions.reset_one/ })).toBeEnabled();

		const create = within(row('user.create')).getByRole('checkbox');
		expect(create).toBeDisabled();
		expect(within(row('user.create')).getByText('permissions.fixed_assigns_no_role')).toBeTruthy();

		const audit = within(row('audit.view')).getByRole('checkbox');
		expect(audit).toBeEnabled();
		expect(audit).toBeChecked();
	});
});
