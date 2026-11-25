/**
 * Turning two-factor sign-in off on Your account (L112): a directory or
 * single sign-on account, which has no password here, gives a current code;
 * a local account its password.
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import { session, type User } from '../../../src/lib/state/session.svelte.ts';

vi.mock('$app/paths', () => ({ resolve: (p: string) => '/app' + (p || '/'), asset: (p: string) => '/' + p }));

let sent: { url: string; body: unknown }[];
let disableWith: string;

beforeEach(() => {
	sent = [];
	disableWith = 'code';
	session.user = { id: 7, username: 'demo', role: 'viewer' } as User;
	vi.stubGlobal(
		'fetch',
		vi.fn(async (input: string, init?: RequestInit) => {
			const url = new URL(input, 'http://localhost').pathname;
			sent.push({ url, body: init?.body ? JSON.parse(String(init.body)) : null });
			const body =
				url === '/api/auth/totp/status'
					? { success: true, enabled: true, disable_with: disableWith }
					: { success: true };
			return new Response(JSON.stringify(body), { headers: { 'Content-Type': 'application/json' } });
		})
	);
});

afterEach(() => {
	cleanup();
	vi.unstubAllGlobals();
	session.user = null;
});

test('a directory account turns it off with a current code', async () => {
	const PasswordAndTwoFactor = (await import('../../../src/lib/account/PasswordAndTwoFactor.svelte')).default;
	render(PasswordAndTwoFactor);
	await userEvent.click(await screen.findByRole('button', { name: 'settings.signin.totp_turn_off' }));
	expect(document.getElementById('totp-disable-password')).toBeNull();
	await userEvent.type(document.getElementById('totp-disable-code') as HTMLElement, '123456');
	await userEvent.click(screen.getByRole('button', { name: 'settings.signin.totp_turn_off' }));
	await screen.findByText('settings.totp_disabled_success');
	expect(sent.filter((s) => s.url === '/api/auth/totp/disable').map((s) => s.body)).toEqual([
		{ code: '123456' }
	]);
}, 30_000);

test('a local account turns it off with its password', async () => {
	disableWith = 'password';
	const PasswordAndTwoFactor = (await import('../../../src/lib/account/PasswordAndTwoFactor.svelte')).default;
	render(PasswordAndTwoFactor);
	await userEvent.click(await screen.findByRole('button', { name: 'settings.signin.totp_turn_off' }));
	expect(document.getElementById('totp-disable-code')).toBeNull();
	await userEvent.type(document.getElementById('totp-disable-password') as HTMLElement, 'secret');
	await userEvent.click(screen.getByRole('button', { name: 'settings.signin.totp_turn_off' }));
	await screen.findByText('settings.totp_disabled_success');
	expect(sent.filter((s) => s.url === '/api/auth/totp/disable').map((s) => s.body)).toEqual([
		{ password: 'secret' }
	]);
}, 30_000);
