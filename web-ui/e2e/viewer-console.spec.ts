/**
 * The remote viewer inside the console, with the dev mock device, in
 * Chromium and Firefox: a deep link and the device panel's Connect open
 * the session in the console's main area, sessions stay connected while
 * the person moves to other pages (the same screen comes back, no new
 * connection), several sessions are tabs (a middle click closes one), the
 * sidebar folds away, the console's shortcuts leave the remote screen
 * alone and F8 gives the keyboard back, Open in new tab moves a session
 * to the standalone viewer, and ?standalone=1 keeps that layout.
 *
 * Needs the console with ND_DEV_MOCK_REMOTE=1; skipped without it.
 */
import type { Page } from '@playwright/test';
import { test, expect, expectNoAxeViolations } from './fixtures.ts';
import { UI } from './roles.ts';

const A = '900100012';
const B = '900100013';

async function mockOn(page: Page): Promise<boolean> {
	const r = await page.request.get(`/api/remote/viewer/${A}?mock=1`);
	return r.ok() && !!(await r.json()).mockRemote;
}

/** A second session from New connection: the console stays loaded, the first one connected */
async function addSession(page: Page, id: string): Promise<void> {
	await page.getByRole('button', { name: 'New connection' }).click();
	const dialog = page.getByRole('dialog');
	await dialog.getByLabel('Device ID', { exact: true }).fill(id);
	await dialog.getByRole('button', { name: 'Connect', exact: true }).click();
	await expect(page).toHaveURL(new RegExp(`/remote/session/${id}`));
	await expect(page.locator(`#rd-panel-${id}.streaming`)).toBeVisible({ timeout: 60_000 });
}

async function openSession(page: Page, id: string): Promise<void> {
	await page.goto(`${UI}/remote/${id}?mock=1`);
	await expect(page).toHaveURL(new RegExp(`/remote/session/${id}`));
	await expect(page.locator(`#rd-panel-${id}.streaming, .session-panel.streaming`).first()).toBeVisible({
		timeout: 60_000
	});
}

const viewer = (page: Page) => page.locator('main .console-viewer');
const screen = (page: Page) => page.locator('.session-panel:not([hidden]) .session-canvas').first();

test.beforeEach(async ({ page }) => {
	test.skip(!(await mockOn(page)), 'the console runs without ND_DEV_MOCK_REMOTE=1');
	await page.goto(`${UI}/devices`);
	await page.waitForLoadState('networkidle');
});

test('a deep link opens the session inside the console, with its sidebar and top bar', async ({ page }) => {
	await openSession(page, A);
	await expect(page.locator('.shell .shell-sidebar, .shell nav').first()).toBeVisible();
	await expect(page.locator('.shell-topbar')).toBeVisible();
	await expect(viewer(page)).toBeVisible();
	await expect(page.getByRole('tab', { selected: true })).toHaveAccessibleName(/Connected/);
	await expectNoAxeViolations(page, '.session-bar');
});

test('sessions stay connected on other pages and come back as they were', async ({ page }) => {
	await openSession(page, A);
	// Mark the screen: the same element must come back, not a new connection
	await screen(page).evaluate((el) => el.setAttribute('data-probe', 'kept'));
	await page.locator('.shell').getByRole('link', { name: 'All devices' }).click();
	await expect(page).toHaveURL(/\/devices/);
	await expect(viewer(page)).toBeHidden();
	const strip = page.getByRole('navigation', { name: 'Open sessions' });
	await expect(strip).toBeVisible();
	await expect(strip.getByRole('link')).toHaveCount(1);
	await expectNoAxeViolations(page, '.session-strip');
	await strip.getByRole('link').first().click();
	await expect(page).toHaveURL(new RegExp(`/remote/session/${A}`));
	await expect(viewer(page)).toBeVisible();
	await expect(page.locator('.session-panel.streaming')).toBeVisible();
	await expect(screen(page)).toHaveAttribute('data-probe', 'kept');
});

