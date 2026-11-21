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
});
