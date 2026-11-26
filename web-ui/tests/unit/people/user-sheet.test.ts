/**
 * The user sheet takes a name as long as the rule allows (3 to 64).
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import UserSheet from '../../../src/routes/(console)/users/UserSheet.svelte';
import { session } from '../../../src/lib/state/session.svelte.ts';

beforeEach(() => {
	vi.stubGlobal(
		'fetch',
		async () =>
			new Response('{"success":true,"data":{}}', { headers: { 'Content-Type': 'application/json' } })
	);
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

describe('user sheet', () => {
	test('a 43-character e-mail address as the name is typed in full', async () => {
		const user = userEvent.setup();
		render(UserSheet, {
			user: null,
			groups: [],
			roleLabel: (r: string) => r,
			providerLabel: (p: string) => p,
			onclose: () => {},
			onsaved: () => {}
		});
		const name = 'helpdesk.second-level@branch-office.example';
		expect(name).toHaveLength(43);
		const field = screen.getByLabelText('users.username');
		await user.type(field, name);
		expect(field).toHaveValue(name);
	});

	test('a Direct devices entry that is no device ID is refused at the field and nothing is sent (BUGS L397)', async () => {
		const user = userEvent.setup();
		const writes: string[] = [];
		vi.stubGlobal('fetch', async (input: string, init: RequestInit = {}) => {
			if ((init.method ?? 'GET') !== 'GET')
				writes.push(`${init.method} ${new URL(input, location.origin).pathname}`);
			return new Response('{"success":true,"data":{}}', { headers: { 'Content-Type': 'application/json' } });
		});
		render(UserSheet, {
			user: null,
			groups: [],
			roleLabel: (r: string) => r,
			providerLabel: (p: string) => p,
			onclose: () => {},
			onsaved: () => {}
		});
		await user.type(screen.getByLabelText('users.username'), 'helpdesk1');
		await user.type(screen.getByLabelText('users.password'), 'Strong-Pass-1!');
		// One row per device ID: the wrong one is named under its own row
		await user.click(screen.getByRole('button', { name: 'Add a device' }));
		await user.type(screen.getByRole('textbox', { name: 'Device 1' }), '900100001');
		await user.click(screen.getByRole('button', { name: 'Add a device' }));
		const wrong = screen.getByRole('textbox', { name: 'Device 2' });
		await user.type(wrong, '<script>');
		await user.click(screen.getByRole('button', { name: /users.create|users.add_user|actions.save/ }));
		await vi.waitFor(() => expect(wrong).toHaveAccessibleDescription(/letters, numbers/));
		expect(writes).toEqual([]);
	}, 20_000);
});
