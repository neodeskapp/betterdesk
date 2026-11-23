/**
 * Settings › Sign-in refuses numbers it can't use instead of replacing
 * them (L418): LDAP port and timeout go to the server as typed, and the
 * client session days are checked on the field before anything is sent.
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import { ldapFromServer, ldapPayload } from '../../../src/lib/settings/signin.ts';
import { session, type User } from '../../../src/lib/state/session.svelte.ts';

vi.mock('$app/paths', () => ({ resolve: (p: string) => '/app' + (p || '/'), asset: (p: string) => '/' + p }));

test('LDAP numbers go to the server as typed, an empty one as no value', () => {
	const base = ldapFromServer({ enabled: true, host: 'ldap.example.org' });
	expect(ldapPayload({ ...base, port: 2.6, conn_timeout_sec: 0 })).toMatchObject({
		port: 2.6,
		conn_timeout_sec: 0
	});
	expect(ldapPayload({ ...base, port: 99999 })).toMatchObject({ port: 99999 });
	expect(ldapPayload({ ...base, port: null, conn_timeout_sec: null })).toMatchObject({
		port: null,
		conn_timeout_sec: null
	});
});

let sent: { url: string; method: string; body: unknown }[];

beforeEach(() => {
	sent = [];
	session.user = { id: 1, username: 'admin', role: 'admin' } as User;
	session.permissions = ['server.config'];
	vi.stubGlobal(
		'fetch',
		vi.fn(async (input: string, init?: RequestInit) => {
			const url = new URL(input, 'http://localhost').pathname;
			sent.push({
				url,
				method: init?.method ?? 'GET',
				body: init?.body ? JSON.parse(String(init.body)) : null
			});
			const body =
				url === '/api/settings/client-sessions'
					? { success: true, data: { expiry_days: 9, sliding: true, max_days: 30 } }
					: { success: true, data: {} };
			return new Response(JSON.stringify(body), { headers: { 'Content-Type': 'application/json' } });
		})
	);
});

afterEach(() => {
	cleanup();
	vi.unstubAllGlobals();
	session.user = null;
	session.permissions = [];
});

const saves = () => sent.filter((s) => s.url === '/api/settings/client-sessions' && s.method === 'PUT');

test('session days out of range are refused on the field, nothing is sent', async () => {
	const SignInTab = (await import('../../../src/lib/settings/tabs/SignInTab.svelte')).default;
	render(SignInTab);
	const days = await screen.findByLabelText('settings.client_sessions_expiry_days');
	// The saved values are in before anything is typed
	await vi.waitFor(() => expect(days).toHaveValue(9));
	await userEvent.clear(days);
	await userEvent.type(days, '0');
	await userEvent.click(screen.getByRole('button', { name: 'settings.client_sessions_save' }));
	expect(await screen.findByText('settings.signin.check_days_range')).toBeInTheDocument();
	expect(saves()).toEqual([]);

	await userEvent.clear(days);
	await userEvent.type(days, '40');
	await userEvent.click(screen.getByRole('button', { name: 'settings.client_sessions_save' }));
	expect(await screen.findByText('settings.signin.check_max_below_duration')).toBeInTheDocument();
	expect(saves()).toEqual([]);
}, 30_000);
