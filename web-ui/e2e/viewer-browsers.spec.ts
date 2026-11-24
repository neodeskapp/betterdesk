/**
 * What differs between Chromium and Firefox in the viewer, on a secure
 * page (local-origin.ts) with the dev mock device: Firefox has no
 * Keyboard Lock, so full screen says once which shortcuts stay with the
 * browser (Chromium hands them to the remote and says nothing); Actions
 * sends the Windows key and Alt+Tab in both; both decode the video codecs
 * the remote computers use with WebCodecs.
 *
 * Needs the console with ND_DEV_MOCK_REMOTE=1; skipped without it.
 */
import type { Page } from '@playwright/test';
import { test, expect } from './local-origin.ts';
import { UI } from './roles.ts';

const MOCK_DEVICE = '900100012';

async function streaming(page: Page): Promise<boolean> {
	const r = await page.request.get(`/api/remote/viewer/${MOCK_DEVICE}?mock=1`);
	if (!r.ok() || !(await r.json()).mockRemote) return false;
	await page.goto(`${UI}/devices`);
	await page.waitForLoadState('networkidle');
	await page.goto(`${UI}/remote/${MOCK_DEVICE}?mock=1&standalone=1`);
	await expect(page.locator('.session-panel.streaming')).toBeVisible({ timeout: 60_000 });
	return true;
}

test('full screen: a browser without Keyboard Lock says which shortcuts stay with it', async ({
	page,
	browserName
}) => {
	test.skip(!(await streaming(page)), 'the console runs without ND_DEV_MOCK_REMOTE=1');
	const lock = await page.evaluate(
		() => typeof (navigator as { keyboard?: { lock?: unknown } }).keyboard?.lock
	);
	expect(lock).toBe(browserName === 'chromium' ? 'function' : 'undefined');
	await page.getByRole('button', { name: /^Full screen/ }).click();
	await expect.poll(() => page.evaluate(() => !!document.fullscreenElement)).toBe(true);
	const note = page.getByText('This browser keeps some shortcuts for itself');
	if (browserName === 'firefox') await expect(note).toBeVisible();
	else {
		await page.waitForTimeout(500);
		await expect(note).toHaveCount(0);
	}
});

test('Actions sends the Windows key and Alt+Tab', async ({ page }) => {
	test.skip(!(await streaming(page)), 'the console runs without ND_DEV_MOCK_REMOTE=1');
	await page.locator('.session-canvas').focus();
	await page.keyboard.press('F8');
	const controls = page.getByRole('group', { name: 'Session controls' });
	await controls.getByRole('button', { name: 'Actions', exact: true }).click();
	const menu = page.getByRole('menu');
	await expect(menu.getByRole('menuitem', { name: 'Windows key' })).toBeVisible();
	await expect(menu.getByRole('menuitem', { name: 'Alt+Tab' })).toBeVisible();
	await menu.getByRole('menuitem', { name: 'Windows key' }).click();
	await expect(menu).toHaveCount(0);
});

test('both browsers decode VP9, AV1 and H.264 with WebCodecs on a secure page', async ({ page }) => {
	await page.goto(`${UI}/devices`);
	const codecs = await page.evaluate(async () => {
		const out: Record<string, boolean> = {};
		for (const [k, c] of [
			['vp9', 'vp09.00.10.08'],
			['av1', 'av01.0.04M.08'],
			['h264', 'avc1.42E01F'],
			['vp8', 'vp8']
		]) {
			out[k!] = !!(await VideoDecoder.isConfigSupported({ codec: c!, codedWidth: 1280, codedHeight: 720 }))
				.supported;
		}
		return out;
	});
	expect(codecs).toEqual({ vp9: true, av1: true, h264: true, vp8: true });
});
