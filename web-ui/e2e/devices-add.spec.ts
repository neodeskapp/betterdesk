import { test, expect } from './fixtures.ts';
import { UI } from './roles.ts';

/**
 * Devices › Add computers (/devices/add): every way a computer gets in,
 * as tabs for the accounts that may use them; the old pages lead to their
 * tab, Enter a pairing code opens its dialog there, and the sidebar has
 * one entry instead of five.
 */

const tabs = (page: import('@playwright/test').Page) => page.getByRole('tablist').getByRole('tab');

test.describe('as a super admin', () => {
	test.use({ role: 'super_admin' });

	test('one entry under Devices, six tabs, the Add a computer button stays on All devices', async ({
		page
	}) => {
		await page.goto(`${UI}/devices`);
		const nav = page.locator('.shell-sidebar');
		await expect(nav.getByRole('link', { name: 'Add computers' })).toHaveAttribute('href', /\/devices\/add$/);
		for (const gone of ['/keys', '/tokens', '/pairing', '/agents', '/?activateAgent=1'])
			await expect(nav.locator(`a[href$="${gone}"]`), gone).toHaveCount(0);
		await expect(page.locator('main').getByRole('button', { name: 'Add a computer' })).toBeVisible();

		await nav.getByRole('link', { name: 'Add computers' }).click();
		await expect(page.getByRole('heading', { level: 1, name: 'Add computers' })).toBeVisible();
		await expect(tabs(page)).toHaveText([
			'Invites and install links',
			'Device tokens',
			'Pairing codes',
			'Support agents',
			'Receiver app',
			'Manual setup'
		]);
		await tabs(page).filter({ hasText: 'Device tokens' }).click();
		await expect(page).toHaveURL(/\/devices\/add\?tab=tokens$/);
		await expect(page.locator('.tokens-page')).toBeVisible();
	});

	test('Receiver app: the silent install appears once a token is entered', async ({ page }) => {
		await page.goto(`${UI}/devices/add?tab=receiver`);
		await expect(page.locator('.receiver-page')).toBeVisible();
		await expect(page.locator('.receiver-props')).toContainText('SERVER');
		await page.locator('.receiver-token').fill('0123456789abcdef0123456789abcdef');
		await expect(page.locator('.receiver-props')).toContainText('TOKEN');
	});

	test('the old addresses open their tab', async ({ page }) => {
		for (const [from, tab, body] of [
			['/keys', 'manual', '.keys-page'],
			['/tokens', 'tokens', '.tokens-page'],
			['/pairing', 'pairing', '.pairing-page'],
			['/agents', 'agents', '.agents-page'],
			['/generator', 'agents', '.agents-page']
		] as const) {
			await page.goto(`${UI}${from}`);
			await expect(page, from).toHaveURL(new RegExp(`/devices/add\\?tab=${tab}$`));
			await expect(page.locator(body), from).toBeVisible();
		}
	});

	test('Enter a pairing code opens Pair with code on the Pairing codes tab', async ({ page }) => {
		await page.goto(`${UI}/?activateAgent=1`);
		await expect(page).toHaveURL(/\/devices\/add\?tab=pairing/);
		await expect(page.getByRole('dialog', { name: 'Pairing code' })).toBeVisible();
		await expect(tabs(page).filter({ hasText: 'Pairing codes' })).toHaveAttribute('aria-selected', 'true');
	});
});

test.describe('as a viewer', () => {
	test.use({ role: 'viewer' });

	test('only Manual setup, also when another tab is asked for', async ({ page }) => {
		await page.goto(`${UI}/devices/add?tab=tokens`);
		await expect(tabs(page)).toHaveText(['Manual setup']);
		await expect(page).toHaveURL(/\/devices\/add\?tab=manual$/);
		await expect(page.locator('.keys-page')).toBeVisible();
	});
});
