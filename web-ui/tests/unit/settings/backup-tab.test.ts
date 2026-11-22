/**
 * Settings › Backup: backups go to a folder; the network share login is
 * gone (L122). A share is mounted by the operating system at the folder.
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

vi.mock('#lib/api/settings-system.ts', async (orig) => ({
	...(await orig<typeof import('../../../src/lib/api/settings-system.ts')>()),
	getBackupStats: vi.fn(async () => ({
		users: 1,
		settings: 1,
		folders: 0,
		userGroups: 0,
		deviceGroups: 0,
		strategies: 0,
		backend: 'betterdesk',
		dbType: 'sqlite',
		components: { env: true, goPrivKey: true, goApiKey: true, uploads: 0 }
	})),
	getBackupSchedule: vi.fn(async () => ({
		schedule: { enabled: false, intervalHours: 24, retentionCount: 7, destinationType: 'local', destinationPath: '' },
		status: null
	}))
}));

import BackupTab from '../../../src/lib/settings/tabs/BackupTab.svelte';
import { session } from '../../../src/lib/state/session.svelte.ts';

const requests: string[] = [];

beforeEach(() => {
	session.isSuperAdmin = true;
	requests.length = 0;
	vi.stubGlobal(
		'fetch',
		vi.fn(async (input: RequestInfo | URL) => {
			requests.push(String(input));
			return new Response('{"success":false}', { status: 404, headers: { 'Content-Type': 'application/json' } });
		})
	);
});

afterEach(() => {
	cleanup();
	vi.unstubAllGlobals();
	session.isSuperAdmin = false;
});

describe('Backup tab', () => {
	test('has the schedule and its folder, and no network share login', async () => {
		render(BackupTab);
		expect(await screen.findByText('backup.schedule_title', { selector: 'h2' })).toBeTruthy();
		expect(document.getElementById('scheduled-backup-smb-panel')).toBeNull();
		expect(screen.queryByLabelText('backup.smb_host')).toBeNull();
		expect(requests.filter((url) => url.includes('/backup/smb'))).toEqual([]);
	});
});
