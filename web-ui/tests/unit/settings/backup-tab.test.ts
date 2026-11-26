/**
 * Server › Backups as one settings page: backups go to a folder (the
 * network share login is gone, L122); the schedule and "Keep update
 * backups" are saved by one bar; "keep all" is a switch, not 0; the
 * switch is not there before the saved schedule is, so a first click
 * cannot be undone by the load; the restore is three steps.
 */
import { cleanup, fireEvent, render, screen, within } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

vi.mock('$app/state', () => ({ page: { url: new URL('http://localhost/server/backups'), state: {} } }));
vi.mock('$app/paths', () => ({ resolve: (p: string) => p || '/', asset: (p: string) => '/' + p }));
vi.mock('$app/navigation', () => ({ goto: vi.fn(), beforeNavigate: vi.fn(), replaceState: vi.fn() }));

const schedule = vi.hoisted(() => ({
	resolve: null as null | (() => void)
}));

vi.mock('#lib/api/settings-system.ts', async (orig) => ({
	...(await orig<typeof import('../../../src/lib/api/settings-system.ts')>()),
	getBackupStats: vi.fn(async () => ({
		users: 1,
		settings: 1,
		folders: 0,
		userGroups: 0,
		deviceGroups: 0,
		strategies: 0,
		backend: 'neodesk',
		dbType: 'sqlite',
		components: { env: false, goPrivKey: true, goApiKey: true, uploads: 0 },
		parts: [
			{ id: 'database', included: true, files: 1, secret: true },
			{ id: 'env', included: false, reason: 'docker', files: 0, secret: true }
		]
	})),
	getBackupSchedule: vi.fn(async () => {
		if (schedule.resolve === null) {
			await new Promise<void>((r) => (schedule.resolve = r));
		}
		return {
			schedule: {
				enabled: false,
				intervalHours: 24,
				retentionCount: 7,
				destinationType: 'local',
				destinationPath: '',
				timeOfDay: ''
			},
			status: null,
			next: { at: null, timeZone: 'UTC' }
		};
	}),
	saveBackupSchedule: vi.fn(async () => ({})),
	getBackupRetention: vi.fn(async () => ({ count: 0, source: 'unset' })),
	setBackupRetention: vi.fn(async () => ({})),
	listUpdateBackups: vi.fn(async () => [])
}));

import * as api from '../../../src/lib/api/settings-system.ts';
import BackupTab from '../../../src/lib/settings/tabs/BackupTab.svelte';
import { session } from '../../../src/lib/state/session.svelte.ts';

const requests: string[] = [];

beforeEach(() => {
	session.isSuperAdmin = true;
	session.permissions = ['server.config'];
	schedule.resolve = () => {};
	requests.length = 0;
	vi.mocked(api.saveBackupSchedule).mockClear();
	vi.mocked(api.setBackupRetention).mockClear();
	vi.stubGlobal(
		'fetch',
		vi.fn(async (input: RequestInfo | URL) => {
			requests.push(String(input));
			return new Response('{"success":false}', {
				status: 404,
				headers: { 'Content-Type': 'application/json' }
			});
		})
	);
});

afterEach(() => {
	cleanup();
	vi.unstubAllGlobals();
	session.isSuperAdmin = false;
	session.permissions = [];
});

async function ready() {
	return screen.findByRole('checkbox', { name: 'Make backups on a schedule' });
}

