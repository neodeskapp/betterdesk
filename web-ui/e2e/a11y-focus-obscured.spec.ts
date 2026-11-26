/**
 * WCAG 2.4.11 Focus Not Obscured: a control that gets keyboard focus is
 * never hidden under the sticky top bar, the sticky save bar, the bottom
 * navigation on phones or a pinned table column. Tabs through the page in
 * both directions and checks that the middle of each focused control is
 * the control itself, not a bar on top of it.
 */
import type { Page } from '@playwright/test';
import { test, expect } from './fixtures.ts';
import { UI } from './roles.ts';

type Stop = { name: string; coveredBy: string };

/** Press Tab (or Shift+Tab) `count` times; the stops whose middle is covered by something else */
async function coveredStops(page: Page, count: number, backwards = false): Promise<Stop[]> {
	const covered: Stop[] = [];
	for (let i = 0; i < count; i++) {
		await page.keyboard.press(backwards ? 'Shift+Tab' : 'Tab');
		// Let a scroll that the focus started (smooth in some boxes) come to rest
		await page.waitForTimeout(120);
		const stop = await page.evaluate(() => {
			const el = document.activeElement as HTMLElement | null;
			if (!el || el === document.body) return null;
			const r = el.getBoundingClientRect();
			if (!r.width || !r.height) return null;
			const x = r.left + r.width / 2;
			const y = r.top + r.height / 2;
			if (x < 0 || y < 0 || x >= innerWidth || y >= innerHeight)
				return { name: el.outerHTML.slice(0, 80), coveredBy: 'outside the window' };
			const hit = document.elementFromPoint(x, y);
			if (!hit || el.contains(hit) || hit.contains(el)) return null;
			const name = (el.getAttribute('aria-label') || el.textContent || el.outerHTML).trim().slice(0, 60);
			const bar = hit.closest('header, footer, nav, th, .save-bar, .bottom-nav, [class*=bar]') ?? hit;
			return { name, coveredBy: `${bar.tagName.toLowerCase()}.${[...bar.classList].join('.')}` };
		});
		if (stop) covered.push(stop);
	}
	return covered;
}

test.describe('focus is never hidden under a bar', () => {
	// Each test presses Tab up to 140 times
	test.describe.configure({ timeout: 120_000 });
	test('settings: under the top bar going up, under the save bar going down', async ({ page }) => {
		await page.setViewportSize({ width: 1280, height: 640 });
		await page.goto(`${UI}/settings`);
		const main = page.locator('#main-content');
		await expect(main.getByRole('heading', { level: 1 })).toBeVisible();
		// Unsaved changes pin the save bar to the bottom edge
		const field = main.locator('input[type=text], input:not([type]), input[type=url]').first();
		await field.click();
		await field.press('End');
		await field.pressSequentially('x');
		await expect(page.locator('.save-bar.is-dirty')).toBeVisible();
		await field.focus();
		expect(await coveredStops(page, 40)).toEqual([]);
		expect(await coveredStops(page, 60, true)).toEqual([]);
	});

	test('devices on a phone: nothing under the bottom navigation', async ({ page }) => {
		await page.setViewportSize({ width: 390, height: 700 });
		await page.goto(`${UI}/devices`);
		await expect(page.locator('#main-content').getByRole('heading', { level: 1 })).toBeVisible();
		await page.waitForLoadState('networkidle');
		expect(await coveredStops(page, 70)).toEqual([]);
		expect(await coveredStops(page, 70, true)).toEqual([]);
	});

	test('users: header and cells stay clear of the pinned actions column', async ({ page }) => {
		await page.setViewportSize({ width: 1280, height: 800 });
		await page.goto(`${UI}/users`);
		await expect(page.getByRole('table').first()).toBeVisible();
		await page.waitForLoadState('networkidle');
		expect(await coveredStops(page, 60)).toEqual([]);
	});
});
