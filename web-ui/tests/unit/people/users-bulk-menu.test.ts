/**
 * The users page's Actions menu for selected accounts opens with its
 * groups. The page lost the MenuGroup import once, and opening the menu
 * then broke the whole page.
 */
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

const pageState = vi.hoisted(() => ({ url: new URL('http://localhost/users'), state: {} }));
vi.mock('$app/state', () => ({ page: pageState }));
vi.mock('$app/paths', () => ({ resolve: (p: string) => p || '/', asset: (p: string) => '/' + p }));
vi.mock('$app/navigation', () => ({ goto: vi.fn(), pushState: vi.fn(), replaceState: vi.fn() }));

import Page from '../../../src/routes/(console)/users/+page.svelte';
import { session } from '../../../src/lib/state/session.svelte.ts';

const users = [
	{ id: 1, username: 'admin', role: 'super_admin' },
	{ id: 2, username: 'mira', role: 'viewer', email: 'mira@example.test' },
	{ id: 3, username: 'jonas', role: 'operator', email: 'jonas@example.test' }
];

beforeEach(() => {
	session.isSuperAdmin = true;
	vi.stubGlobal('fetch', async (input: string) => {
		const url = new URL(input, location.origin);
		let data: unknown = { success: false };
		if (url.pathname === '/api/users') data = { success: true, data: { users } };
		if (url.pathname === '/api/panel/user-groups') data = { success: true, data: { groups: [] } };
		if (url.pathname === '/api/users/invites')
			data = { success: true, data: { invites: [], mail_ready: false } };
		if (url.pathname === '/api/panel/org') data = { organizations: [] };
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

describe('actions for selected accounts', () => {
	test('the menu opens with its groups', async () => {
		render(Page);
		await screen.findAllByText('jonas');
		// The first checkbox selects all; the next ones are the rows
		await fireEvent.click(screen.getAllByRole('checkbox')[2]);
		const button = await screen.findByRole('button', { name: 'users.bulk_actions' });
		await fireEvent.click(button);
		const menu = await screen.findByRole('menu');
		expect(menu.querySelectorAll('[role="group"]').length).toBeGreaterThan(1);
		expect(screen.getByRole('menuitem', { name: /users.bulk_delete_menu/ })).toBeTruthy();
	});
});
