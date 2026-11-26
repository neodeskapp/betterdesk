import type { Page } from '@playwright/test';
import { test, expect, expectNoAxeViolations } from './fixtures.ts';
import { UI } from './roles.ts';

/**
 * Roles and permissions, the first-time admin's task "let helpdesk staff
 * connect to devices but not delete them": pick the role, turn on Connect
 * to devices, turn off Delete devices, save, read the toast, undo. Once
 * with the mouse and once with the keyboard alone. The role is Guest link
 * issuer, which no e2e account has; it starts with Delete devices allowed
 * by an override, and every test puts it back to its defaults.
 */

test.use({ role: 'super_admin' });
// The tests change the same role one after the other
test.describe.configure({ mode: 'serial' });

const ROLE = 'guest_issuer';

async function csrf(page: Page): Promise<string> {
	return page.evaluate(async () => {
		const r = await fetch('/api/console/bootstrap', { headers: { Accept: 'application/json' } });
		return (await r.json()).csrfToken as string;
	});
}

async function batch(page: Page, changes: { permission: string; granted: boolean | null }[]) {
	const token = await csrf(page);
	const r = await page.request.post('/api/panel/role-permissions/batch', {
		headers: { Accept: 'application/json', 'Content-Type': 'application/json', 'X-CSRF-Token': token },
		data: JSON.stringify({ role: ROLE, changes })
	});
	expect(r.status()).toBe(200);
}

async function has(page: Page): Promise<string[]> {
	const r = await page.request.get(`/api/panel/roles/${ROLE}/permissions`, {
		headers: { Accept: 'application/json' }
	});
	return ((await r.json()).data?.permissions ?? []) as string[];
}

const clear = [
	{ permission: 'device.connect', granted: null },
	{ permission: 'device.delete', granted: null }
];

test.beforeEach(async ({ page }) => {
	await page.goto(`${UI}/`);
	await batch(page, [
		{ permission: 'device.connect', granted: null },
		{ permission: 'device.delete', granted: true }
	]);
});

test.afterEach(async ({ page }) => {
	await batch(page, clear);
});

const toast = (page: Page) => page.locator('.toast-region[role="status"]');

async function expectSavedThenUndo(page: Page, undo: () => Promise<void>) {
	await expect(toast(page)).toContainText(
		'Saved: Connect to devices allowed, Delete devices default (not allowed).'
	);
	await expect.poll(() => has(page)).toContain('device.connect');
	expect(await has(page)).not.toContain('device.delete');
	await undo();
	await expect(toast(page)).toContainText('The changes are undone.');
	await expect.poll(() => has(page)).toContain('device.delete');
	expect(await has(page)).not.toContain('device.connect');
	await expect(page.locator('#perm-device-delete-control')).toBeChecked();
	await expect(page.locator('#perm-device-connect-control')).not.toBeChecked();
}

test('let helpdesk staff connect but not delete, with the mouse', async ({ page }) => {
	await page.goto(`${UI}/permissions`);
	const picker = page.getByRole('radiogroup', { name: 'Role to change' });
	await picker.getByRole('radio', { name: 'Guest link issuer' }).check();
	await expect(page).toHaveURL(/[?&]role=guest_issuer/);
	await expect(page.locator('.settings-summary')).toContainText(
		"People with this role can make guest links and see and change devices. They can't connect to devices"
	);

	const connect = page.getByRole('checkbox', { name: 'Connect to devices' });
	const del = page.getByRole('checkbox', { name: 'Delete devices' });
	await expect(del).toBeChecked();
	await connect.check();
	await del.uncheck();

	// Nothing saved yet: the bar names both changes, the rows say so, the summary follows
	const bar = page.getByRole('region', { name: 'Unsaved changes' });
	await expect(bar).toContainText('2 unsaved changes');
	await expect(bar).toContainText('Connect to devices: default (not allowed) → allowed');
	await expect(bar).toContainText('Delete devices: allowed → default (not allowed)');
	await expect(page.locator('#perm-device-connect')).toContainText('Changed, not saved');
	await expect(page.locator('.settings-summary')).toContainText('With your unsaved changes.');
	expect(await has(page)).toContain('device.delete');
	await expectNoAxeViolations(page);

	await bar.getByRole('button', { name: 'Save changes' }).click();
	await expect(bar).toHaveCount(0);
	await expectSavedThenUndo(page, () => toast(page).getByRole('button', { name: 'Undo' }).click());
});

interface Focused {
	id: string;
	name: string | null;
	text: string;
}

/** Press Tab until the focused element matches, at most `max` times */
async function tabTo(page: Page, match: (el: Focused) => boolean, max = 400) {
	for (let i = 0; i < max; i++) {
		await page.keyboard.press('Tab');
		const el = await page.evaluate((): Focused | null => {
			const a = document.activeElement;
			return a ? { id: a.id, name: a.getAttribute('name'), text: a.textContent?.trim() ?? '' } : null;
		});
		if (el && match(el)) return;
	}
	throw new Error('focus never got there');
}

test('let helpdesk staff connect but not delete, with the keyboard alone', async ({ page }) => {
	await page.goto(`${UI}/permissions`);
	await expect(page.getByRole('radiogroup', { name: 'Role to change' })).toBeVisible();
	await page.locator('body').focus();

	// Into the role picker, then the arrow keys to Guest link issuer
	await tabTo(page, (el) => el.name === 'perm-role');
	const issuer = page.getByRole('radio', { name: 'Guest link issuer' });
	for (let i = 0; i < 12 && !(await issuer.isChecked()); i++) await page.keyboard.press('ArrowDown');
	await expect(issuer).toBeChecked();
	await expect(page).toHaveURL(/[?&]role=guest_issuer/);
	await expect(issuer).toBeFocused();

	await tabTo(page, (el) => el.id === 'perm-device-connect-control');
	await page.keyboard.press('Space');
	await tabTo(page, (el) => el.id === 'perm-device-delete-control');
	await page.keyboard.press('Space');
	await expect(page.getByRole('region', { name: 'Unsaved changes' })).toContainText('2 unsaved changes');

	// Ctrl+S saves while the bar shows
	await page.keyboard.press('Control+s');
	// The toast is far after the rows: its Undo also runs with Ctrl+Z
	await expectSavedThenUndo(page, async () => {
		await expect(toast(page).getByRole('button', { name: 'Undo' })).toHaveAttribute(
			'aria-keyshortcuts',
			'Control+Z Meta+Z'
		);
		await page.keyboard.press('Control+z');
	});
});

test('the top bar search finds "delete devices" and opens its row', async ({ page }) => {
	await page.goto(`${UI}/devices`);
	await expect(page.locator('.search-trigger')).toBeVisible();
	await page.keyboard.press('Control+k');
	const box = page.getByRole('combobox');
	await expect(box).toBeFocused();
	await box.fill('delete devices');
	const option = page.getByRole('option', { name: /Delete devices/ }).first();
	await expect(option).toContainText('People, Roles and permissions');
	await option.click();
	await expect(page).toHaveURL(/\/permissions#perm-device-delete$/);
	await expect(page.locator('#perm-device-delete')).toBeInViewport();
});

test('which roles may manage API keys, and what Server admin can do with it', async ({ page }) => {
	await page.goto(`${UI}/permissions?role=server_admin`);
	const row = page.locator('#perm-server-keys');
	await expect(row).toContainText('This role can list and delete API keys but not create them');
	await row.getByText('Which roles have this?').click();
	await expect(row.locator('.perm-who li')).toContainText(['Super admin (always)', 'Server admin']);
});
