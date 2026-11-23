/**
 * Appearance rules on the console's own pages (BUGS L358, L359, L361): the
 * stylesheet's rules for the console background, the web remote client's
 * background and the text font reach the pages' markup. The rules are the
 * ones the server writes for these settings (console/branding
 * console_css_test.go); they are added to the served stylesheet here, so
 * nothing is saved and parallel tests see the console as it is.
 */
import type { Page } from '@playwright/test';
import { test, expect } from './fixtures.ts';
import { UI } from './roles.ts';

const wallpaper = (scope: string, surface: string, color: string) =>
	`${scope} { isolation: isolate; }\n` +
	`${scope}::before {\n    content: '';\n    position: fixed;\n    inset: 0;\n    z-index: -1;\n    pointer-events: none;\n    background: ${color};\n}\n` +
	`${scope}::after {\n    content: '';\n    position: fixed;\n    inset: 0;\n    z-index: -1;\n    pointer-events: none;\n    background: rgba(0, 0, 0, 0.40);\n}\n` +
	`${surface} { background: transparent; }\n`;

async function withRules(page: Page, rules: string) {
	await page.route('**/css/branding.css*', async (route) => {
		const served = await route.fetch();
		await route.fulfill({
			response: served,
			body: (await served.text()) + '\n/* --- console pages --- */\n' + rules
		});
	});
}

const pseudo = (page: Page, which: '::before' | '::after') =>
	page.evaluate((w) => getComputedStyle(document.body, w).backgroundColor, which);

test('the console background shows behind the console pages, under their content', async ({ page }) => {
	await withRules(page, wallpaper('body:has(> #bd-root .shell)', '#bd-root .shell', '#203040'));
	await page.goto(`${UI}/devices`);
	await expect(page.locator('.shell')).toBeVisible();
	expect(await pseudo(page, '::before')).toBe('rgb(32, 48, 64)');
	expect(await pseudo(page, '::after')).toBe('rgba(0, 0, 0, 0.4)');
	await expect(page.locator('.shell')).toHaveCSS('background-color', 'rgba(0, 0, 0, 0)');
	// the layer is under the page: the heading is what a click there reaches
	const heading = page.getByRole('heading', { level: 1 });
	const box = (await heading.boundingBox())!;
	const hit = await page.evaluate(
		([x, y]) => document.elementFromPoint(x!, y!)?.closest('h1') !== null,
		[box.x + 4, box.y + box.height / 2]
	);
	expect(hit).toBe(true);
	// the sidebar keeps its own surface
	await expect(page.locator('.shell-sidebar')).not.toHaveCSS('background-color', 'rgba(0, 0, 0, 0)');
});

test('the web remote client background shows behind its device list', async ({ page }) => {
	await withRules(page, wallpaper('body:has(> #bd-root .desk)', '#bd-root .desk', '#304050'));
	await page.goto(`${UI}/remote`);
	await expect(page.locator('.desk')).toBeVisible();
	expect(await pseudo(page, '::before')).toBe('rgb(48, 64, 80)');
	await expect(page.locator('.desk')).toHaveCSS('background-color', 'rgba(0, 0, 0, 0)');
});

test('the text font is the font of the console text', async ({ page }) => {
	await withRules(
		page,
		"html:root {\n    --font: 'Courier New', 'IBM Plex Sans', system-ui, sans-serif;\n}\n"
	);
	await page.goto(`${UI}/devices`);
	await expect(page.locator('.shell')).toBeVisible();
	expect(await page.evaluate(() => getComputedStyle(document.body).fontFamily)).toMatch(/^"?Courier New"?,/);
	await expect(page.locator('.sidebar-nav')).toHaveCSS('font-family', /^"?Courier New"?,/);
});
