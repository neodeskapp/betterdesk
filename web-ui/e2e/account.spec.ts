import { test, expect } from './fixtures.ts';
import { UI } from './roles.ts';

/**
 * Your account (/account): the account menu leads there; it has your
 * profile, password, two-factor sign-in, signed-in devices, API tokens
 * (for the roles that may make them) and preferences. The old addresses
 * of these parts land on them, and the top bar search finds them.
 */

test.describe('as an admin', () => {
	test.use({ role: 'admin' });

	test('the account menu opens Your account with all its parts', async ({ page }) => {
		await page.goto(`${UI}/devices`);
		await page.locator('.account-btn, .account-icon-btn').filter({ visible: true }).first().click();
		await page.getByRole('menuitem', { name: 'Your account' }).click();
		await expect(page).toHaveURL(/\/account$/);
		await expect(page.getByRole('heading', { level: 1, name: 'Your account' })).toBeVisible();
		for (const id of ['profile', 'password', 'two-factor', 'sessions', 'api-tokens', 'preferences'])
			await expect(page.locator(`#${id}`), id).toBeVisible();
		await expect(page.locator('#current-password')).toBeVisible();
		// The browsers this account is signed in on, with this one marked
		await expect(
			page.locator('#sessions').getByRole('listitem').filter({ hasText: 'This browser' })
		).toBeVisible();
	});

	test('old addresses land on their part of Your account', async ({ page }) => {
		for (const [from, to] of [
			['/settings?tab=auth#account-password', /\/account#password$/],
			['/settings?tab=auth#current-password', /\/account#password$/],
			['/settings?tab=auth#auth-panel-totp', /\/account#two-factor$/],
			['/security-audit?tab=sessions', /\/account#sessions$/],
			['/api-tokens', /\/account#api-tokens$/]
		] as const) {
			await page.goto(`${UI}${from}`);
			await expect(page, from).toHaveURL(to);
			await expect(page.locator('.is-found'), from).toBeVisible();
		}
	});

	test('Settings › Sign-in no longer has your own password', async ({ page }) => {
		await page.goto(`${UI}/settings?tab=auth`);
		await expect(page.locator('#require-two-factor')).toBeVisible();
		await expect(page.locator('#current-password')).toHaveCount(0);
		await expect(page.locator('#auth-panel-totp')).toHaveCount(0);
	});

	test('the top bar search finds the password and lands on it, outlined', async ({ page }) => {
		await page.goto(`${UI}/devices`);
		await expect(page.locator('.search-trigger')).toBeVisible();
		await page.keyboard.press('Control+k');
		const box = page.getByRole('combobox');
		await expect(box).toBeFocused();
		await box.fill('new password');
		const option = page
			.getByRole('group', { name: /^Settings/ })
			.getByRole('option', { name: /New password/ });
		await expect(option.first()).toContainText('Your account, Password');
		await option.first().click();
		await expect(page).toHaveURL(/\/account#new-password$/);
		await expect(page.locator('#new-password')).toBeInViewport();
		await expect(page.locator('.is-found')).toBeVisible();
	});

	test('Preferences change the theme of this browser', async ({ page }) => {
		await page.goto(`${UI}/account#preferences`);
		const prefs = page.locator('#preferences');
		await prefs.getByRole('radio', { name: 'Dark' }).check();
		await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
		await prefs.getByRole('radio', { name: 'Light' }).check();
		await expect(page.locator('html')).toHaveAttribute('data-theme', 'light');
		await prefs.getByRole('radio', { name: 'Console default' }).check();
	});
});

test.describe('as a viewer', () => {
	test.use({ role: 'viewer' });

	test('Your account without API tokens; Settings › Sign-in is not in the sidebar', async ({ page }) => {
		await page.goto(`${UI}/account`);
		await expect(page.locator('#password')).toBeVisible();
		await expect(page.locator('#two-factor')).toBeVisible();
		await expect(page.locator('#api-tokens')).toHaveCount(0);
		await expect(page.locator('nav a[href$="/settings?tab=auth"]')).toHaveCount(0);
	});
});
