/**
 * Server › Updates: the update channel is a setting (PATTERN.md rules 1, 2
 * and 7). Picking a channel saves nothing; the save bar names the change
 * and Save switches it. The channel in force is said once (the side
 * column); under the choice a line says it only while another one is
 * picked. The group says once when a change applies, without repeating
 * the install note of Software updates. Check for updates and Install
 * stay buttons.
 */
import { cleanup, render, screen, within } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';

vi.mock('$app/paths', () => ({ resolve: (p: string) => p || '/', asset: (p: string) => '/' + p }));
vi.mock('#lib/api/settings-system.ts', async (orig) => ({
	...(await orig<typeof import('../../../src/lib/api/settings-system.ts')>()),
	getUpdateChannel: vi.fn(async () => ({ channel: 'stable', owner: 'neodeskapp', repo: 'neodesk' })),
	setUpdateChannel: vi.fn(async () => ({ branch: 'dev', previousBranch: 'main' })),
	getServerBinaryStatus: vi.fn(async () => ({ dockerMode: false, stale: false })),
	getLastUpdateResult: vi.fn(async () => null),
	checkForUpdates: vi.fn()
}));
const confirmMock = vi.hoisted(() => vi.fn(async () => true));
vi.mock('#lib/ui/dialogs.svelte.ts', () => ({ confirm: confirmMock }));
const toastMock = vi.hoisted(() => ({
	show: vi.fn(),
	success: vi.fn(),
	error: vi.fn(),
	warning: vi.fn(),
	info: vi.fn()
}));
vi.mock('#lib/ui/toasts.svelte.ts', async (orig) => ({
	...(await orig<typeof import('../../../src/lib/ui/toasts.svelte.ts')>()),
	toast: toastMock
}));

import * as api from '../../../src/lib/api/settings-system.ts';
import UpdatesTab from '../../../src/lib/settings/tabs/UpdatesTab.svelte';
import { anyDirty } from '../../../src/lib/settings/dirty.ts';
import { session } from '../../../src/lib/state/session.svelte.ts';

beforeEach(() => {
	session.isSuperAdmin = true;
	vi.mocked(api.setUpdateChannel).mockClear();
	vi.mocked(api.getUpdateChannel).mockClear();
	confirmMock.mockClear();
	for (const fn of Object.values(toastMock)) fn.mockReset();
});

afterEach(() => {
	cleanup();
	session.isSuperAdmin = false;
});

async function pickDevelopment() {
	const user = userEvent.setup();
	render(UpdatesTab);
	const group = await screen.findByRole('radiogroup', { name: 'updates.channel_title' });
	await vi.waitFor(() => expect(api.getUpdateChannel).toHaveBeenCalled());
	await user.click(within(group).getByRole('radio', { name: /settings.updates_tab.channel_development/ }));
	return user;
}

test('the channel in force is said under the choice only while another one is picked', async () => {
	await pickDevelopment();
	expect(screen.getByText(/Now: Stable\. Releases of neodeskapp\/neodesk on GitHub\./)).toBeTruthy();
});

test('with the saved channel picked, the choice has no line repeating it', async () => {
	render(UpdatesTab);
	await vi.waitFor(() => expect(api.getUpdateChannel).toHaveBeenCalled());
	const panel = await screen.findByRole('region', { name: 'updates.channel_title' });
	await vi.waitFor(() => expect(within(panel).getByRole('radio', { name: /channel_stable/ })).toBeChecked());
	expect(panel.textContent).not.toContain('Now: Stable');
});

test('the group says once when a change of channel applies', async () => {
	render(UpdatesTab);
	const panel = await screen.findByRole('region', { name: 'updates.channel_title' });
	expect(panel.textContent).toContain('Applies to the next check for updates.');
	// Software updates above already says nothing is installed before Install
	expect(panel.textContent).not.toContain('Nothing is installed');
});

test('picking a channel saves nothing and the save bar names the change', async () => {
	await pickDevelopment();
	expect(api.setUpdateChannel).not.toHaveBeenCalled();
	expect(confirmMock).not.toHaveBeenCalled();
	const bar = screen.getByRole('region', { name: 'Unsaved changes' });
	expect(bar.textContent).toContain('updates.channel_title:');
	expect(bar.textContent).toContain('Stable → Development');
	expect(anyDirty()).toBe(true);
});

test('Save switches the channel and the toast names it', async () => {
	const user = await pickDevelopment();
	await user.click(screen.getByRole('button', { name: 'updates.channel_save' }));
	await vi.waitFor(() => expect(api.setUpdateChannel).toHaveBeenCalledWith('development'));
	await vi.waitFor(() =>
		expect(toastMock.show).toHaveBeenCalledWith(
			expect.objectContaining({ message: 'Saved: updates.channel_title Development.' })
		)
	);
	expect(anyDirty()).toBe(false);
});

test('Discard puts the saved channel back', async () => {
	const user = await pickDevelopment();
	await user.click(screen.getByRole('button', { name: 'Discard' }));
	expect(screen.getByRole('radio', { name: /settings.updates_tab.channel_stable/ })).toBeChecked();
	expect(screen.queryByRole('region', { name: 'Unsaved changes' })).toBeNull();
});
