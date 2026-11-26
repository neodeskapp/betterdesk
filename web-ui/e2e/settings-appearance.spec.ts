/**
 * Settings › Appearance and Settings › Branding on the settings pattern
 * in a real console: one save bar that names each change and Discards
 * it, the filter and Advanced, the question before leaving with unsaved
 * changes, and a phone width without sideways scrolling. Nothing is saved.
 */
import type { Page } from '@playwright/test';
import { test, expect, expectNoAxeViolations } from './fixtures.ts';
import { UI } from './roles.ts';

test.use({ role: 'super_admin' });

async function open(page: Page, tab: 'branding' | 'brand') {
	await page.goto(`${UI}/settings?tab=${tab}`);
	await expect(page.locator('.branding-form[aria-busy="false"]')).toBeVisible({ timeout: 20_000 });
}

test('Appearance: a change shows in the save bar by name, Discard puts it back', async ({ page }) => {
	await open(page, 'branding');
	const theme = page.getByRole('radiogroup', { name: 'Default theme' });
	const before = await theme.getByRole('radio', { checked: true }).getAttribute('value');
	const other = before === 'dark' ? 'Light' : 'Dark';
	await theme.getByRole('radio', { name: new RegExp(`^${other}`) }).check();
	const bar = page.getByRole('region', { name: 'Unsaved changes' });
	await expect(bar).toContainText(`Default theme:`);
	await expect(bar).toContainText(other);
	await expectNoAxeViolations(page);
	await bar.getByRole('button', { name: 'Discard' }).click();
	await expect(bar).toHaveCount(0);
	await expect(theme.locator(`input[value="${before}"]`)).toBeChecked();
});

test('Appearance: the filter finds custom CSS behind Advanced', async ({ page }) => {
	await open(page, 'branding');
	await page.getByRole('searchbox', { name: 'Filter appearance settings' }).fill('custom css');
	await expect(page.locator('#custom-css-control')).toBeVisible();
	await expect(page.locator('#font-heading-search')).toBeHidden();
	await expect(page.getByText(/1 setting matches/)).toBeVisible();
});

test('Branding: leaving with an unsaved change asks first', async ({ page }) => {
	await open(page, 'brand');
	const name = page.getByRole('textbox', { name: 'Application name' });
	const before = await name.inputValue();
	await name.fill(`${before} test`);
	await expect(page.getByRole('region', { name: 'Unsaved changes' })).toContainText('Application name');
	// To Appearance through the settings search: the sidebar may hide it under "Show more"
	const search = page.getByRole('searchbox', { name: 'Search all settings' });
	await search.fill('custom css');
	await search.press('Enter');
	const dialog = page.getByRole('dialog');
	await expect(dialog).toBeVisible();
	await dialog.getByRole('button', { name: 'Cancel' }).click();
	await expect(name).toHaveValue(`${before} test`);
});

for (const tab of ['branding', 'brand'] as const) {
	test(`${tab} at phone width: one column, the console preview apart from the client app's, no sideways scrolling`, async ({
		page
	}) => {
		await page.setViewportSize({ width: 390, height: 844 });
		await open(page, tab);
		const wide = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
		expect(wide).toBeLessThanOrEqual(0);
		// The console preview comes before the groups; at the end it would sit
		// right after the client app's own preview, two previews back to back
		const preview = await page.getByRole('complementary', { name: 'Console preview' }).boundingBox();
		const firstGroup = await page.locator('.settings-group').first().boundingBox();
		expect(preview!.y).toBeLessThan(firstGroup!.y);
		await expectNoAxeViolations(page);
	});
}
