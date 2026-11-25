/**
 * Reset password: with mail settings and an address the dialog sends a
 * reset link and asks for no password; without an address the link is off
 * and the dialog sets a password as before.
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import ResetPasswordDialog from '../../../src/routes/(console)/users/ResetPasswordDialog.svelte';
import { ConsoleUser } from '../../../src/lib/api/users.ts';
import * as v from 'valibot';

let sent: { path: string; body: Record<string, unknown> }[] = [];

beforeEach(() => {
	sent = [];
	vi.stubGlobal('fetch', async (input: string, init: RequestInit = {}) => {
		if ((init.method ?? 'GET') !== 'GET')
			sent.push({
				path: new URL(input, location.origin).pathname,
				body: JSON.parse(String(init.body ?? '{}'))
			});
		return new Response('{"success":true}', { headers: { 'Content-Type': 'application/json' } });
	});
});

afterEach(() => {
	cleanup();
	vi.unstubAllGlobals();
});

const account = (email: string) => v.parse(ConsoleUser, { id: 7, username: 'carol', role: 'viewer', email });

describe('reset password dialog', () => {
	test('with an address it sends a link and no password', async () => {
		const closed = vi.fn();
		render(ResetPasswordDialog, { user: account('carol@example.com'), mailReady: true, onclose: closed });
		expect(screen.queryByLabelText('users.new_password')).toBeNull();
		await userEvent.click(screen.getByRole('button', { name: 'users.send_reset_link' }));
		await vi.waitFor(() => expect(closed).toHaveBeenCalled());
		expect(sent).toEqual([{ path: '/api/users/7/password-reset-link', body: {} }]);
	});

	test('without an address the link is off and a password is set', async () => {
		const closed = vi.fn();
		render(ResetPasswordDialog, { user: account(''), mailReady: true, onclose: closed });
		expect(screen.getByRole('radio', { name: /users.reset_mode_link/ })).toBeDisabled();
		await userEvent.type(screen.getByLabelText('users.new_password'), 'New-Pass-456!');
		await userEvent.click(screen.getByRole('button', { name: 'users.set_password' }));
		await vi.waitFor(() => expect(closed).toHaveBeenCalled());
		expect(sent).toEqual([{ path: '/api/users/7/reset-password', body: { newPassword: 'New-Pass-456!' } }]);
	});
});
