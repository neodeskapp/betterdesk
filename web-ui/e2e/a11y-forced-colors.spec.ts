/**
 * Windows contrast themes (forced colours) replace backgrounds, tints and
 * shadows with system colours. A selected filter, the current page, the
 * selected tab, the current navigation item and a chosen option must
 * still look different from the others there (WCAG 1.4.1, 1.4.11).
 * Compares what is left of the look of a selected and an unselected item.
 */
import type { Locator, Page } from '@playwright/test';
import { test, expect } from './fixtures.ts';
import { UI } from './roles.ts';

/** The parts of an item's look that forced colours keep */
async function look(item: Locator): Promise<string> {
	return item.evaluate((el) => {
		/* Chrome keeps the alpha of an author colour in forced colours: a
		   15 % tint becomes 15 % of the system colour, which nobody sees.
		   A colour counts only when it is mostly opaque. */
		const canvas = getComputedStyle(document.body).backgroundColor;
		const seenColor = (c: string) => {
			const m = /rgba?\(([^)]+)\)/.exec(c);
			const alpha = m ? Number(m[1]!.split(/[ ,/]+/)[3] ?? 1) : 1;
			return alpha >= 0.5 ? c : 'none';
		};
		// A background the colour of the page is no background
		const seen = (c: string) => (seenColor(c) === canvas ? 'none' : seenColor(c));
		const border = (cs: CSSStyleDeclaration, side: string) => {
			const width = parseFloat(cs.getPropertyValue(`border-${side}-width`));
			const style = cs.getPropertyValue(`border-${side}-style`);
			const color = seenColor(cs.getPropertyValue(`border-${side}-color`));
			return width > 0 && style !== 'none' && color !== 'none' ? `${width}px ${style} ${color}` : 'none';
		};
		const cs = getComputedStyle(el);
		const before = getComputedStyle(el, '::before');
		const after = getComputedStyle(el, '::after');
		const pseudo = (p: CSSStyleDeclaration) =>
			p.content === 'none'
				? 'none'
				: [seen(p.backgroundColor), ...['top', 'bottom'].map((s) => border(p, s))].join(' ');
		return JSON.stringify([
			seen(cs.backgroundColor),
			cs.color,
			cs.fontWeight,
			cs.textDecorationLine,
			['top', 'right', 'bottom', 'left'].map((s) => border(cs, s)),
			cs.outlineStyle === 'none' ? '' : `${cs.outlineWidth} ${cs.outlineColor}`,
			pseudo(before),
			pseudo(after)
		]);
	});
}

async function forced(page: Page, width = 1280) {
	await page.setViewportSize({ width, height: 900 });
	await page.emulateMedia({ forcedColors: 'active', colorScheme: 'dark' });
}

async function expectDifferent(selected: Locator, other: Locator) {
	await expect(selected).toBeVisible({ timeout: 15_000 });
	await expect(other).toBeVisible();
	// Move the pointer away so hover doesn't count
	await selected.page().mouse.move(0, 0);
	expect(await look(selected), 'selected and unselected look the same in forced colours').not.toBe(
		await look(other)
	);
}

test.describe('selection is visible in forced colours', () => {
	test('filter buttons', async ({ page }) => {
		await forced(page);
		await page.goto(`${UI}/devices`);
		const pressed = page.locator('#main-content .filter-btn[aria-pressed="true"]').first();
		const other = page.locator('#main-content .filter-btn[aria-pressed="false"]').first();
		await expectDifferent(pressed, other);
	});

	test('tabs', async ({ page }) => {
		await forced(page);
		await page.goto(`${UI}/devices/add`);
		await expectDifferent(
			page.getByRole('tab', { selected: true }).first(),
			page.getByRole('tab', { selected: false }).first()
		);
	});

	test('the current item of the navigation', async ({ page }) => {
		await forced(page);
		await page.goto(`${UI}/devices`);
		const nav = page.getByRole('navigation', { name: /main/i }).first();
		await expectDifferent(
			nav.locator('a[aria-current="page"]').first(),
			nav.locator('a:not([aria-current])').first()
		);
	});

	test('a chosen option in a choice list', async ({ page }) => {
		await forced(page);
		// Settings › General: how clients connect
		await page.goto(`${UI}/settings`);
		const chosen = page.locator('.choice:has(input:checked)').first();
		const other = page.locator('.choice:not(:has(input:checked))').first();
		await expectDifferent(chosen, other);
	});

	test('a chosen choice of side-by-side choices', async ({ page }) => {
		await forced(page);
		// Your account › Preferences: the theme
		await page.goto(`${UI}/account#preferences`);
		const chosen = page.locator('.segment:has(input:checked) label').first();
		const other = page.locator('.segment:not(:has(input:checked)) label').first();
		await expectDifferent(chosen, other);
	});

	test('the current page of a pager', async ({ page }) => {
		await forced(page);
		await page.goto(`${UI}/security-audit`);
		const current = page.locator('.pagination-btn[aria-current="page"]').first();
		test.skip(!(await current.isVisible().catch(() => false)), 'the audit log has one page here');
		await expectDifferent(
			current,
			page.locator('.pagination-btn:not([aria-current]):not(:disabled)').first()
		);
	});
});
