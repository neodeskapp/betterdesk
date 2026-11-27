/**
 * Your account › Preferences is the one place whose settings apply at
 * once (PATTERN.md rule 1): it says so once for the group, and under the
 * theme it says what applies now and where that comes from (rule 2).
 */
import { cleanup, fireEvent, render, screen, within } from '@testing-library/svelte';
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

test('the theme is its three choices side by side: one click picks one', async () => {
	await show();
	const group = screen.getByRole('radiogroup', { name: 'account.theme' });
	expect(group).toHaveClass('choice-segments');
	const [own, light, dark] = within(group).getAllByRole('radio');
	expect(own).toBeChecked();
	await fireEvent.click(light!);
	expect(theme.choice).toBe('light');
	expect(dark).not.toBeChecked();
});

test('each preference is a settings row with its control beside the name', async () => {
	await show();
	for (const id of [
		'preferences-theme',
		'preferences-language',
		'preferences-time-zone',
		'preferences-accessibility'
	])
		expect(document.getElementById(id)).toHaveClass('setting-row');
	expect(screen.getByRole('combobox', { name: 'settings.language' })).toBeTruthy();
	expect(screen.getByRole('button', { name: 'account.accessibility_open' })).toBeTruthy();
});
