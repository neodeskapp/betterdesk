/**
 * Settings B: the system settings (Settings › Email and Time, the Server
 * section's Backups, Updates and Configuration files), /languages and the
 * "restart required" prompt.
 *
 * Per role: the tabs, the Server pages and the Translations page show their
 * controls exactly to the roles with server.config (the server checks the
 * same); everyone else gets one sentence on a tab and the no-access view on
 * a page, without asking the server for what it would refuse; axe finds nothing on any tab. Then the main tasks with their
 * conditional parts, without saving anything: the restore steps, the mail
 * sign-in fields, the update check, the file editor's discard question, a
 * language's texts, and the restart prompt with a mocked pending restart.
 *
 * The Time tab for server_admin needs the server change in
 * routes/commercialization.routes.js (clock reads with server.config) and a
 * console restart.
 */
import type { Page } from '@playwright/test';
import { test, expect, expectNoAxeViolations } from './fixtures.ts';
import { UI, type Role } from './roles.ts';

const ROLES: Role[] = ['admin', 'super_admin', 'server_admin', 'global_admin', 'operator', 'viewer'];
const CONFIG: Role[] = ['admin', 'super_admin', 'server_admin'];

/** The address and a heading only its content has; a page has no Settings tab */
const TABS = [
	{ path: '/server/backups', heading: 'Make a backup', page: true },
	{ path: '/server/updates', heading: 'Update channel', page: true },
	{ path: '/settings?tab=email', heading: 'Mail server', page: false },
	{ path: '/settings?tab=time', heading: 'Billing time zone', page: false },
	{ path: '/server/config-files', heading: 'Files on this server', page: true }
] as const;

/**
 * A full page load starts the whole console (session, strings, the tab's
 * code) before the page shows; with every worker's checks on one machine
 * that takes longer than the default 5 s.
 */
const START = { timeout: 20000 };

/** The tab's own requests have answered: no loading line is left. (Not
 *  networkidle: the shell's polls and a hover preload keep the network busy.) */
async function settled(page: Page): Promise<void> {
	await expect(page.locator('main [role="status"]', { hasText: /^Loading/ })).toHaveCount(0, START);
}

async function permissions(page: Page): Promise<string[]> {
	return page.evaluate(async () => {
		const r = await fetch('/api/console/bootstrap', {
			credentials: 'same-origin',
			headers: { Accept: 'application/json' }
		});
		return ((await r.json()).permissions ?? []) as string[];
	});
}

for (const role of ROLES) {
	test.describe(`system settings as ${role}`, () => {
		test.use({ role });

		test('tabs and Translations follow server.config', async ({ page }) => {
			// Seven full page loads and seven axe runs
			test.slow();
			const settingsCalls: string[] = [];
			page.on('request', (r) => {
				if (/\/api\/(settings\/(backup|updates|email|advanced)|panel\/languages)/.test(r.url()))
					settingsCalls.push(r.url());
			});
			await page.goto(`${UI}/server/backups`);
			await expect(page.getByRole('heading', { level: 1 })).toBeVisible(START);
			const perms = await permissions(page);
			const allowed = CONFIG.includes(role);
			expect(perms.includes('server.config') || role === 'admin' || role === 'super_admin').toBe(allowed);

			for (const { path, heading, page: own } of TABS) {
				await page.goto(`${UI}${path}`);
				await expect(page.getByRole('heading', { level: 1 })).toBeVisible(START);
				if (allowed) {
					await expect(page.getByRole('heading', { name: heading, exact: true, level: 2 })).toBeVisible(
						START
					);
				} else if (own) {
					await expect(page.getByRole('heading', { name: "You don't have access to this page" })).toBeVisible(
						START
					);
					await expect(page.getByRole('heading', { name: heading, exact: true, level: 2 })).toHaveCount(0);
				} else {
					await expect(
						page.getByText('Only super admins and server admins can see and change these settings.')
					).toBeVisible();
					await expect(page.getByRole('heading', { name: heading, exact: true, level: 2 })).toHaveCount(0);
				}
				await settled(page);
				await expectNoAxeViolations(page);
			}

			await page.goto(`${UI}/languages`);
			if (allowed) {
				await expect(page.locator('.languages-table tbody tr[data-key]').first()).toBeVisible(START);
			} else {
				await expect(page.getByRole('heading', { name: "You don't have access to this page" })).toBeVisible(
					START
				);
			}
			await expectNoAxeViolations(page);
			if (!allowed) expect(settingsCalls, 'no request the server would refuse').toEqual([]);
		});
	});
}

