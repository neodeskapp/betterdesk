/**
 * Sign in as…: the dialog asks the server first. When the account may not
 * be viewed it says why and offers no Start; when it may, it asks what the
 * server asks for (the password, the code when two-factor is on, a
 * reason) and sends the start with them.
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, test, vi } from 'vitest';
import SignInAsDialog from '../../../src/routes/(console)/users/SignInAsDialog.svelte';
import type { ConsoleUser } from '../../../src/lib/api/users.ts';

afterEach(() => {
	cleanup();
	vi.unstubAllGlobals();
});

const olga = { id: 5, username: 'olga', role: 'operator' } as unknown as ConsoleUser;

function server(check: Record<string, unknown>) {
	const calls: { url: string; body: unknown }[] = [];
	vi.stubGlobal('fetch', (url: string, init: RequestInit = {}) => {
		calls.push({ url, body: init.body ? JSON.parse(String(init.body)) : null });
		const data = url.endsWith('/impersonation') ? check : { active: true };
		return Promise.resolve(
			new Response(JSON.stringify({ success: true, data }), {
				headers: { 'content-type': 'application/json' }
			})
		);
	});
	return calls;
}

describe('Sign in as dialog', () => {
	test('says why an account may not be viewed and offers no Start', async () => {
		server({
			allowed: false,
			code: 'impersonation_role',
			error: 'olga has the same role as you',
			username: 'olga',
			needs: { password: true, code: false }
		});
		render(SignInAsDialog, { user: olga, onclose: () => {} });
		expect(await screen.findByText('olga has the same role as you')).toBeTruthy();
		expect(screen.queryByRole('button', { name: 'impersonation.start' })).toBeNull();
	});

	test('asks for the password, the code and a reason, and sends them', async () => {
		const assign = vi.fn();
		vi.stubGlobal('location', { ...location, assign });
		const calls = server({
			allowed: true,
			username: 'olga',
			needs: { password: true, code: true },
			minutes: 15,
			notice_mail: true
		});
		render(SignInAsDialog, { user: olga, onclose: () => {} });
		const user = userEvent.setup();
		await user.type(await screen.findByLabelText('impersonation.your_password'), 'secret pw');
		await user.type(screen.getByLabelText('impersonation.your_code'), '123456');
		await user.type(screen.getByLabelText(/impersonation.reason/), 'Ticket 4711');
		await user.click(screen.getByRole('button', { name: 'impersonation.start' }));
		await vi.waitFor(() => expect(assign).toHaveBeenCalledWith('/'));
		const start = calls.find((c) => c.url.endsWith('/impersonate'));
		expect(start?.body).toEqual({ password: 'secret pw', code: '123456', reason: 'Ticket 4711' });
	});

	test('does not start without a reason', async () => {
		const calls = server({ allowed: true, username: 'olga', needs: { password: true, code: false } });
		render(SignInAsDialog, { user: olga, onclose: () => {} });
		const user = userEvent.setup();
		await user.type(await screen.findByLabelText('impersonation.your_password'), 'secret pw');
		await user.click(screen.getByRole('button', { name: 'impersonation.start' }));
		expect(await screen.findByText('impersonation.reason_required')).toBeTruthy();
		expect(calls.some((c) => c.url.endsWith('/impersonate'))).toBe(false);
	});
});
