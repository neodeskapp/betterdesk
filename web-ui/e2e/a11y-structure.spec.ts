/**
 * Page structure (WCAG 2.4.1, 2.4.2, 1.3.1): every page has a title that
 * names it, all content sits in a landmark, landmarks of one kind have
 * different names, and every page with blocks before its content has a
 * skip link to it.
 */
import AxeBuilder from '@axe-core/playwright';
import type { Page } from '@playwright/test';
import { test, expect } from './fixtures.ts';
import { UI } from './roles.ts';

async function structureViolations(page: Page): Promise<string[]> {
	const r = await new AxeBuilder({ page })
		.withRules(['document-title', 'region', 'landmark-unique'])
		.analyze();
	return r.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(' ')).join(', ')}`);
}

test.describe('page structure', () => {
	for (const path of ['/help-requests', '/users', '/account', '/organizations', '/languages', '/billing']) {
		test(`${path}: content in landmarks with distinct names`, async ({ page }) => {
			await page.goto(`${UI}${path}`);
			await expect(page.locator('#main-content h1')).toBeVisible();
			await page.waitForLoadState('networkidle');
			expect(await structureViolations(page)).toEqual([]);
		});
	}

	test('an address the console has no page for says so in its title', async ({ page }) => {
		await page.goto(`${UI}/kit`);
		await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
		await expect(page).toHaveTitle(/^(\(\d+\) )?.+ - /);
		const h1 = (await page.getByRole('heading', { level: 1 }).textContent())!.trim();
		expect(await page.title()).toContain(h1);
	});

	test('the flow builder has a skip link to its content', async ({ page }) => {
		await page.goto(`${UI}/cdap-studio`);
		await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
		await page.keyboard.press('Tab');
		const skip = page.locator(':focus');
		await expect(skip).toHaveAttribute('href', '#main-content');
		await page.keyboard.press('Enter');
		await expect(page.locator('#main-content')).toBeFocused();
	});
});

test.describe('public pages', () => {
	// An unknown code: the server answers 404 and there is no session
	test.use({ storageState: { cookies: [], origins: [] }, allowProblems: [/(http|status of) 40[14]/] });
	for (const path of ['/join/AAAA-BBBB', '/s/AAAA-BBBB']) {
		test(`${path}: a title that names the page`, async ({ page }) => {
			await page.goto(`${UI}${path}`);
			await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
			expect(await page.title()).not.toBe('');
			expect(await structureViolations(page)).toEqual([]);
		});
	}
});
