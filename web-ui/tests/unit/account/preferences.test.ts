/**
 * Your account › Preferences is the one place whose settings apply at
 * once (PATTERN.md rule 1): it says so once for the group, and under the
 * theme it says what applies now and where that comes from (rule 2).
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import { session, type User } from '../../../src/lib/state/session.svelte.ts';
import { theme } from '../../../src/lib/state/theme.svelte.ts';

vi.mock('$app/paths', () => ({ resolve: (p: string) => '/app' + (p || '/'), asset: (p: string) => '/' + p }));

beforeEach(() => {
	session.user = { id: 7, username: 'hank', role: 'viewer', permissions: [] } as unknown as User;
	theme.consoleMode = 'dark';
	theme.choice = 'default';
});

afterEach(() => {
	cleanup();
	session.user = null;
	session.permissions = [];
	theme.consoleMode = 'system';
	theme.choice = 'default';
});

async function show() {
	const Preferences = (await import('../../../src/lib/account/Preferences.svelte')).default;
	render(Preferences);
}

test('the group says once that its changes apply at once, and for whom', async () => {
	await show();
	const group = document.getElementById('preferences')!;
	expect(group.textContent).toContain('Changes here apply at once, only for you.');
	expect(group.textContent).toContain('The language goes with your account to every device.');
});

test('with the console default it names the theme that applies now', async () => {
	await show();
	expect(screen.getByText(/Now: Dark, the console default/)).toBeTruthy();
});

test('with a theme of this browser it says so and names the console default beside it', async () => {
	theme.choice = 'light';
	await show();
	expect(screen.getByText(/Set in this browser\. The console default is Dark\./)).toBeTruthy();
});

test('a system console default says that it follows the device', async () => {
	theme.consoleMode = 'system';
	await show();
	expect(screen.getByText(/Now: System \(follows this device\), the console default/)).toBeTruthy();
});

test('the link to Settings › Appearance shows only to those who may open it', async () => {
	await show();
	expect(screen.queryByRole('link', { name: 'Settings › Appearance' })).toBeNull();
	cleanup();
	session.permissions = ['server.config'];
	await show();
	expect(screen.getByRole('link', { name: 'Settings › Appearance' }).getAttribute('href')).toBe(
		'/app/settings?tab=branding'
	);
});
