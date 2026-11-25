/**
 * The device list and the device panel, once per role: the row menu and the
 * panel offer exactly what the role's permissions allow, search and ?open=
 * work, and axe finds nothing in the page, the open menu or the panel.
 */
import type { Page } from '@playwright/test';
import { test, expect, expectNoAxeViolations } from './fixtures.ts';
import { UI, type Role } from './roles.ts';

const ROLES: Role[] = ['admin', 'super_admin', 'server_admin', 'global_admin', 'operator', 'viewer'];

interface ApiDevice {
	id: string;
	soft_deleted?: boolean;
	remote_target?: unknown;
}

/** The signed-in person's permissions and a plain device (not deleted, not an RDP/VNC target) they can see */
async function context(page: Page): Promise<{ permissions: string[]; deviceId: string }> {
	return page.evaluate(async () => {
		const get = async (url: string) =>
			(await fetch(url, { credentials: 'same-origin', headers: { Accept: 'application/json' } })).json();
		const boot = await get('/api/console/bootstrap');
		const list = await get('/api/devices');
		const devices = (list.data?.devices ?? []) as ApiDevice[];
		const plain = devices.find((d) => !d.soft_deleted && !d.remote_target);
		return { permissions: boot.permissions as string[], deviceId: plain?.id ?? '' };
	});
}

const rows = (page: Page) => page.locator('.devices-table tbody tr:has(.device-name)');

for (const role of ROLES) {
	test.describe(`devices as ${role}`, () => {
		test.use({ role });

		test('the row menu and the panel follow the permissions', async ({ page }) => {
			await page.goto(`${UI}/devices`);
			await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
			await expect(rows(page).first()).toBeVisible();
			const { permissions, deviceId } = await context(page);
			expect(deviceId, 'the demo data has a device this role can see').not.toBe('');
			if (role === 'viewer') {
				expect(permissions).not.toContain('device.delete');
				expect(permissions).not.toContain('device.ban');
				expect(permissions).not.toContain('device.edit');
			}
			await expectNoAxeViolations(page);

			// Search narrows the list to that device
			await page.getByRole('searchbox').first().fill(deviceId);
			const row = rows(page).filter({ hasText: deviceId });
			await expect(row).toHaveCount(1);
			// The count line says the filter is applied (no re-render under the next click)
			await expect(page.locator('.list-head[role="status"]')).toContainText('1');

			// Row menu: exactly the actions the permissions allow
			await row.getByRole('button', { name: /^Actions:/ }).click();
			const menu = page.getByRole('menu');
			await expect(menu).toBeVisible();
			const offers = async (id: string) => (await menu.locator(`.act-${id}`).count()) > 0;
			expect(await offers('details')).toBe(true);
			expect(await offers('toggle-ban')).toBe(permissions.includes('device.ban'));
			expect(await offers('delete')).toBe(permissions.includes('device.delete'));
			expect(await offers('edit')).toBe(permissions.includes('device.edit'));
			expect(await offers('change-id')).toBe(permissions.includes('device.change_id'));
			expect(await offers('web-remote')).toBe(permissions.includes('device.connect'));
			expect(await offers('signin')).toBe(permissions.includes('device.signin_manage'));
			await expectNoAxeViolations(page);
			await page.keyboard.press('Escape');
			await expect(menu).toBeHidden();

			// The panel: opened by a click on the row, in the URL, closed by Escape
			await row.locator('.device-name').click();
			const panel = page.getByRole('dialog');
			await expect(panel).toBeVisible();
			await expect(page).toHaveURL(new RegExp(`[?&]open=${deviceId}`));
			await expect(panel.getByRole('tablist')).toBeVisible();
			await expect(panel.getByRole('button', { name: 'Edit', exact: true })).toHaveCount(
				permissions.includes('device.edit') ? 1 : 0
			);
			await expectNoAxeViolations(page);
			await page.keyboard.press('Escape');
			await expect(panel).toBeHidden();
			await expect(page).not.toHaveURL(/[?&]open=/);
		});
	});
}

test.describe('saved sign-in', () => {
	test.use({ role: 'super_admin' });

	test('set from the panel, the password never shown again, then cleared (L469)', async ({ page }) => {
		await page.goto(`${UI}/devices`);
		const { deviceId } = await context(page);
		await page.goto(`${UI}/devices?open=${deviceId}`);
		const panel = page.getByRole('dialog');
		await expect(panel.getByRole('tablist')).toBeVisible();
		await panel.getByRole('tab', { name: 'Actions' }).click();
		await panel.locator('[data-action-key="signin"]').click();
		const sheet = page.getByRole('dialog', { name: `Saved sign-in for ${deviceId}` });
		await expect(sheet).toBeVisible();
		await expect(sheet.getByText('Not saved')).toBeVisible();
		// The switch is off until someone turns it on
		const auto = sheet.getByLabel('Sign in automatically when a session opens on the lock screen');
		await expect(auto).not.toBeChecked();
		await expectNoAxeViolations(page);

		await sheet.getByLabel('Account name', { exact: true }).fill(String.raw`LAB\e2e-admin`);
		await sheet.getByLabel('Password', { exact: true }).fill('E2e-Made-Up-Pass-1');
		await sheet.getByRole('button', { name: 'Save' }).click();
		await expect(sheet).toBeHidden();

		// Opened again: the account and who saved it, the password field empty and nothing that holds it
		await panel.locator('[data-action-key="signin"]').click();
		await expect(sheet.getByText('Saved', { exact: true })).toBeVisible();
		await expect(sheet.getByLabel('Account name', { exact: true })).toHaveValue(String.raw`LAB\e2e-admin`);
		await expect(sheet.getByLabel('New password')).toHaveValue('');
		expect(await sheet.innerHTML()).not.toContain('E2e-Made-Up-Pass-1');
		const status = await page.evaluate(async (id) => {
			const r = await fetch(`/api/devices/${encodeURIComponent(id)}/signin`, {
				credentials: 'same-origin',
				headers: { Accept: 'application/json' }
			});
			return r.text();
		}, deviceId);
		expect(status).toContain('"set":true');
		expect(status).not.toContain('E2e-Made-Up-Pass-1');

		await sheet.getByRole('button', { name: 'Clear saved sign-in' }).click();
		await page.getByRole('dialog').last().getByRole('button', { name: 'Clear saved sign-in' }).click();
		await expect(sheet).toBeHidden();
		await panel.locator('[data-action-key="signin"]').click();
		await expect(sheet.getByText('Not saved')).toBeVisible();
		await page.keyboard.press('Escape');
	});
});

