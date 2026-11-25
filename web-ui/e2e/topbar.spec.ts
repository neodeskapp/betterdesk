import type { Page } from '@playwright/test';
import { test, expect } from './fixtures.ts';
import { UI } from './roles.ts';

/**
 * The top bar (console structure plan, step 3): search, Quick support,
 * notifications, Help as an icon and the account at the end, at every
 * width. Theme, language and accessibility are in the account menu under
 * Preferences; Refresh is gone. On a phone there is no second row and no
 * menu button: the bottom bar's More opens the sidebar, with Help at its
 * end.
 */

const bar = (page: Page) => page.locator('.shell-topbar');

/** The names of the top bar's visible buttons, in order */
async function buttons(page: Page): Promise<string[]> {
	return bar(page)
		.locator('button')
		.evaluateAll((list) =>
			list
				.filter((b) => b.getClientRects().length > 0 && getComputedStyle(b).visibility !== 'hidden')
				.map((b) => (b.getAttribute('aria-label') || b.textContent || '').replace(/\s+/g, ' ').trim())
		);
}

test.use({ role: 'super_admin' });

test.describe('on a wide screen', () => {
	test.use({ viewport: { width: 1440, height: 900 }, hasTouch: false });

	test('search, Quick support with its name, notifications, Help, and the account last', async ({ page }) => {
		await page.goto(`${UI}/devices`);
		await expect(bar(page).locator('.search-trigger')).toBeVisible();
		const names = await buttons(page);
		for (const gone of ['Refresh', 'Accessibility', 'Language', 'Open menu', 'More'])
			expect(
				names.some((n) => n.startsWith(gone)),
				gone
			).toBe(false);
		expect(names.some((n) => n.startsWith('Theme'))).toBe(false);
		const help = bar(page).getByRole('button', { name: 'Help', exact: true });
		await expect(help).toBeVisible();
		await expect(help).toHaveClass(/icon-btn/);
		await expect(bar(page).locator('.quick-support-label')).toBeVisible();
		// The account is the last button, also with the sidebar open
		const last = bar(page).locator('button').last();
		await expect(last).toHaveAttribute('aria-haspopup', 'menu');
		await expect(last).toContainText('e2e_super_admin');
		// The sidebar keeps "Powered by NeoDesk" and has no account button
		await expect(page.locator('.sidebar-footer .account-btn')).toHaveCount(0);
		await expect(page.locator('.sidebar-footer .powered-by')).toBeVisible();
	});

	test('Help: documentation, Discord, GitHub, keyboard shortcuts and platform support', async ({ page }) => {
		await page.goto(`${UI}/devices`);
		await bar(page).getByRole('button', { name: 'Help', exact: true }).click();
		const panel = page.locator('.help-panel');
		for (const name of ['Documentation', 'Community on Discord', 'GitHub repository'])
			await expect(panel.getByRole('link', { name })).toBeVisible();
		await expect(panel.getByRole('link', { name: 'Platform support' })).toHaveAttribute(
			'href',
			/\/cross-platform$/
		);
		await panel.getByRole('button', { name: 'Keyboard shortcuts' }).click();
		const dialog = page.getByRole('dialog', { name: 'Keyboard shortcuts' });
		await expect(dialog).toContainText('Ctrl K');
		await page.keyboard.press('Escape');
		await expect(dialog).toHaveCount(0);
		// Platform support is reference: it left the Devices section
		await expect(page.locator('.shell-sidebar a[href$="/cross-platform"]')).toHaveCount(0);
	});

	test('the account menu sets theme, language and accessibility under Preferences', async ({ page }) => {
		await page.goto(`${UI}/devices`);
		const account = bar(page).locator('button').last();
		await account.click();
		const menu = page.getByRole('menu', { name: 'Account' });
		await menu.getByRole('menuitemradio', { name: 'Dark' }).click();
		await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
		await account.click();
		await menu.getByRole('menuitemradio', { name: 'Console default' }).click();
		await account.click();
		await menu.getByRole('menuitem', { name: /^Language/ }).click();
		await expect(
			page.getByRole('menu', { name: 'Language' }).getByRole('menuitemradio', { name: 'Deutsch' })
		).toBeVisible();
		await page.keyboard.press('Escape');
		await account.click();
		await menu.getByRole('menuitem', { name: /^Accessibility/ }).click();
		await expect(page.getByRole('dialog', { name: 'Accessibility' })).toBeVisible();
	});
});

test.describe('between phone and desktop', () => {
	test('1100 px: Quick support is an icon; 900 px: the menu button opens the sidebar', async ({ page }) => {
		await page.setViewportSize({ width: 1100, height: 900 });
		await page.goto(`${UI}/devices`);
		await expect(bar(page).getByRole('button', { name: 'Quick support' })).toBeVisible();
		await expect(bar(page).locator('.quick-support-label')).not.toBeInViewport();
		await expect(bar(page).locator('button').last()).toHaveAttribute('aria-haspopup', 'menu');
		await page.setViewportSize({ width: 900, height: 900 });
		await bar(page).getByRole('button', { name: 'Open menu' }).click();
		await expect(page.locator('.shell-sidebar')).toBeInViewport();
	});
});

test.describe('on a phone', () => {
	test.use({ viewport: { width: 390, height: 844 }, hasTouch: true });

	test('logo, search, Quick support, notifications and the account; no second row', async ({ page }) => {
		await page.goto(`${UI}/devices`);
		await expect(bar(page).locator('.topbar-logo')).toBeVisible();
		const names = await buttons(page);
		expect(
			names.some((n) => n === 'More'),
			'a More button in the top bar'
		).toBe(false);
		expect(
			names.some((n) => n === 'Open menu'),
			'the menu button'
		).toBe(false);
		expect(
			names.some((n) => n === 'Help'),
			'Help in the top bar'
		).toBe(false);
		await expect(bar(page).getByRole('button', { name: 'Quick support' })).toBeVisible();
		await expect(bar(page).getByRole('button', { name: /^Notifications/ })).toBeVisible();
		await expect(bar(page).locator('button').last()).toHaveAttribute('aria-haspopup', 'menu');
		const width = await page.evaluate(() => document.documentElement.scrollWidth);
		expect(width).toBeLessThanOrEqual(390);
	});

	test("the bottom bar's More opens the sidebar with Help at its end", async ({ page }) => {
		await page.goto(`${UI}/devices`);
		await page.locator('.bottom-nav').getByRole('button', { name: 'More' }).click();
		const help = page.locator('.shell-sidebar').getByRole('button', { name: 'Help', exact: true });
		await expect(help).toBeVisible();
		await help.click();
		await expect(page.getByRole('dialog', { name: 'Help' })).toBeVisible();
	});
});
