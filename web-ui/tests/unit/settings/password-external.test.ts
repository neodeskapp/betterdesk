/**
 * Settings › Sign-in for a directory or single sign-on account (L345): its
 * password lives in the directory or at the provider, so the tab says so
 * instead of offering a Change password form that can never work.
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import { session, type User } from '../../../src/lib/state/session.svelte.ts';

vi.mock('$app/paths', () => ({ resolve: (p: string) => '/app' + (p || '/'), asset: (p: string) => '/' + p }));

let disableWith: 'password' | 'code';

beforeEach(() => {
	session.user = { id: 7, username: 'hank', role: 'viewer' } as User;
	vi.stubGlobal(
		'fetch',
		vi.fn(async (input: string) => {
			const url = new URL(input, 'http://localhost').pathname;
			const body =
				url === '/api/auth/totp/status'
					? { success: true, enabled: false, disable_with: disableWith }
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

test('a directory account gets a note instead of the password form', async () => {
	disableWith = 'code';
	const SignInTab = (await import('../../../src/lib/settings/tabs/SignInTab.svelte')).default;
	render(SignInTab);
	expect(await screen.findByText('settings.signin.password_external')).toBeInTheDocument();
	expect(screen.queryByLabelText('settings.current_password')).toBeNull();
	expect(screen.queryByRole('button', { name: 'settings.save_password' })).toBeNull();
}, 30_000);

test('a local account keeps the password form', async () => {
	disableWith = 'password';
	const SignInTab = (await import('../../../src/lib/settings/tabs/SignInTab.svelte')).default;
	render(SignInTab);
	expect(await screen.findByLabelText('settings.current_password')).toBeInTheDocument();
	expect(screen.queryByText('settings.signin.password_external')).toBeNull();
}, 30_000);