test.describe('devices links', () => {
	test.use({ role: 'operator' });

	test('Open in NeoDesk and Open in RustDesk hand the ID to each app by its own scheme (L423)', async ({
		page
	}) => {
		// The browser has neither app: keep what the console hands to window.open
		await page.addInitScript(() => {
			const w = window as unknown as { __opened: string[] };
			w.__opened = [];
			window.open = ((url?: string | URL) => {
				w.__opened.push(String(url));
				return null;
			}) as typeof window.open;
		});
		await page.goto(`${UI}/devices`);
		const { deviceId } = await context(page);
		await page.getByRole('searchbox').first().fill(deviceId);
		const row = rows(page).filter({ hasText: deviceId });
		await expect(row).toHaveCount(1);
		for (const name of ['Open in NeoDesk', 'Open in RustDesk']) {
			await row.getByRole('button', { name: /^Actions:/ }).click();
			await page.getByRole('menu').getByRole('menuitem', { name }).click();
		}
		const opened = await page.evaluate(() => (window as unknown as { __opened: string[] }).__opened);
		const id = encodeURIComponent(deviceId);
		expect(opened).toEqual([`neodesk://${id}`, `rustdesk://${id}`]);
	});

	test('a link with ?open= opens the device; Back closes it', async ({ page }) => {
		await page.goto(`${UI}/devices`);
		const { deviceId } = await context(page);
		await page.goto(`${UI}/devices?open=${deviceId}`);
		const panel = page.getByRole('dialog');
		await expect(panel).toBeVisible();
		await expect(panel).toContainText(deviceId);
		// Let the panel's fonts and styles finish loading: leaving cuts them off, which Firefox reports
		await page.waitForLoadState('networkidle');
		await page.goto(`${UI}/devices`);
		// Search first: in a long list the row may be far down
		await page.getByRole('searchbox').first().fill(deviceId);
		await rows(page).filter({ hasText: deviceId }).locator('.device-name').click();
		await expect(panel).toBeVisible();
		await page.goBack();
		await expect(panel).toBeHidden();
		await expect(page).toHaveURL(new RegExp(`${UI}/devices$`));
	});
});

test.describe('device usage (L480)', () => {
	test.use({ role: 'operator' });

	test('the Metrics tab draws the usage history; a client that sends none says so', async ({ page }) => {
		await page.goto(`${UI}/devices`);
		const { withHistory, withoutUsage } = await page.evaluate(async () => {
			const get = async (url: string) =>
				(await fetch(url, { credentials: 'same-origin', headers: { Accept: 'application/json' } })).json();
			const list = ((await get('/api/devices')).data?.devices ?? []) as {
				id: string;
				soft_deleted?: boolean;
				remote_target?: unknown;
				client_type?: string;
			}[];
			let withHistory = '';
			let withoutUsage = '';
			for (const d of list.filter((x) => !x.soft_deleted && !x.remote_target)) {
				const detail = (await get(`/api/devices/${encodeURIComponent(d.id)}`)).data ?? {};
				const history = Array.isArray(detail.metrics_history) ? detail.metrics_history : [];
				if (!withHistory && history.length >= 2) withHistory = d.id;
				const neodesk = /neodesk/i.test(String(detail.client_type ?? ''));
				if (
					!withoutUsage &&
					!neodesk &&
					!history.length &&
					!detail.metrics &&
					!detail.telemetry?.snapshots?.metrics
				)
					withoutUsage = d.id;
				if (withHistory && withoutUsage) break;
			}
			return { withHistory, withoutUsage };
		});
		expect(withHistory, 'a seeded device with a usage history').not.toBe('');
		expect(withoutUsage, 'a seeded device whose client sends no usage').not.toBe('');

		await page.goto(`${UI}/devices?open=${withHistory}`);
		const panel = page.getByRole('dialog');
		await panel.getByRole('tab', { name: 'Metrics' }).click();
		await expect(panel.getByRole('group', { name: /CPU, memory and disk/ })).toBeVisible({ timeout: 20_000 });

		await page.goto(`${UI}/devices?open=${withoutUsage}`);
		await expect(
			panel.getByText("This device's client doesn't send usage data", { exact: false })
		).toBeVisible({ timeout: 20_000 });
	});
});
