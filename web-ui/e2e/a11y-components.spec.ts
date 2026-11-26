/**
 * Accessibility details that need a real layout or a remote session:
 * - The sidebar's width handle is a 24 px target (WCAG 2.5.8), while the
 *   line it draws stays thin.
 * - A remote session tab says in its description that Delete closes it
 *   (the × next to it is pointer only: a button may not sit in a tablist),
 *   so keyboard, screen reader and voice control users learn the way.
 *   Needs the console with ND_DEV_MOCK_REMOTE=1; skipped without it.
 */
import { test, expect } from './fixtures.ts';
import { UI } from './roles.ts';

test('the sidebar width handle is at least 24 px wide', async ({ page }) => {
	await page.setViewportSize({ width: 1400, height: 900 });
	await page.goto(`${UI}/devices`);
	const handle = page.getByRole('separator', { name: 'Resize sidebar' });
	await expect(handle).toBeVisible();
	const box = await handle.boundingBox();
	expect(box?.width ?? 0).toBeGreaterThanOrEqual(24);
});

test('a session tab says that Delete closes it', async ({ page }) => {
	const mock = await page.request.get('/api/remote/viewer/900100012?mock=1');
	test.skip(!(mock.ok() && (await mock.json()).mockRemote), 'the console runs without ND_DEV_MOCK_REMOTE=1');
	await page.goto(`${UI}/remote/900100012?mock=1`);
	const tab = page.getByRole('tab', { selected: true });
	await expect(tab).toBeVisible({ timeout: 60_000 });
	await expect(tab).toHaveAccessibleDescription(/Delete/);
	await tab.focus();
	await page.keyboard.press('Delete');
	await expect(page.getByRole('tab')).toHaveCount(0);
});
