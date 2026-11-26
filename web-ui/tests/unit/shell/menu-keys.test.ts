/**
 * The top bar's own menus follow the APG menu button: ArrowUp opens on
 * the last item, and everything the bell's menu says can be reached with
 * the arrow keys (screen readers skip text that isn't an item).
 */
import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, expect, test, vi } from 'vitest';
import { session, type User } from '../../../src/lib/state/session.svelte.ts';
import { resetNotifications } from '../../../src/lib/shell/notifications.svelte.ts';

// The shell's menus pull in much of the app on first import
vi.setConfig({ testTimeout: 30_000 });

vi.mock('$app/paths', () => ({ resolve: (p: string) => '/app' + (p || '/'), asset: (p: string) => '/' + p }));

afterEach(() => {
	session.user = null;
	resetNotifications();
	vi.unstubAllGlobals();
});

const ITEMS = [
	{ id: 29, kind: 'help_request', title: 'demo-reception-01', message: 'Printer', read: 1 },
	{ id: 30, kind: 'help_request', title: 'demo-office-02', message: 'Mail', read: 1 }
];

test('ArrowUp on the account button opens the menu on its last item', async () => {
	session.user = { id: 7, username: 'demo_ui_admin', role: 'admin' } as User;
	const AccountMenu = (await import('../../../src/lib/shell/AccountMenu.svelte')).default;
	render(AccountMenu);
	const button = screen.getByRole('button', { name: /demo_ui_admin/ });
	button.focus();
	await userEvent.keyboard('{ArrowUp}');
	const items = screen.getAllByRole('menu')[0]!.querySelectorAll('[role^="menuitem"]');
	expect(document.activeElement).toBe(items[items.length - 1]);
	await userEvent.keyboard('{Escape}');
	await userEvent.keyboard('{ArrowDown}');
	expect(document.activeElement).toBe(screen.getAllByRole('menu')[0]!.querySelector('[role^="menuitem"]'));
});

test('the bell: Loading is an item focus lands on, then the first row; ArrowUp opens on the last', async () => {
	session.user = { id: 7, username: 'demo_ui_admin', role: 'viewer' } as User;
	// The first load waits until the test answers it; later ones answer at once
	let answer: (r: Response) => void = () => {};
	const list = () => Response.json({ success: true, items: ITEMS, unread_count: 0 });
	vi.stubGlobal(
		'fetch',
		vi
			.fn()
			.mockImplementationOnce(
				() =>
					new Promise<Response>((resolve) => {
						answer = resolve;
					})
			)
			.mockImplementation(async () => list())
	);
	const NotificationsMenu = (await import('../../../src/lib/shell/NotificationsMenu.svelte')).default;
	render(NotificationsMenu);
	const bell = screen.getByRole('button', { name: 'notifications.title' });
	bell.focus();
	await userEvent.keyboard('{ArrowDown}');
	const loading = await screen.findByRole('menuitem', { name: 'notifications.loading' });
	await vi.waitFor(() => expect(loading).toHaveFocus());
	expect(loading).toHaveAttribute('aria-disabled', 'true');

	answer(list());
	const first = await screen.findByRole('menuitem', { name: /demo-reception-01/ });
	await vi.waitFor(() => expect(first).toHaveFocus());

	await userEvent.keyboard('{Escape}');
	expect(bell).toHaveFocus();
	await userEvent.keyboard('{ArrowUp}');
	await vi.waitFor(() => expect(screen.getByRole('menuitem', { name: /demo-office-02/ })).toHaveFocus());
});

test('the bell: with nothing to show, the empty sentence is an item', async () => {
	session.user = { id: 7, username: 'demo_ui_admin', role: 'viewer' } as User;
	vi.stubGlobal(
		'fetch',
		vi.fn(async () => Response.json({ success: true, items: [], unread_count: 0 }))
	);
	const NotificationsMenu = (await import('../../../src/lib/shell/NotificationsMenu.svelte')).default;
	render(NotificationsMenu);
	await userEvent.click(screen.getByRole('button', { name: 'notifications.title' }));
	const empty = await screen.findByRole('menuitem', { name: 'notifications.empty_hint' });
	await vi.waitFor(() => expect(empty).toHaveFocus());
});