test.describe('system settings tasks', () => {
	test.use({ role: 'admin' });

	test('restore: a file, then the parts, then Restore asks first', async ({ page }) => {
		await page.goto(`${UI}/server/backups`);
		const restore = page.locator('#backup-restore');
		await expect(
			restore.getByText('Choose a backup file. Then you pick what to restore from it.')
		).toBeVisible(START);
		await expect(page.locator('#restore-start-btn')).toHaveCount(0);
		// Three steps; nothing red before anything dangerous is chosen
		await expect(restore.locator('.restore-step')).toHaveCount(3);
		await expect(restore.locator('.restore-step').first()).toHaveAttribute('aria-current', 'step');
		await expect(restore.locator('.btn-danger, .btn-danger-quiet')).toHaveCount(0);

		await page.locator('#restore-file-input').setInputFiles({
			name: 'neodesk-backup.tar.gz',
			mimeType: 'application/gzip',
			buffer: Buffer.from([0x1f, 0x8b, 0, 0])
		});
		await expect(page.locator('#restore-options')).toBeVisible();
		await expect(page.locator('#restore-start-btn')).toBeDisabled();
		await page.getByLabel('Uploaded images').check();
		await expect(page.locator('#restore-start-btn')).toBeEnabled();
		await page.locator('#restore-start-btn').click();
		const dialog = page.getByRole('dialog');
		await expect(dialog).toContainText('neodesk-backup.tar.gz');
		await expect(dialog).toContainText('Uploaded images');
		// The confirmation is the red step and says what is lost
		await expect(dialog).toContainText('is lost');
		await expect(dialog.getByRole('button', { name: 'Restore', exact: true })).toHaveClass(/btn-danger/);
		await expectNoAxeViolations(page);
		await page.keyboard.press('Escape');
		await expect(dialog).toBeHidden();
		await expect(page.locator('#restore-start-btn')).toBeFocused();

		// An old settings file restores without parts
		await page.locator('#restore-file-input').setInputFiles({
			name: 'settings.json',
			mimeType: 'application/json',
			buffer: Buffer.from('{}')
		});
		await expect(page.locator('#restore-options')).toBeHidden();
		await expect(page.locator('#restore-start-btn')).toBeEnabled();
	});

	test('schedule details show when they apply; no network share login', async ({ page }) => {
		// The switch is there once the saved schedule is, so a first click is not undone by the load
		await page.goto(`${UI}/server/backups`);
		const enabled = page.getByRole('checkbox', { name: 'Make backups on a schedule' });
		await expect(enabled).toBeVisible(START);
		const on = await enabled.isChecked();
		await expect(page.locator('#scheduled-backup-details')).toBeVisible({ visible: on });
		await enabled.click();
		await expect(enabled).toBeChecked({ checked: !on });
		await expect(page.locator('#scheduled-backup-details')).toBeVisible({ visible: !on });
		await expect(page.getByText('1 unsaved change', { exact: true })).toBeVisible();
		if (!on) {
			// A folder destination asks for its path
			await page.getByRole('combobox', { name: 'Where to save' }).selectOption('path');
			await expect(page.locator('#scheduled-backup-path')).toBeVisible();
			// An hourly schedule has no time of day
			await page.getByRole('combobox', { name: 'How often' }).selectOption({ label: 'Every 6 hours' });
			await expect(page.locator('#scheduled-backup-time')).toBeHidden();
		}
		await page.getByRole('button', { name: 'Discard' }).click();
		await expect(enabled).toBeChecked({ checked: on });
		// A155: backups go to a folder path; the network share login is gone
		await expect(page.getByRole('heading', { name: 'Network share login (SMB)' })).toHaveCount(0);
		await expect(page.locator('#backup-smb-host')).toHaveCount(0);
		await expectNoAxeViolations(page);
	});

	test('email: user name and password only with a sign-in; TLS moves the port', async ({ page }) => {
		// The form shows the saved mail server once it has loaded
		const loaded = page.waitForResponse((r) => r.url().endsWith('/api/settings/email/smtp'), START);
		await page.goto(`${UI}/settings?tab=email`);
		await loaded;
		const auth = page.locator('#email-smtp-auth');
		await expect(auth).toBeVisible(START);
		await settled(page);
		if (await auth.isChecked()) await auth.click();
		await expect(page.locator('#email-smtp-user')).toBeHidden();
		await auth.click();
		await expect(page.locator('#email-smtp-user')).toBeVisible();
		const port = page.locator('#email-smtp-port');
		await port.fill('587');
		const tls = page.locator('#email-smtp-secure');
		if (await tls.isChecked()) await tls.click();
		await tls.click();
		await expect(port).toHaveValue('465');
		await tls.click();
		await expect(port).toHaveValue('587');
	});

	test('updates: a check says what it found', async ({ page }) => {
		await page.goto(`${UI}/server/updates`);
		await expect(page.locator('#update-remote-version')).toContainText('Not checked yet', START);
		await page.locator('#update-check-btn').click();
		await expect(page.locator('#update-status-badge')).toBeVisible({ timeout: 30000 });
		await expect(page.locator('#update-remote-version')).not.toContainText('Not checked yet');
		// The backups made before updates are with the other backups now
		await expect(page.locator('#backup-retention-input')).toHaveCount(0);
		await expectNoAxeViolations(page);
	});

	test('backups: both retention counts on one page; old addresses lead here', async ({ page }) => {
		await page.goto(`${UI}/settings?tab=backup#scheduled-backup-retention`);
		await expect(page).toHaveURL(/\/server\/backups#scheduled-backup-retention$/, START);
		await expect(page.getByRole('heading', { level: 1, name: 'Backups' })).toBeVisible(START);
		await expect(page.getByRole('heading', { name: 'Before updates' })).toBeVisible(START);
		// "Keep all" is a switch with the number beside it, not 0
		const keep = page.getByRole('group', { name: 'Keep update backups' });
		await expect(keep).toBeVisible(START);
		await expect(keep.getByRole('checkbox', { name: 'Keep all update backups' })).toBeVisible();
		// The update backups' address lands on them
		await page.goto(`${UI}/settings?tab=backup#update-backups-panel`);
		await expect(page).toHaveURL(/\/server\/backups#update-backups-panel$/, START);
		await expect(page.getByRole('heading', { name: 'Before updates' })).toBeInViewport(START);
		// Deleting older update backups is apart, at the end
		await expect(page.getByRole('heading', { name: 'Danger zone' })).toBeVisible();
		await expect(page.getByRole('button', { name: /Save number|Delete older backups now/ })).toHaveCount(0);
		await page.goto(`${UI}/settings?tab=updates`);
		await expect(page).toHaveURL(/\/server\/updates$/, START);
		await page.goto(`${UI}/settings?tab=advanced`);
		await expect(page).toHaveURL(/\/server\/config-files$/, START);
		await expect(page.getByRole('heading', { level: 1, name: 'Configuration files' })).toBeVisible(START);
		await expectNoAxeViolations(page);
	});

	test('advanced: a file opens in the editor; switching with changes asks first', async ({ page }) => {
		await page.goto(`${UI}/server/config-files`);
		const files = page.locator('.split-list .list-row');
		await expect(files.first()).toBeVisible(START);
		await files.first().click();
		const editor = page.locator('#advanced-config-textarea');
		await expect(editor).toBeVisible();
		if (await editor.isEnabled()) {
			await editor.press('End');
			await editor.pressSequentially(' ');
			await expect(page.locator('#advanced-config-save')).toBeEnabled();
			await files.nth(1).click();
			const ask = page.getByRole('dialog');
			await expect(ask).toContainText('You have unsaved changes. Discard them?');
			await ask.getByRole('button', { name: 'Cancel' }).click();
			await expect(editor).toBeVisible();
		}
		await expectNoAxeViolations(page);
	});

	test('translations: #<code> opens a language; Missing and the search narrow it; Back returns', async ({
		page
	}) => {
		await page.goto(`${UI}/languages`);
		const de = page.locator('.languages-table tbody tr[data-key="de"]');
		await expect(de).toBeVisible(START);
		await de.click();
		await expect(page).toHaveURL(/#de$/);
		await expect(page.getByRole('heading', { level: 2, name: /\(de\)$/ })).toBeFocused();
		await expect(page.locator('.texts-table tbody tr').first()).toBeVisible({ timeout: 20000 });
		await page.getByRole('searchbox').last().fill('settings.tab_backup');
		await expect(page.locator('.texts-table tbody tr')).toHaveCount(1);
		await page.getByRole('button', { name: 'Missing', exact: true }).click();
		await expectNoAxeViolations(page);
		await page.goBack();
		await expect(page.locator('.languages-table')).toBeVisible();
	});
});

test.describe('restart prompt', () => {
	test.use({ role: 'admin' });

	test('a waiting restart comes back on another page; Cancel and restore answers it', async ({ page }) => {
		let canceled = false;
		await page.route('**/api/settings/restart/pending', (r) =>
			r.fulfill({
				json: {
					success: true,
					data: canceled
						? null
						: {
								id: 'e2e-restart',
								phase: 'pending',
								changes: [{ key: 'billing-clock', label: 'Server time (NTP)' }]
							}
				}
			})
		);
		await page.route('**/api/settings/restart/cancel', (r) => {
			canceled = true;
			return r.fulfill({ json: { success: true, data: { canceled: true, id: 'e2e-restart' } } });
		});
		await page.goto(`${UI}/devices`);
		const dialog = page.getByRole('dialog', { name: 'Restart needed' });
		await expect(dialog).toBeVisible(START);
		await expect(dialog).toContainText('Server time (NTP)');
		// Waiting for an answer: Escape does not close it
		await page.keyboard.press('Escape');
		await expect(dialog).toBeVisible();
		await expectNoAxeViolations(page);
		await dialog.getByRole('button', { name: 'Cancel and restore' }).click();
		await expect(dialog).toBeHidden();
	});
});
