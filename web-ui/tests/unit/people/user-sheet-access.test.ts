/**
 * The user sheet's device access (PATTERN.md rules 2, 4 and 6): direct
 * devices are one row per device ID with a message under a wrong row, the
 * part says what this user sees now (with nothing chosen, what the device
 * visibility setting gives), and an administrator role says that the
 * choices have no effect.
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import UserSheet from '../../../src/routes/(console)/users/UserSheet.svelte';
import { session } from '../../../src/lib/state/session.svelte.ts';

vi.mock('$app/paths', () => ({ resolve: (p: string) => p || '/', asset: (p: string) => '/' + p, base: '' }));

let scope = 'open';
let saved: unknown = null;

beforeEach(() => {
	scope = 'open';
	saved = null;
	vi.stubGlobal('fetch', async (input: string, init: RequestInit = {}) => {
		const path = new URL(input, location.origin).pathname;
		if (init.method === 'PUT' || init.method === 'PATCH') saved = JSON.parse(String(init.body));
		const data = path === '/api/settings/device-scope' ? { mode: scope } : {};
		return new Response(JSON.stringify({ success: true, data, ...data }), {
			headers: { 'Content-Type': 'application/json' }
		});
	});
	session.isSuperAdmin = true;
	session.user = {
		id: 1,
		username: 'root',
		role: 'super_admin',
		preferredLanguage: null,
		avatarVersion: null
	};
});

afterEach(() => {
	cleanup();
	vi.unstubAllGlobals();
	session.isSuperAdmin = false;
	session.user = null;
});

const operator = {
	id: 5,
	username: 'op',
	role: 'operator',
	email: '',
	auth_provider: 'local',
	user_groups: [],
	folder_ids: [],
	peer_grants: ['111222333', '444555666'],
	strategy_guid: ''
} as never;

function show(user: unknown = operator) {
	render(UserSheet, {
		user: user as never,
		groups: [],
		roleLabel: (r: string) => r,
		providerLabel: (p: string) => p,
		onclose: vi.fn(),
		onsaved: vi.fn()
	});
}

test('direct devices are one row per device ID', () => {
	show();
	expect(screen.getByRole('textbox', { name: 'Device 1' })).toHaveValue('111222333');
	expect(screen.getByRole('textbox', { name: 'Device 2' })).toHaveValue('444555666');
	expect(screen.getByRole('button', { name: 'Add a device' })).toBeTruthy();
});

test('a wrong device ID gets its message under its own row', async () => {
	const user = userEvent.setup();
	show();
	const second = screen.getByRole('textbox', { name: 'Device 2' });
	await user.clear(second);
	await user.type(second, 'bad id!');
	await user.tab();
	expect(second).toHaveAccessibleDescription(/letters, numbers/);
});

test('the direct devices are saved as the list of rows', async () => {
	const user = userEvent.setup();
	show();
	await user.click(screen.getByRole('button', { name: 'Add a device' }));
	await user.type(screen.getByRole('textbox', { name: 'Device 3' }), '777888999');
	await user.click(screen.getByRole('button', { name: 'actions.save' }));
	await vi.waitFor(() => expect(saved).not.toBeNull());
	expect(
		(saved as { peer_ids?: string[]; peerIds?: string[] }).peer_ids ??
			(saved as { peerIds?: string[] }).peerIds
	).toEqual(['111222333', '444555666', '777888999']);
});

test('with something chosen it says what this user sees', () => {
	show();
	expect(
		screen.getByText(
			'This user sees the devices of the chosen user groups and folders, and the direct devices.'
		)
	).toBeTruthy();
});

test('with nothing chosen and visibility Restricted it says the user sees no devices', async () => {
	scope = 'restricted';
	show({ ...(operator as object), peer_grants: [] });
	expect(
		await screen.findByText(
			'Nothing chosen: this user sees no devices, because device visibility is Restricted.'
		)
	).toBeTruthy();
});

test('with nothing chosen and visibility Open it says the user sees all devices', async () => {
	show({ ...(operator as object), peer_grants: [] });
	expect(
		await screen.findByText('Nothing chosen: this user sees all devices, because device visibility is Open.')
	).toBeTruthy();
	// The setting is on Settings › Joining, its row as the settings search has it
	const where = screen.getByRole('link', { name: 'Settings › Joining' });
	expect(where.getAttribute('href')).toBe('/settings?tab=joining#device-scope-settings-panel');
});

test('an administrator role says the choices have no effect', async () => {
	const user = userEvent.setup();
	show();
	await user.click(screen.getByRole('radio', { name: /global_admin/ }));
	expect(
		screen.getByText('Has no effect while the role is global_admin: this role always sees all devices.')
	).toBeTruthy();
});
