/**
 * Your account › Email address: a new address is sent for confirmation,
 * the pending one shows with Send the link again and Cancel the change,
 * without the email settings there is no form but why and a link to them,
 * and a directory account gets no form.
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import EmailAddress from '../../../src/lib/account/EmailAddress.svelte';
import { session } from '../../../src/lib/state/session.svelte.ts';

let sent: { method: string; path: string; body: unknown }[] = [];
let own: Record<string, unknown> = {};

beforeEach(() => {
	sent = [];
	vi.stubGlobal('fetch', async (input: string, init: RequestInit = {}) => {
		const method = init.method ?? 'GET';
		const path = new URL(input, location.origin).pathname;
		if (method !== 'GET') sent.push({ method, path, body: init.body ? JSON.parse(String(init.body)) : null });
		if (method === 'POST' && path === '/api/users/me/email') {
			const body = JSON.parse(String(init.body)) as { email: string };
			own = {
				...own,
				email_pending: { email: body.email, expires_at: '2026-11-27T10:00:00Z', expired: false }
			};
		}
		if (method === 'DELETE') own = { ...own, email_pending: null };
		return new Response(JSON.stringify({ success: true, data: own }), {
			headers: { 'Content-Type': 'application/json' }
		});
	});
});

afterEach(() => {
	cleanup();
	vi.unstubAllGlobals();
	session.isSuperAdmin = false;
	session.permissions = [];
});

describe('own email address', () => {
	test('a new address waits for its confirmation and can be cancelled', async () => {
		own = { email: 'old@example.com', auth_provider: 'local', email_pending: null, mail_ready: true };
		render(EmailAddress);
		await screen.findByText('old@example.com');
		expect(screen.queryByText('users.email_pending')).toBeNull();
		await userEvent.type(screen.getByLabelText('account.email_new'), 'new@example.com');
		await userEvent.click(screen.getByRole('button', { name: 'account.email_change' }));
		await screen.findByText('users.email_pending');
		expect(sent).toEqual([
			{ method: 'POST', path: '/api/users/me/email', body: { email: 'new@example.com' } }
		]);
		// The old address stays until the link is used
		expect(screen.getByText('old@example.com')).toBeTruthy();

		await userEvent.click(screen.getByRole('button', { name: 'users.email_change_cancel' }));
		await vi.waitFor(() => expect(screen.queryByText('users.email_pending')).toBeNull());
		expect(sent.at(-1)).toEqual({ method: 'DELETE', path: '/api/users/me/email-change', body: null });
	});

	test('a pending address can be sent again', async () => {
		own = {
			email: 'old@example.com',
			auth_provider: 'local',
			email_pending: { email: 'new@example.com', expires_at: '2026-11-27T10:00:00Z', expired: false },
			mail_ready: true
		};
		render(EmailAddress);
		await userEvent.click(await screen.findByRole('button', { name: 'users.email_change_resend' }));
		await vi.waitFor(() =>
			expect(sent).toEqual([{ method: 'POST', path: '/api/users/me/email-change/resend', body: {} }])
		);
	});

	test('without the email settings there is no form, only why and, for who may, the way there', async () => {
		own = { email: 'old@example.com', auth_provider: 'local', email_pending: null, mail_ready: false };
		session.isSuperAdmin = false;
		session.permissions = [];
		render(EmailAddress);
		await screen.findByText('old@example.com');
		expect(screen.getByText('users.email_change_needs_mail')).toBeTruthy();
		expect(screen.queryByLabelText('account.email_new')).toBeNull();
		expect(screen.queryByRole('button', { name: 'account.email_change' })).toBeNull();
		expect(screen.queryByRole('link', { name: 'account.email_settings_link' })).toBeNull();
		cleanup();

		session.permissions = ['server.config'];
		render(EmailAddress);
		const settings = await screen.findByRole('link', { name: 'account.email_settings_link' });
		expect(settings.getAttribute('href')).toMatch(/\/settings\?tab=email$/);
		expect(sent).toEqual([]);
	});

	test('a directory account gets its address from the directory', async () => {
		own = { email: 'dir@example.com', auth_provider: 'ldap', email_pending: null, mail_ready: true };
		render(EmailAddress);
		await screen.findByText('dir@example.com');
		expect(screen.queryByLabelText('account.email_new')).toBeNull();
		expect(screen.getByText('users.email_from_provider_hint')).toBeTruthy();
	});
});
