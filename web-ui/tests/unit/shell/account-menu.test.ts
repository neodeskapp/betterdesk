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

test('Your account opens the account page; API tokens open their part of it', async () => {
	session.user = { id: 7, username: 'demo_ui_admin', role: 'admin' } as User;
	const AccountMenu = (await import('../../../src/lib/shell/AccountMenu.svelte')).default;
	render(AccountMenu);
	await userEvent.click(screen.getByRole('button', { name: /demo_ui_admin/ }));
	expect(screen.getByRole('menuitem', { name: 'account.title' }).getAttribute('href')).toBe('/app/account');
	expect(screen.getByRole('menuitem', { name: 'api_tokens.title' }).getAttribute('href')).toBe(
		'/app/account#api-tokens'
	);
	// Password and two-factor sign-in are parts of Your account now, not menu items into Settings
	expect(screen.queryByRole('menuitem', { name: 'auth.change_password' })).toBeNull();
	expect(screen.queryByRole('menuitem', { name: 'nav.account_2fa' })).toBeNull();
});

test('an account that may not make API tokens gets no API tokens item', async () => {
	session.user = { id: 8, username: 'demo_ui_viewer', role: 'viewer' } as User;
	const AccountMenu = (await import('../../../src/lib/shell/AccountMenu.svelte')).default;
	render(AccountMenu);
	await userEvent.click(screen.getByRole('button', { name: /demo_ui_viewer/ }));
	expect(screen.getByRole('menuitem', { name: 'account.title' })).toBeTruthy();
	expect(screen.queryByRole('menuitem', { name: 'api_tokens.title' })).toBeNull();
});
