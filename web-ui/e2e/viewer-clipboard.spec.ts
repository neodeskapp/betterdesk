/**
 * The clipboard between this computer and the remote one in the browser
 * viewer, with the dev mock device, in Chromium and Firefox: Ctrl+V sends
 * this computer's clipboard and then pastes it on the device in one go,
 * pasted images reach the device as PNG, Ctrl+C on the device puts its
 * text on this computer's clipboard, the toolbar's state turns the
 * clipboard off and on. The page runs at http://localhost (a secure
 * context, local-origin.ts), where the browsers have their clipboard API.
 * Firefox lets a test read and write the clipboard without a gesture only
 * with dom.events.testing.asyncClipboard.
 *
 * Needs the console with ND_DEV_MOCK_REMOTE=1; skipped without it.
 */
import type { Page } from '@playwright/test';
import { test, expect } from './local-origin.ts';
import { UI } from './roles.ts';

const MOCK_DEVICE = '900100012';

test.use({
	launchOptions: { firefoxUserPrefs: { 'dom.events.testing.asyncClipboard': true } }
});

interface MockDevice {
	clipboard?: { text: string; image: number };
	pasted?: string[];
	copyText?: string;
}

function device(page: Page): Promise<MockDevice> {
	return page.evaluate(() => Reflect.get(window, '__ndMockDevice') as MockDevice);
}

async function streaming(page: Page): Promise<boolean> {
	const r = await page.request.get(`/api/remote/viewer/${MOCK_DEVICE}?mock=1`);
	if (!r.ok() || !(await r.json()).mockRemote) return false;
	await page.goto(`${UI}/devices`);
	await page.waitForLoadState('networkidle');
	await page.goto(`${UI}/remote/${MOCK_DEVICE}?mock=1`);
	await expect(page.locator('.session-panel.streaming')).toBeVisible({ timeout: 60_000 });
	expect(await page.evaluate(() => isSecureContext)).toBe(true);
	return true;
}

async function writeLocal(page: Page, text: string): Promise<void> {
	await page.evaluate((t) => navigator.clipboard.writeText(t), text);
}

function readLocal(page: Page): Promise<string> {
	return page.evaluate(() => navigator.clipboard.readText());
}

test.beforeEach(async ({ context, browserName }) => {
	if (browserName === 'chromium') await context.grantPermissions(['clipboard-read', 'clipboard-write']);
});

test('Ctrl+V in the remote screen sends this computer clipboard, then the device pastes it', async ({
	page
}) => {
	test.skip(!(await streaming(page)), 'the console runs without ND_DEV_MOCK_REMOTE=1');
	await writeLocal(page, 'typed on this computer');
	await page.locator('.session-canvas').focus();
	await page.keyboard.press('Control+KeyV');
	await expect.poll(async () => (await device(page)).pasted).toEqual(['typed on this computer']);
	expect((await device(page)).clipboard?.text).toBe('typed on this computer');
	// The next paste with the same clipboard pastes again without sending it again
	await page.keyboard.press('Control+KeyV');
	await expect.poll(async () => (await device(page)).pasted?.length).toBe(2);
});

test('an image copied here reaches the device as PNG with Ctrl+V, and one copied there comes back', async ({
	page,
	browserName
}) => {
	// Headless Firefox keeps only text on its clipboard (write() of a PNG succeeds, read() gives nothing back)
	test.skip(browserName === 'firefox', 'headless Firefox has no image clipboard');
	test.skip(!(await streaming(page)), 'the console runs without ND_DEV_MOCK_REMOTE=1');
	await page.evaluate(async () => {
		const c = document.createElement('canvas');
		c.width = 40;
		c.height = 30;
		c.getContext('2d')!.fillRect(0, 0, 40, 30);
		const blob = await new Promise<Blob>((done) => c.toBlob((b) => done(b!), 'image/png'));
		await navigator.clipboard.write([new ClipboardItem({ 'image/png': blob })]);
	});
	await page.locator('.session-canvas').focus();
	await page.keyboard.press('Control+KeyV');
	// The browser hands the image on as PNG (it may encode it anew, so the size differs)
	await expect.poll(async () => (await device(page)).clipboard?.image ?? 0).toBeGreaterThan(0);
	expect((await device(page)).pasted).toHaveLength(1);
	// The device copies a picture: this computer's clipboard holds a PNG then
	await page.locator('.session-canvas').click();
	await page.evaluate(async () => {
		const c = document.createElement('canvas');
		c.width = 8;
		c.height = 8;
		c.getContext('2d')!.fillRect(0, 0, 8, 8);
		const blob = await new Promise<Blob>((done) => c.toBlob((b) => done(b!), 'image/png'));
		(Reflect.get(window, '__ndMockDevice') as { copy(t: string, b: Blob): void }).copy('', blob);
	});
	await expect
		.poll(() =>
			page.evaluate(async () => (await navigator.clipboard.read()).flatMap((item) => [...item.types]))
		)
		.toContain('image/png');
});

test('Ctrl+C on the device puts its text on this computer clipboard', async ({ page }) => {
	test.skip(!(await streaming(page)), 'the console runs without ND_DEV_MOCK_REMOTE=1');
	await writeLocal(page, 'old content');
	await page.evaluate(() => {
		(Reflect.get(window, '__ndMockDevice') as MockDevice).copyText = 'copied on the device';
	});
	await page.locator('.session-canvas').focus();
	await page.keyboard.press('Control+KeyC');
	await expect.poll(() => readLocal(page)).toBe('copied on the device');
	await expect(page.locator('[data-clipboard]')).toHaveAttribute('data-clipboard', 'on');
});

test('the toolbar turns the clipboard off: nothing goes over then, and on again', async ({ page }) => {
	test.skip(!(await streaming(page)), 'the console runs without ND_DEV_MOCK_REMOTE=1');
	const button = page.locator('[data-clipboard]');
	await expect(button).toHaveAttribute('data-clipboard', 'on');
	await expect(button).toHaveAttribute('aria-pressed', 'true');
	await button.click();
	await expect(button).toHaveAttribute('data-clipboard', 'off');
	await writeLocal(page, 'must stay here');
	await page.locator('.session-canvas').focus();
	await page.keyboard.press('Control+KeyV');
	// The key still reaches the device, which pastes what it had
	await expect.poll(async () => (await device(page)).pasted).toEqual(['']);
	expect((await device(page)).clipboard).toBeUndefined();
	await button.click();
	await expect(button).toHaveAttribute('data-clipboard', 'on');
	await page.locator('.session-canvas').focus();
	await page.keyboard.press('Control+KeyV');
	await expect.poll(async () => (await device(page)).pasted).toEqual(['', 'must stay here']);
});
