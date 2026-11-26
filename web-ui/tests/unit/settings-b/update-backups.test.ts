/**
 * Restoring an update backup: without a supervisor the answer says the
 * server must be started again, and the page says so instead of reloading.
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

vi.mock('#lib/api/settings-system.ts', async (orig) => ({
	...(await orig<typeof import('../../../src/lib/api/settings-system.ts')>()),
	listUpdateBackups: vi.fn(async () => [
		{
			name: 'backup-1',
			timestamp: '2026-11-20T10:00:00Z',
			sha: 'abc',
			fileCount: 1,
			filesBackedUp: 1,
			sizeBytes: 10
		}
	]),
	getConsoleStatus: vi.fn(async () => ({ status: 'ok', cacheVersion: 'v1' })),
	restoreUpdateBackup: vi.fn(async () => ({
		restored: 1,
		consoleRestartBlocked: 'Start the server again to finish the update'
	}))
}));
vi.mock('#lib/ui/dialogs.svelte.ts', () => ({ confirm: vi.fn(async () => true) }));
const toastMock = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn() }));
vi.mock('#lib/ui/toasts.svelte.ts', async (orig) => ({
	...(await orig<typeof import('../../../src/lib/ui/toasts.svelte.ts')>()),
	toast: toastMock
}));

import * as api from '../../../src/lib/api/settings-system.ts';
import UpdateBackups from '../../../src/lib/settings-b/UpdateBackups.svelte';

beforeEach(() => {
	for (const fn of Object.values(toastMock)) fn.mockReset();
});

afterEach(() => {
	cleanup();
});

describe('update backups', () => {
	test('a restore without a supervisor says to start the server again and does not claim a restart', async () => {
		const user = userEvent.setup();
		render(UpdateBackups);
		const restore = await screen.findByRole('button', { name: /settings.updates_tab.restore_files/ });
		await user.click(restore);
		await vi.waitFor(() => expect(api.restoreUpdateBackup).toHaveBeenCalledWith('backup-1'));
		await vi.waitFor(() =>
			expect(toastMock.warning).toHaveBeenCalledWith('Backup restored. Start the server again to use it.')
		);
		expect(toastMock.success).not.toHaveBeenCalled();
	});
});
