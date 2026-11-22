/**
 * New recovery codes on the Sign-in tab (L111): with two-factor sign-in on,
 * a current code makes eight new ones, shown once.
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import { session, type User } from '../../../src/lib/state/session.svelte.ts';

vi.mock('$app/paths', () => ({ resolve: (p: string) => '/app' + (p || '/'), asset: (p: string) => '/' + p }));

let sent: { url: string; body: unknown }[];

beforeEach(() => {
	sent = [];
	session.user = { id: 7, username: 'demo', role: 'viewer' } as User;
	vi.stubGlobal(
		'fetch',
		vi.fn(async (input: string, init?: RequestInit) => {
			const url = new URL(input, 'http://localhost').pathname;
			sent.push({ url, body: init?.body ? JSON.parse(String(init.body)) : null });
			const body =
				url === '/api/auth/totp/status'
					? { success: true, enabled: true }
					: url === '/api/auth/totp/recovery-codes'
						? { success: true, recoveryCodes: ['AAAA-BBBB', 'CCCC-DDDD'] }
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

test('a current code makes new recovery codes and the tab shows them', async () => {
	const SignInTab = (await import('../../../src/lib/settings/tabs/SignInTab.svelte')).default;
	render(SignInTab);
	await userEvent.click(await screen.findByRole('button', { name: 'settings.signin.recovery_new' }));
	await userEvent.type(screen.getByLabelText('auth.totp_code'), '123456');
	await userEvent.click(screen.getByRole('button', { name: 'settings.signin.recovery_new' }));
	expect(await screen.findByText('settings.signin.recovery_renewed')).toBeInTheDocument();
	expect(screen.getByText(/AAAA-BBBB/)).toBeInTheDocument();
	expect(sent.filter((s) => s.url === '/api/auth/totp/recovery-codes').map((s) => s.body)).toEqual([
		{ code: '123456' }
	]);
}, 30_000);
