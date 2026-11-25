/**
 * Inviting by email: the Add user sheet sends an email address and no
 * password to /api/users/invite, suggests the username the server will
 * give, and offers the invite only when the console can send mail. The
 * set-password page takes the token from a link.
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import UserSheet from '../../../src/routes/(console)/users/UserSheet.svelte';
import { linkToken } from '../../../src/lib/api/passwordlinks.ts';
import { usernameFromEmail } from '../../../src/lib/people/users.ts';
import { session } from '../../../src/lib/state/session.svelte.ts';

interface Sent {
	method: string;
	path: string;
	body: Record<string, unknown>;
}

let sent: Sent[] = [];

beforeEach(() => {
	sent = [];
	vi.stubGlobal('fetch', async (input: string, init: RequestInit = {}) => {
		const method = init.method ?? 'GET';
		if (method !== 'GET')
			sent.push({
				method,
				path: new URL(input, location.origin).pathname,
				body: JSON.parse(String(init.body ?? '{}'))
			});
		return new Response('{"success":true,"data":{"id":5,"username":"jannis","invite_sent":true}}', {
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

function sheet(mailReady: boolean, onsaved = () => {}) {
	render(UserSheet, {
		user: null,
		groups: [],
		roleLabel: (r: string) => r,
		providerLabel: (p: string) => p,
		mailReady,
		onclose: () => {},
		onsaved
	});
}

describe('username from an email address', () => {
	test.each([
		['jannis@example.com', 'jannis'],
		['Jannis.G+desk@example.com', 'Jannis.Gdesk'],
		['.a-@example.com', 'a__'],
		['ü@example.com', '___'],
		['no address', '']
	])('%s gives %s', (email, want) => {
		expect(usernameFromEmail(email)).toBe(want);
	});
});

describe('link token', () => {
	const token = 'Abc_def-0123456789xyzABCDEFGHIJ';
	test.each([
		[`#${token}`, token],
		[`https://desk.example/set-password#${token}`, token],
		[`  ${token}  `, token],
		['#short', ''],
		['#has space in it but is long enough', '']
	])('%s', (value, want) => {
		expect(linkToken(value)).toBe(want);
	});
});

describe('add user by invite', () => {
	test('an invite sends the address and no password, and the username only when typed', async () => {
		const user = userEvent.setup();
		const saved = vi.fn();
		sheet(true, saved);
		expect(screen.queryByLabelText('users.password')).toBeNull();
		await user.type(screen.getByLabelText('users.email'), 'jannis@example.com');
		expect(screen.getByLabelText('users.username')).toHaveAttribute('placeholder', 'jannis');
		await user.click(screen.getByRole('button', { name: 'users.send_invite' }));
		await vi.waitFor(() => expect(saved).toHaveBeenCalled());
		expect(sent).toHaveLength(1);
		expect(sent[0]?.path).toBe('/api/users/invite');
		expect(sent[0]?.body.email).toBe('jannis@example.com');
		expect(sent[0]?.body).not.toHaveProperty('password');
		expect(sent[0]?.body).not.toHaveProperty('username');
	});

	test('an invite without an address is refused at the field', async () => {
		const user = userEvent.setup();
		sheet(true);
		await user.click(screen.getByRole('button', { name: 'users.send_invite' }));
		await vi.waitFor(() =>
			expect(screen.getByLabelText('users.email')).toHaveAccessibleDescription(/users.invite_email_required/)
		);
		expect(sent).toEqual([]);
	});

	test('without email settings the invite is off and the sheet asks for a password', async () => {
		const user = userEvent.setup();
		sheet(false);
		expect(screen.getByRole('radio', { name: /users.add_mode_invite/ })).toBeDisabled();
		await user.type(screen.getByLabelText('users.username'), 'helpdesk1');
		await user.type(screen.getByLabelText('users.password'), 'Strong-Pass-1!');
		await user.click(screen.getByRole('button', { name: 'users.create' }));
		await vi.waitFor(() => expect(sent).toHaveLength(1));
		expect(sent[0]?.path).toBe('/api/users');
		expect(sent[0]?.body.password).toBe('Strong-Pass-1!');
	});
});
