/**
 * Server management (/server-management?tab=...), once per role: the three
 * views open for roles with server.config and say "no permission" (without
 * asking the server for anything) to the others; the file browser opens a
 * folder and shows a file read-only, with no action that changes one;
 * services and overview render; the old terminal address is not found
 * (A194); axe finds nothing in each view or the file sheet.
 */
import type { Page } from '@playwright/test';
import { test, expect, expectNoAxeViolations } from './fixtures.ts';
import { UI, type Role } from './roles.ts';

const ROLES: Role[] = ['admin', 'super_admin', 'server_admin', 'global_admin', 'operator', 'viewer'];

async function allowed(page: Page, permission: string): Promise<boolean> {
	return page.evaluate(async (p) => {
		const r = await fetch('/api/console/bootstrap', { headers: { Accept: 'application/json' } });
		const boot = (await r.json()) as { permissions: string[]; isSuperAdmin: boolean };
		return boot.isSuperAdmin || boot.permissions.includes(p);
	}, permission);
}

for (const role of ROLES) {
	test.describe(`server management as ${role}`, () => {
		test.use({ role });

		test('each view follows server.config', async ({ page }) => {
			const calls: string[] = [];
			page.on('request', (r) => {
				if (r.url().includes('/api/server-management/')) calls.push(r.url());
			});
			await page.goto(`${UI}/server-management?tab=overview`);
			await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
			const may = await allowed(page, 'server.config');
			if (role === 'operator' || role === 'viewer' || role === 'global_admin') expect(may).toBe(false);
			if (!may) {
				await expect(page.locator('.no-access')).toBeVisible();
				expect(calls).toEqual([]);
				await expectNoAxeViolations(page);
				return;
			}
			// Overview: the servers and the machine
			await expect(page.locator('.sm-hbbs')).toContainText(/\S/);
			await expect(page.locator('.facts').first()).toBeVisible({ timeout: 15_000 });
			await expectNoAxeViolations(page);

			// Files and Services are tabs of Server status: one page, one heading
			await expect(page.getByRole('heading', { level: 1, name: 'Server status' })).toBeVisible();
			await page.getByRole('tab', { name: 'Files' }).click();
			await expect(page).toHaveURL(/\?tab=files$/);
			await expect(page.getByRole('heading', { level: 1, name: 'Server status' })).toBeVisible();

			// Files: a folder, and a file in a read-only sheet. A194: no row menu, nothing that writes
			await page.goto(`${UI}/server-management?tab=files`);
			await expect(page.getByRole('tab', { name: 'Files', selected: true })).toBeVisible();
			const rows = page.locator('.files-table tbody tr[data-key]');
			await expect(rows.first()).toBeVisible({ timeout: 15_000 });
			await expect(rows.first().getByRole('button', { name: /^Actions for/ })).toHaveCount(0);
			await expectNoAxeViolations(page);
			const file = page.locator('.files-table tbody tr.type-file').first();
			if (await file.count()) {
				await file.locator('.row-open').click();
				const sheet = page.getByRole('dialog');
				await expect(sheet).toBeVisible();
				await expect(sheet.locator('textarea')).toHaveAttribute('readonly', '');
				await expect(sheet.getByRole('button', { name: /^Save/ })).toHaveCount(0);
				await expectNoAxeViolations(page);
				await sheet.getByRole('button', { name: 'Close', exact: true }).first().click();
				await expect(sheet).toBeHidden();
			}

			// Services: a list or the one sentence for a machine without services
			await page.goto(`${UI}/server-management?tab=services`);
			await expect(page.locator('.services-refresh')).toBeVisible();
			await expect(page.locator('main')).toContainText(/service/i);
			await expectNoAxeViolations(page);

			// A194: no Terminal; its old address is not found
			await expect(page.getByRole('tab', { name: 'Terminal' })).toHaveCount(0);
			await page.goto(`${UI}/server-management?tab=terminal`);
			await expect(page.getByRole('heading', { level: 1, name: 'Page not found' })).toBeVisible();
			await expect(page.locator('.terminal-status')).toHaveCount(0);
		});
	});
}
