import { render, screen, cleanup } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, expect, test, vi } from 'vitest';
import { session, type User } from '../../../src/lib/state/session.svelte.ts';

vi.mock('$app/paths', () => ({ resolve: (p: string) => '/app' + (p || '/'), asset: (p: string) => '/' + p }));

afterEach(() => {
	cleanup();
	session.user = null;
});

test("Signed-in devices opens the list of the account's own sessions (A239)", async () => {
	session.user = { id: 7, username: 'demo_ui_admin', role: 'operator' } as User;
	const fetchMock = vi.fn(async (_input: RequestInfo | URL) =>
		Response.json({ success: true, data: { sessions: [], limit: { max: 10, source: 'default' } } })
	);
	vi.stubGlobal('fetch', fetchMock);
	const AccountMenu = (await import('../../../src/lib/shell/AccountMenu.svelte')).default;
	render(AccountMenu);
	await userEvent.click(screen.getByRole('button', { name: /demo_ui_admin/ }));
	await userEvent.click(screen.getByRole('menuitem', { name: 'sessions.title' }));
	expect(await screen.findByRole('dialog', { name: 'sessions.title' })).toBeInTheDocument();
	await vi.waitFor(() => expect(String(fetchMock.mock.calls[0]?.[0])).toMatch(/\/api\/users\/me\/sessions$/));
	vi.unstubAllGlobals();
});

test('Change password and Two-factor sign-in open their own parts of the Sign-in tab', async () => {
	session.user = { id: 7, username: 'demo_ui_admin', role: 'admin' } as User;
	const AccountMenu = (await import('../../../src/lib/shell/AccountMenu.svelte')).default;
	render(AccountMenu);
	await userEvent.click(screen.getByRole('button', { name: /demo_ui_admin/ }));
	// The settings host jumps to #<id> and reveals it; both ids are on the Sign-in tab
	expect(screen.getByRole('menuitem', { name: 'auth.change_password' }).getAttribute('href')).toMatch(
		/\/settings\?tab=auth#account-password$/
	);
	expect(screen.getByRole('menuitem', { name: 'nav.account_2fa' }).getAttribute('href')).toMatch(
		/\/settings\?tab=auth#auth-panel-totp$/
	);
});