test('two sessions are tabs; a middle click closes one, the last close goes back', async ({ page }) => {
	await openSession(page, A);
	await addSession(page, B);
	const tabs = page.getByRole('tablist', { name: 'Open sessions' }).getByRole('tab');
	await expect(tabs).toHaveCount(2);
	// Picking the other tab changes the address
	await tabs.first().click();
	await expect(page).toHaveURL(new RegExp(`/remote/session/${A}`));
	await page.locator('.session-tab').nth(1).click({ button: 'middle' });
	await expect(tabs).toHaveCount(1);
	await page.locator('.session-tab .tab-close').first().click();
	await expect(page.locator('.console-viewer')).toHaveCount(0);
	await expect(page).not.toHaveURL(/\/remote\/session\//);
});

test('the sidebar folds away and comes back, and is remembered', async ({ page }) => {
	await openSession(page, A);
	const hide = page.getByRole('button', { name: 'Hide the sidebar' });
	await hide.click();
	await expect(page.getByRole('button', { name: 'Show the sidebar' })).toHaveAttribute(
		'aria-pressed',
		'false'
	);
	// Folded: the top bar offers the menu that opens the sidebar as a drawer
	await expect(page.locator('.shell-topbar button[aria-controls]').first()).toBeVisible();
	await page.reload();
	await expect(page.locator('.session-panel.streaming')).toBeVisible({ timeout: 60_000 });
	await expect(page.getByRole('button', { name: 'Show the sidebar' })).toBeVisible();
	await page.getByRole('button', { name: 'Show the sidebar' }).click();
	await expect(page.getByRole('button', { name: 'Hide the sidebar' })).toBeVisible();
});

test('the remote screen keeps the console shortcuts, and F8 gives the keyboard back', async ({ page }) => {
	await openSession(page, A);
	await screen(page).focus();
	await page.keyboard.press('Control+KeyK');
	await expect(page.getByRole('dialog')).toHaveCount(0);
	await page.keyboard.press('F8');
	await expect(page.getByRole('group', { name: 'Session controls' })).toBeVisible();
	await expect(screen(page)).not.toBeFocused();
	// Away from the screen the console's search opens again
	await page.locator('body').click({ position: { x: 5, y: 5 } });
	await page.keyboard.press('Control+KeyK');
	await expect(page.getByRole('dialog')).toBeVisible();
});

test('Open in new tab moves the session to the standalone viewer', async ({ page, context }) => {
	await openSession(page, A);
	const [tab] = await Promise.all([
		context.waitForEvent('page'),
		page.getByRole('button', { name: 'Open in new tab' }).click()
	]);
	await expect(tab).toHaveURL(new RegExp(`/remote/${A}\\?standalone=1`));
	await expect(tab.locator('.session-panel')).toBeVisible({ timeout: 60_000 });
	await expect(tab.locator('.shell')).toHaveCount(0);
	await expect(page.locator('.console-viewer')).toHaveCount(0);
	await tab.close();
});

test('the device panel Connect opens the session in place', async ({ page }) => {
	await page.goto(`${UI}/devices?open=${A}`);
	const panel = page.getByRole('dialog');
	await expect(panel).toBeVisible();
	await panel.getByRole('button', { name: 'Connect', exact: true }).click();
	await expect(page).toHaveURL(new RegExp(`/remote/session/${A}`));
	await expect(viewer(page)).toBeVisible();
	await expect(page.locator('.session-panel')).toBeVisible();
});

test('?standalone=1 keeps the viewer of its own', async ({ page }) => {
	await page.goto(`${UI}/remote/${A}?mock=1&standalone=1`);
	await expect(page.locator('.session-panel.streaming')).toBeVisible({ timeout: 60_000 });
	await expect(page).toHaveURL(new RegExp(`/remote/${A}\\?`));
	await expect(page.locator('.shell')).toHaveCount(0);
	await expect(page.getByRole('button', { name: 'Back to devices' })).toBeVisible();
});

test('phone width: the session stays usable, the sidebar is the drawer', async ({ page }) => {
	await page.setViewportSize({ width: 390, height: 844 });
	await openSession(page, A);
	await expect(screen(page)).toBeVisible();
	const box = await screen(page).boundingBox();
	expect(box?.width ?? 0).toBeGreaterThan(300);
	// Nothing wider than the screen
	expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
});

test('Remote sessions in the sidebar opens inside the console: the open sessions, and a new one by ID', async ({
	page
}) => {
	await openSession(page, A);
	await page.locator('.shell nav a[href$="/remote/sessions"]').click();
	await expect(page).toHaveURL(/\/remote\/sessions$/);
	await expect(page.locator('.shell-topbar')).toBeVisible();
	const list = page.locator('.session-list');
	await expect(list.getByRole('link')).toHaveCount(1);
	await expect(list).toContainText('Connected');
	await expect(page.getByRole('navigation', { name: 'Open sessions' })).toHaveCount(0);
	await page.getByLabel('Device ID', { exact: true }).fill(B);
	await page.locator('.quick').getByRole('button', { name: 'Connect', exact: true }).click();
	await expect(page).toHaveURL(new RegExp(`/remote/session/${B}`));
	await expect(page.getByRole('tablist', { name: 'Open sessions' }).getByRole('tab')).toHaveCount(2);
	await expect(page.locator(`#rd-panel-${A}.streaming`)).toHaveCount(1);
	await expectNoAxeViolations(page, '.session-bar');
});