describe('Backups page', () => {
	test('has the schedule and its folder, and no network share login', async () => {
		render(BackupTab);
		expect(await screen.findByRole('heading', { level: 2, name: 'Scheduled backups' })).toBeTruthy();
		expect(document.getElementById('scheduled-backup-smb-panel')).toBeNull();
		expect(screen.queryByLabelText('backup.smb_host')).toBeNull();
		expect(requests.filter((url) => url.includes('/backup/smb'))).toEqual([]);
	});

	test('a server admin with server.config sees why the full backup actions are missing', async () => {
		// The server lets only super admins download, schedule or restore full backups (L387)
		session.isSuperAdmin = false;
		render(BackupTab);
		expect(await screen.findByText('settings.backup_tab.super_admin_only')).toBeTruthy();
		expect(document.getElementById('backup-download-btn')).toBeNull();
		expect(document.getElementById('scheduled-backup-panel')).toBeNull();
		expect(document.getElementById('backup-restore')).toBeNull();
		// "Keep update backups" is theirs to set
		expect(await screen.findByRole('group', { name: 'settings.updates_tab.retention_label' })).toBeTruthy();
	});

	test('the schedule switch appears only with the saved schedule, so a first click sticks', async () => {
		schedule.resolve = null;
		render(BackupTab);
		await screen.findByRole('heading', { level: 2, name: 'Make a backup' });
		expect(screen.queryByRole('checkbox', { name: 'Make backups on a schedule' })).toBeNull();
		await vi.waitFor(() => expect(schedule.resolve).not.toBeNull());
		(schedule.resolve as unknown as () => void)();
		const enable = await ready();
		await userEvent.click(enable);
		expect(enable).toBeChecked();
		expect(screen.getByText('1 unsaved change')).toBeTruthy();
	});

	test('every night at 02:00, keep 14, and keep 10 update backups: one Save for all of it', async () => {
		const user = userEvent.setup();
		render(BackupTab);
		await user.click(await ready());
		await user.selectOptions(screen.getByRole('combobox', { name: 'How often' }), 'Every day');
		await user.selectOptions(screen.getByRole('combobox', { name: 'Time of day' }), '02:00');
		const keep = screen.getByRole('spinbutton', { name: 'backup.schedule_retention' });
		await user.clear(keep);
		await user.type(keep, '14');
		// Update backups: "Keep all" is a switch, the number appears when it is off
		const updates = screen.getByRole('group', { name: 'settings.updates_tab.retention_label' });
		const all = within(updates).getByRole('checkbox', { name: 'Keep all update backups' });
		expect(all).toBeChecked();
		await user.click(all);
		const count = within(updates).getByRole('spinbutton', { name: 'Keep this many' });
		await user.clear(count);
		await user.type(count, '10');

		// One bar, one Save; no per-group save buttons
		expect(screen.queryByRole('button', { name: /Save schedule|Save number/ })).toBeNull();
		expect(screen.getByText('4 unsaved changes')).toBeTruthy();
		expect(screen.getByText('When you save, the next backup runs on the new schedule.')).toBeTruthy();
		await user.click(screen.getByRole('button', { name: 'Save changes' }));

		await vi.waitFor(() =>
			expect(api.saveBackupSchedule).toHaveBeenCalledWith({
				enabled: true,
				intervalHours: 24,
				timeOfDay: '02:00',
				retentionCount: 14,
				destinationType: 'local',
				destinationPath: ''
			})
		);
		expect(api.setBackupRetention).toHaveBeenCalledWith(10);
		await vi.waitFor(() => expect(screen.queryByText(/^\d+ unsaved changes?$/)).toBeNull());
	});

	test('"Keep all update backups" saves the 0 the server stores for all', async () => {
		vi.mocked(api.getBackupRetention).mockResolvedValueOnce({ count: 5, source: 'database' });
		const user = userEvent.setup();
		render(BackupTab);
		await ready();
		const all = await screen.findByRole('checkbox', { name: 'Keep all update backups' });
		expect(all).not.toBeChecked();
		await user.click(all);
		await user.click(screen.getByRole('button', { name: 'Save changes' }));
		await vi.waitFor(() => expect(api.setBackupRetention).toHaveBeenCalledWith(0));
		// Only what changed is saved
		expect(api.saveBackupSchedule).not.toHaveBeenCalled();
	});

	test('a refused number keeps the changes and says why', async () => {
		const user = userEvent.setup();
		render(BackupTab);
		await user.click(await ready());
		const keep = screen.getByRole('spinbutton', { name: 'backup.schedule_retention' });
		await user.clear(keep);
		await user.type(keep, '0');
		expect(keep).toHaveAttribute('aria-invalid', 'true');
		await user.click(screen.getByRole('button', { name: 'Save changes' }));
		expect(api.saveBackupSchedule).not.toHaveBeenCalled();
		expect(screen.getAllByText('Enter a whole number from 1 to 365.').length).toBeGreaterThan(0);
		// On and the refused number stay unsaved
		expect(screen.getByText('2 unsaved changes')).toBeTruthy();
	});

	test('restore: a file, then what to restore, then a red confirmation that names it', async () => {
		const user = userEvent.setup();
		render(BackupTab);
		await ready();
		const steps = screen.getAllByRole('listitem').filter((li) => li.classList.contains('restore-step'));
		expect(steps).toHaveLength(3);
		expect(steps[0]).toHaveAttribute('aria-current', 'step');
		// Nothing red before anything dangerous is chosen
		expect(
			document.querySelector('#backup-restore .btn-danger, #backup-restore .btn-danger-quiet')
		).toBeNull();

		// The page binds the input's files both ways: give the input a list it may also set
		const input = screen.getByLabelText('settings.backup_tab.restore_file') as HTMLInputElement;
		const archive = new File([new Uint8Array([0x1f, 0x8b])], 'neodesk-backup.tar.gz', {
			type: 'application/gzip'
		});
		Object.defineProperty(input, 'files', {
			value: { 0: archive, length: 1, item: () => archive },
			writable: true,
			configurable: true
		});
		await fireEvent.change(input);
		expect(steps[1]).toHaveAttribute('aria-current', 'step');
		const start = screen.getByRole('button', { name: 'Review and restore…' });
		expect(start).toBeDisabled();
		await user.click(screen.getByRole('checkbox', { name: /settings.backup_tab.opt_uploads/ }));
		await user.click(start);
		const dialog = await screen.findByRole('dialog');
		expect(dialog).toHaveTextContent('settings.backup_tab.opt_uploads');
		expect(dialog).toHaveTextContent(/is lost/);
		expect(within(dialog).getByRole('button', { name: 'settings.backup_tab.confirm_button' })).toHaveClass(
			'btn-danger'
		);
		expect(steps[2]).toHaveAttribute('aria-current', 'step');
	});
});
