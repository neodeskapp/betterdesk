import { test, expect } from './fixtures.ts';
import type { Page } from '@playwright/test';
import { UI } from './roles.ts';

/**
 * Controls that sit side by side share their height, lists line up their
 * counts, and long text in a tile ends in an ellipsis instead of running
 * out of it. Measured at a desktop width with a mouse.
 */

test.use({ viewport: { width: 1440, height: 900 } });

const box = async (page: Page, selector: string) => {
	const b = await page.locator(selector).first().boundingBox();
	if (!b) throw new Error(`${selector} is not on the page`);
	return b;
};

test('the chat search and its New button have the same height', async ({ page }) => {
	await page.goto(`${UI}/chat`);
	const field = await box(page, '.chat-find input');
	const button = await box(page, '.chat-find button');
	expect(Math.abs(field.height - button.height)).toBeLessThanOrEqual(1);
	expect(Math.abs(field.y + field.height / 2 - (button.y + button.height / 2))).toBeLessThanOrEqual(1);
});

test('folder and group entries fill their row and end their counts under the heading button', async ({ page }) => {
	await page.goto(`${UI}/devices`);
	const scopes = page.locator('nav.scopes');
	await expect(scopes.locator('.chip-count').first()).toBeVisible();
	const result = await scopes.evaluate((nav) => {
		const out: string[] = [];
		for (const block of nav.querySelectorAll('.filter-block')) {
			const head = block.querySelector('.filter-head')!.getBoundingClientRect();
			// The heading button's label ends at its end padding
			const addButton = block.querySelector('.filter-add')!;
			const add = addButton.getBoundingClientRect();
			const addPad = parseFloat(getComputedStyle(addButton).paddingInlineEnd);
			for (const option of block.querySelectorAll<HTMLElement>('.filter-option')) {
				const r = option.getBoundingClientRect();
				if (!r.width) continue;
				if (Math.abs(r.right - head.right) > 1) out.push(`entry ends at ${r.right}, row at ${head.right}`);
				const count = option.querySelector('.chip-count')!.getBoundingClientRect();
				if (Math.abs(count.right - (add.right - addPad)) > 1)
					out.push(`count ends at ${count.right}, heading button text at ${add.right - addPad}`);
			}
		}
		return out;
	});
	expect(result).toEqual([]);
});

test('a long system name ends in an ellipsis inside its device tile', async ({ page }) => {
	await page.goto(`${UI}/remote`);
	await page.getByRole('button', { name: /^(Tiles|Grid)$/ }).click();
	const tile = page.locator('.book-grid .card').first();
	await expect(tile).toBeVisible();
	// The seed's names are short: give the first tile a long one, as real Windows builds have
	const fits = await tile.evaluate((card) => {
		const name = card.querySelector<HTMLElement>('.platform-name')!;
		name.textContent = 'windows / Windows 11 Pro - 11 (26200) Enterprise LTSC with a long build name';
		const c = card.getBoundingClientRect();
		const n = name.getBoundingClientRect();
		return { inside: n.right <= c.right, ellipsis: getComputedStyle(name).textOverflow === 'ellipsis' };
	});
	expect(fits).toEqual({ inside: true, ellipsis: true });
});
