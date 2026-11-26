/**
 * A first-time admin's task: "turn off sound for all sessions", by
 * keyboard alone. Before the settings rework the search found nothing for
 * "sound" and the Policies page let the admin untick a box that no app
 * applies. Now the search leads to the policy setting, and its group says
 * that no NeoDesk app applies it yet and where sound can be turned off
 * today (an access rule under Devices). With several organizations the
 * page first asks which one, and keeps the setting in the address.
 */
import { test, expect } from './fixtures.ts';
import { UI } from './roles.ts';

test.use({ role: 'super_admin' });

const NOT_APPLIED = 'No NeoDesk app applies this group yet. It is saved and handed to devices that ask.';

test('the search finds the sound setting and its group says where sound is turned off today', async ({
	page
}) => {
	await page.goto(`${UI}/`);
	await expect(page.locator('.search-trigger')).toBeVisible();
	await page.keyboard.press('Control+k');
	const box = page.getByRole('combobox');
	await expect(box).toBeFocused();
	await box.fill('sound');
	const option = page.getByRole('option', { name: /Allow sound from the device/ }).first();
	await expect(option).toContainText('Policies');
	const id = await option.getAttribute('id');
	for (let i = 0; i < 20 && (await box.getAttribute('aria-activedescendant')) !== id; i++)
		await page.keyboard.press('ArrowDown');
	await expect(box).toHaveAttribute('aria-activedescendant', id ?? '');
	await page.keyboard.press('Enter');

	await expect(page).toHaveURL(/\/policies(\/[^#]*)?#pol-features-allow-audio$/, { timeout: 30_000 });
	// Policies belong to one organization: with several, the page asks which first and keeps the setting
	const firstOrg = page.locator('a[href*="/policies/"][href$="#pol-features-allow-audio"]').first();
	if (await firstOrg.isVisible({ timeout: 10_000 }).catch(() => false)) {
		for (let i = 0; i < 100 && !(await firstOrg.evaluate((el) => el === document.activeElement)); i++)
			await page.keyboard.press('Tab');
		await expect(firstOrg).toBeFocused();
		await page.keyboard.press('Enter');
	}
	const row = page.locator('#pol-features-allow-audio');
	await expect(row).toBeVisible({ timeout: 30_000 });
	await expect(row).toBeInViewport();

	const features = page.locator('section', {
		has: page.getByRole('heading', { level: 2, name: 'Features' })
	});
	await expect(features.getByText(NOT_APPLIED)).toBeVisible();
	await expect(features.getByText(/To turn sound off in sessions today/)).toBeVisible();

	// The link to where it works today is reachable by keyboard and opens Devices
	const link = features.getByRole('link', { name: 'Open access rules' });
	for (let i = 0; i < 250 && !(await link.evaluate((el) => el === document.activeElement)); i++)
		await page.keyboard.press('Tab');
	await expect(link).toBeFocused();
	await page.keyboard.press('Enter');
	await expect(page).toHaveURL(/\/devices/);
});
