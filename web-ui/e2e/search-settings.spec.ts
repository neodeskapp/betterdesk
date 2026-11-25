import { test, expect } from './fixtures.ts';
import { UI } from './roles.ts';

/**
 * The search in the top bar (Ctrl K) finds settings and things to do:
 * type, arrow to the result, Enter, and the setting is on screen with its
 * outline; settings the account can't see are not offered.
 */

async function openSearch(page: import('@playwright/test').Page) {
	await expect(page.locator('.search-trigger')).toBeVisible();
	await page.keyboard.press('Control+k');
	const box = page.getByRole('combobox');
	await expect(box).toBeFocused();
	return box;
}

test.describe('as an admin', () => {
	test.use({ role: 'super_admin' });

	test('a German word finds the time zone; arrows and Enter open it, outlined', async ({ page }) => {
		await page.goto(`${UI}/devices`);
		const box = await openSearch(page);
		await box.fill('zeitzone');
		const group = page.getByRole('group', { name: /^Settings/ });
		const option = group.getByRole('option', { name: /Time zone/ }).first();
		await expect(option).toContainText('Settings, Time');
		// Arrow down until the setting is the highlighted option
		const id = await option.getAttribute('id');
		for (let i = 0; i < 12 && (await box.getAttribute('aria-activedescendant')) !== id; i++)
			await page.keyboard.press('ArrowDown');
		await expect(box).toHaveAttribute('aria-activedescendant', id ?? '');
		await page.keyboard.press('Enter');
		await expect(page).toHaveURL(/\/settings\?tab=time#time-billing-timezone$/);
		const found = page.locator('.is-found');
		await expect(found).toBeVisible();
		await expect(found).toBeInViewport();
		await expect(
			page.locator('.is-found:has(#time-billing-timezone), .is-found#time-billing-timezone')
		).toHaveCount(1);
	});

	test('a setting on another tab of the page that is open: the tab changes and the field shows', async ({
		page
	}) => {
		await page.goto(`${UI}/settings`);
		const box = await openSearch(page);
		await box.fill('ldap host');
		await page
			.getByRole('group', { name: /^Settings/ })
			.getByRole('option', { name: /Host/ })
			.first()
			.click();
		await expect(page).toHaveURL(/\/settings\?tab=auth#ldap-host$/);
		await expect(page.locator('#ldap-host')).toBeInViewport();
		await expect(page.locator('.is-found')).toBeVisible();
	});

	test('the same setting again is outlined again', async ({ page }) => {
		await page.goto(`${UI}/settings?tab=email#email-smtp-host`);
		await expect(page.locator('.is-found')).toBeVisible();
		await expect(page.locator('.is-found')).toHaveCount(0, { timeout: 6_000 });
		const box = await openSearch(page);
		await box.fill('smtp');
		await page
			.getByRole('group', { name: /^Settings/ })
			.getByRole('option', { name: /Mail server address/ })
			.click();
		await expect(page.locator('.is-found')).toBeVisible();
		await expect(page).toHaveURL(/tab=email#email-smtp-host$/);
	});

	test('things to do: Add a computer opens its dialog from any page', async ({ page }) => {
		await page.goto(`${UI}/users`);
		const box = await openSearch(page);
		await box.fill('invite');
		await expect(page.getByRole('group', { name: /^Actions/ })).toBeVisible();
		await page.keyboard.press('Enter');
		await expect(page).toHaveURL(/\/devices$/);
		await expect(page.getByRole('dialog', { name: 'Add a computer' })).toBeVisible();
	});

	test('recent searches come back in an empty field', async ({ page }) => {
		await page.goto(`${UI}/devices`);
		let box = await openSearch(page);
		await box.fill('smtp');
		await page.keyboard.press('Enter');
		await expect(page).toHaveURL(/tab=email/);
		box = await openSearch(page);
		const recent = page.getByRole('group', { name: /Recent searches/ });
		await expect(recent.getByRole('option', { name: 'smtp' })).toBeVisible();
		await page.keyboard.press('Enter');
		await expect(box).toHaveValue('smtp');
	});
});

test.describe('as a viewer', () => {
	test.use({ role: 'viewer' });

	test('settings the account cannot see are not offered', async ({ page }) => {
		await page.goto(`${UI}/`);
		const box = await openSearch(page);
		await box.fill('ldap');
		// The Sign-in page itself is a page result; none of its LDAP settings are
		await expect(page.getByRole('group', { name: /^Pages/ })).toBeVisible();
		await expect(page.getByRole('group', { name: /^Settings/ })).toHaveCount(0);
		await box.fill('password');
		await expect(
			page
				.getByRole('group', { name: /^Settings/ })
				.getByRole('option')
				.first()
		).toBeVisible();
	});
});
