/**
 * The viewer's clipboard on a plain HTTP page, where browsers give the
 * page no clipboard API: the note says so once, Ctrl+V still sends the
 * text through the browser's paste event, and text the device copies
 * reaches this computer's clipboard inside a gesture (the copy command):
 * at once after Ctrl+C in the viewer, otherwise it waits for the next click
 * or key press, which the toolbar shows as "Copy here".
 *
 * The e2e runs reach the console by its container name, which is plain
 * HTTP for the browsers. Needs ND_DEV_MOCK_REMOTE=1; skipped without it.
 */
import type { Page } from '@playwright/test';
import { test, expect } from './fixtures.ts';
import { UI } from './roles.ts';

const MOCK_DEVICE = '900100012';

interface MockDevice {
	clipboard?: { text: string; image: number };
	pasted?: string[];
	copyText?: string;
	copy(text: string): void;
}

function device(page: Page): Promise<Omit<MockDevice, 'copy'>> {
	return page.evaluate(() => {
		const d = Reflect.get(window, '__ndMockDevice') as MockDevice;
		return { clipboard: d.clipboard, pasted: d.pasted, copyText: d.copyText };
	});
}

async function streaming(page: Page): Promise<boolean> {
	const r = await page.request.get(`/api/remote/viewer/${MOCK_DEVICE}?mock=1`);
	if (!r.ok() || !(await r.json()).mockRemote) return false;
	await page.goto(`${UI}/devices`);
	await page.waitForLoadState('networkidle');
	await page.goto(`${UI}/remote/${MOCK_DEVICE}?mock=1`);
	await expect(page.locator('.session-panel.streaming')).toBeVisible({ timeout: 60_000 });
	return !(await page.evaluate(() => isSecureContext));
}

/** This computer's clipboard as a paste into a text field shows it (the page can't read it on HTTP) */
async function pasteIntoField(page: Page): Promise<string> {
	await page.evaluate(() => {
		const field = document.createElement('textarea');
		field.id = 'paste-probe';
		field.setAttribute('aria-label', 'paste probe');
		document.body.append(field);
		field.focus();
	});
	await page.keyboard.press('Control+KeyV');
	const value = await page.locator('#paste-probe').inputValue();
	await page.evaluate(() => document.getElementById('paste-probe')?.remove());
	return value;
}

/** Puts text on this computer's clipboard the way a person would: select and Ctrl+C in a field */
async function copyFromField(page: Page, text: string): Promise<void> {
	await page.evaluate((t) => {
		const field = document.createElement('textarea');
		field.id = 'copy-probe';
		field.setAttribute('aria-label', 'copy probe');
		field.value = t;
		document.body.append(field);
		field.focus();
		field.select();
	}, text);
	await page.keyboard.press('Control+KeyC');
	await page.evaluate(() => document.getElementById('copy-probe')?.remove());
}

test('plain HTTP: the note says once that the page has no clipboard access', async ({ page }) => {
	test.skip(!(await streaming(page)), 'needs the mock device on a plain HTTP page');
	await page.evaluate(() => localStorage.removeItem('nd_viewer_clipboard_http_seen'));
	await page.reload();
	await expect(page.locator('.session-panel.streaming')).toBeVisible({ timeout: 60_000 });
	const note = page.locator('.clipboard-note');
	await expect(note).toContainText('plain HTTP');
	await note.getByRole('button', { name: 'Dismiss' }).click();
	await expect(note).toHaveCount(0);
	await page.reload();
	await expect(page.locator('.session-panel.streaming')).toBeVisible({ timeout: 60_000 });
	await expect(note).toHaveCount(0);
});

test('plain HTTP: Ctrl+V sends the text and then pastes it on the device', async ({ page }) => {
	test.skip(!(await streaming(page)), 'needs the mock device on a plain HTTP page');
	await copyFromField(page, 'sent over http');
	await page.locator('.session-canvas').focus();
	await page.keyboard.press('Control+KeyV');
	await expect.poll(async () => (await device(page)).pasted).toEqual(['sent over http']);
});

test('plain HTTP: what the device copies after Ctrl+C here lands on this computer at once', async ({
	page
}) => {
	test.skip(!(await streaming(page)), 'needs the mock device on a plain HTTP page');
	await page.evaluate(() => {
		(Reflect.get(window, '__ndMockDevice') as MockDevice).copyText = 'copied on the device over http';
	});
	await page.locator('.session-canvas').focus();
	await page.keyboard.press('Control+KeyC');
	await expect(page.locator('[data-clipboard]')).toHaveAttribute('data-clipboard', 'on');
	// The copy reached this computer within the key press's time (no "Copy here" waiting)
	await page.waitForTimeout(400);
	await expect(page.locator('[data-clipboard]')).toHaveAttribute('data-clipboard', 'on');
	expect(await pasteIntoField(page)).toBe('copied on the device over http');
});

test('plain HTTP: a copy on the device the browser refuses to write waits, and Copy here takes it', async ({
	page
}) => {
	test.skip(!(await streaming(page)), 'needs the mock device on a plain HTTP page');
	await copyFromField(page, 'before');
	// Headless browsers allow the copy command without a gesture; a real one refuses it after
	// about 5 s without a key press or click. The page refuses until the test allows it again
	await page.evaluate(() => {
		const original = document.execCommand.bind(document);
		const w = window as unknown as { refuseCopy: boolean };
		w.refuseCopy = true;
		document.execCommand = (...args: Parameters<Document['execCommand']>) =>
			w.refuseCopy ? false : original(...args);
	});
	await page.evaluate(() => (Reflect.get(window, '__ndMockDevice') as MockDevice).copy('copied later'));
	const button = page.locator('[data-clipboard]');
	await expect(button).toHaveAttribute('data-clipboard', 'pending');
	await expect(button).toContainText('Copy here');
	await page.evaluate(() => ((window as unknown as { refuseCopy: boolean }).refuseCopy = false));
	await button.click();
	await expect(button).toHaveAttribute('data-clipboard', 'on');
	expect(await pasteIntoField(page)).toBe('copied later');
});
