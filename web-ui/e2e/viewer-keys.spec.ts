/**
 * Keys on the remote screen as a Mac sends them (the page says it runs on
 * a Mac), with the dev mock device, which is no Mac: Cmd+S reaches the
 * device as Ctrl+S, Cmd with another key stays Cmd, and the keyboard
 * menu's "Send Cmd as Ctrl for common shortcuts" turns that off.
 *
 * Needs the console with ND_DEV_MOCK_REMOTE=1; skipped without it.
 */
import type { Page } from '@playwright/test';
import { test, expect } from './fixtures.ts';
import { UI } from './roles.ts';

const MOCK_DEVICE = '900100012';

function keys(page: Page): Promise<string[]> {
	return page.evaluate(() => (Reflect.get(window, '__ndMockDevice') as { keys?: string[] }).keys ?? []);
}

test('on a Mac, Cmd+S goes over as Ctrl+S; Cmd+L stays; the menu turns it off', async ({ page }) => {
	const r = await page.request.get(`/api/remote/viewer/${MOCK_DEVICE}?mock=1`);
	test.skip(!r.ok() || !(await r.json()).mockRemote, 'the console runs without ND_DEV_MOCK_REMOTE=1');
	await page.addInitScript(() => {
		Object.defineProperty(Navigator.prototype, 'platform', { get: () => 'MacIntel' });
		Object.defineProperty(Navigator.prototype, 'userAgentData', { get: () => undefined });
	});
	await page.goto(`${UI}/devices`);
	await page.waitForLoadState('networkidle');
	await page.goto(`${UI}/remote/${MOCK_DEVICE}?mock=1`);
	await expect(page.locator('.session-panel.streaming')).toBeVisible({ timeout: 60_000 });
	const screen = page.locator('.session-panel:not([hidden]) .session-canvas');
	await screen.focus();
	await page.keyboard.down('Meta');
	await page.keyboard.press('KeyS');
	await page.keyboard.up('Meta');
	await expect.poll(() => keys(page)).toEqual(['ControlLeft+ctrl', 'KeyS+ctrl']);
	await page.keyboard.down('Meta');
	await page.keyboard.press('KeyL');
	await page.keyboard.up('Meta');
	await expect
		.poll(() => keys(page))
		.toEqual(['ControlLeft+ctrl', 'KeyS+ctrl', 'MetaLeft+meta', 'KeyL+meta']);

	await page.keyboard.press('F8');
	const controls = page.getByRole('group', { name: 'Session controls' });
	await controls.getByRole('button', { name: 'Display', exact: true }).click();
	const toggle = page.getByRole('menuitemcheckbox', { name: 'Send Cmd as Ctrl for common shortcuts' });
	await expect(toggle).toHaveAttribute('aria-checked', 'true');
	await toggle.click();
	await page.keyboard.press('Escape');
	await screen.focus();
	await page.keyboard.down('Meta');
	await page.keyboard.press('KeyS');
	await page.keyboard.up('Meta');
	await expect.poll(async () => (await keys(page)).slice(-2)).toEqual(['MetaLeft+meta', 'KeyS+meta']);
});
