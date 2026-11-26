/**
 * The user's page (/users/<id>) as people see it: with user.view alone
 * everything shows and no action does; with the rights to change the
 * account the actions are there in their groups, the dangerous ones
 * apart; an id without an account says so and leads back to the list.
 */
import { cleanup, render, screen, within } from '@testing-library/svelte';
import { afterEach, beforeAll, beforeEach, describe, expect, test, vi } from 'vitest';

const pageState = vi.hoisted(() => ({ params: { id: '5' }, url: new URL('http://localhost/users/5') }));
vi.mock('$app/state', () => ({ page: pageState }));
vi.mock('$app/paths', () => ({ resolve: (p: string) => p || '/', asset: (p: string) => '/' + p }));
vi.mock('$app/navigation', () => ({ goto: vi.fn(), pushState: vi.fn(), replaceState: vi.fn() }));

const { default: Page } = await import('../../../src/routes/(console)/users/[id]/+page.svelte');
const { session } = await import('../../../src/lib/state/session.svelte.ts');
const { setStrings } = await import('../../../src/lib/i18n/i18n.svelte.ts');

beforeAll(async () => {
	// Compiled once here, so the first test doesn't wait for it
	await import('../../../src/routes/(console)/users/UserSheet.svelte');
}, 60_000);

const OLGA = {
	id: 5,
	username: 'olga',
	display_name: 'Olga Ober',
	first_name: 'Olga',
	last_name: 'Ober',
	role: 'operator',
	email: 'olga@example.test',
	auth_provider: 'local',
	created_at: '2026-01-02 03:04:05',
	last_login: '2026-11-01 10:00:00',
	user_groups: [],
	folder_ids: [],
	peer_grants: [],
	strategy_guid: '',
	disabled: false,
	locked: false,
	locks: [],
	must_change_password: false,
	two_factor_required: false,
	two_factor_state: 'off',
	two_factor_due: false,
	password_reset_until: null,
	preferred_language: '',
	time_zone: 'Europe/Berlin',
	invite: null,
	permissions: ['device.view', 'device.connect', 'chat.team'],
	team_chat: true,
	last_super_admin: false
};

let answers: Record<string, { status?: number; body: unknown }>;

beforeEach(() => {
	answers = {
		'/api/users/5': { body: { success: true, data: OLGA } },
		'/api/users/5/effective-scope': { body: { success: true, data: { count: 3, restricted: true } } },
		'/api/users/5/organizations': { body: { organizations: [] } },
		'/api/panel/user-groups': { body: { success: true, data: { groups: [] } } },
		'/api/users/invites': { body: { success: true, data: { invites: [], mail_ready: false } } },
		'/api/users/5/api-tokens': { body: { success: true, data: { tokens: [] } } },
		'/api/users/5/activity': { body: { success: true, data: { entries: [] } } },
		'/api/users/5/security-events': { body: { success: true, data: { events: [] } } },
		'/api/users/5/sessions': { body: { success: true, data: { sessions: [] } } },
		'/api/users/999': { status: 404, body: { success: false, error: 'User not found' } }
	};
	vi.stubGlobal('fetch', async (input: string) => {
		const url = new URL(input, 'http://localhost');
		const a = answers[url.pathname] ?? { status: 404, body: { success: false, error: 'not here' } };
		return new Response(JSON.stringify(a.body), {
			status: a.status ?? 200,
			headers: { 'content-type': 'application/json' }
		});
	});
	pageState.params = { id: '5' };
});

afterEach(() => {
	cleanup();
	vi.unstubAllGlobals();
	session.user = null;
	session.permissions = [];
	session.isSuperAdmin = false;
	setStrings('en', 'ltr', {});
});

function as(role: string, permissions: string[], id = 9) {
	session.user = { id, username: 'me', role, preferredLanguage: null, avatarVersion: null };
	session.permissions = permissions;
	session.isSuperAdmin = role === 'super_admin';
}

describe("the user's page", () => {
	test('read-only with user.view: the profile and what the role allows, no actions that change it', async () => {
		as('server_admin', ['user.view']);
		setStrings('en', 'ltr', { users: { can_device_connect: 'Can connect to devices' } });
		render(Page);
		expect(await screen.findByRole('heading', { level: 1, name: 'Olga Ober' })).toBeTruthy();
		expect(screen.getByText('Europe/Berlin')).toBeTruthy();
		// The role in sentences, never permission ids
		const role = screen.getByRole('heading', { name: 'users.page_role' }).closest('section')!;
		expect(within(role).getByText('Can connect to devices')).toBeTruthy();
		expect(within(role).queryByText('device.connect')).toBeNull();
		// Nothing that changes the account
		expect(screen.queryByRole('button', { name: 'users.edit' })).toBeNull();
		expect(screen.queryByRole('button', { name: 'users.edit_profile' })).toBeNull();
		expect(screen.queryByRole('button', { name: 'users.disable_menu_ask' })).toBeNull();
		expect(screen.queryByRole('button', { name: 'users.edit_menu' })).toBeNull();
		expect(screen.queryByRole('button', { name: 'users.delete_menu' })).toBeNull();
		expect(screen.queryByRole('button', { name: 'users.reset_password_menu' })).toBeNull();
		// Showing things stays: the sign-in link and the organizations
		expect(screen.getByRole('button', { name: 'users.copy_sign_in_link' })).toBeTruthy();
		expect(await screen.findByText('users.page_sessions_need_edit')).toBeTruthy();
	});

	test('a super admin gets the actions in their groups and the dangerous ones at the end', async () => {
		as(
			'super_admin',
			['user.view', 'user.edit', 'user.delete', 'user.security', 'chat.team', 'audit.view'],
			1
		);
		render(Page);
		await screen.findByRole('heading', { level: 1, name: 'Olga Ober' });
		const side = screen.getByRole('complementary');
		expect(within(side).getByRole('button', { name: 'users.send_message' })).toBeTruthy();
		expect(within(side).getByRole('button', { name: 'users.reset_password_menu' })).toBeTruthy();
		expect(within(side).queryByRole('button', { name: 'users.delete_menu' })).toBeNull();
		const danger = screen.getByRole('heading', { name: 'users.menu_group_danger' }).closest('section')!;
		expect(within(danger).getByRole('button', { name: /users.delete_menu/ })).toBeTruthy();
		expect(within(danger).getByRole('button', { name: /users.disable_menu/ })).toBeTruthy();
		expect(screen.getByRole('button', { name: 'users.edit_profile' })).toBeTruthy();
	});

	test('an id without an account says so and leads back', async () => {
		as('super_admin', ['user.view'], 1);
		pageState.params = { id: '999' };
		render(Page);
		expect(await screen.findByText('users.page_not_found')).toBeTruthy();
		expect(screen.getAllByRole('link', { name: 'users.page_back' }).length).toBeGreaterThan(0);
		expect(screen.queryByRole('button', { name: 'actions.retry' })).toBeNull();
	});
});
