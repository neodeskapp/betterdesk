/**
 * The console on phones and tablets (touch, as the small-screen pass of
 * 3.6.0-rc.2 ran it), in English and German: nothing is wider than the
 * screen, so no page pans sideways; menus, toasts and the rows around them
 * don't cover each other; text stays in its column, row or table.
 *
 * Firefox has no mobile viewport (isMobile); it runs the same sizes with
 * touch only.
 */
import type { Page } from '@playwright/test';
import { test, expect } from './fixtures.ts';
import { UI } from './roles.ts';

const SIZES = [
	{ width: 360, height: 640 },
	{ width: 390, height: 844 },
	{ width: 768, height: 1024 }
];
const LANGS = ['en', 'de'] as const;

/** A sample of pages: the shell is the same on all of them, the content differs */
const PAGES = [
	'/',
	'/devices',
	'/users',
	'/help-requests',
	'/settings?tab=auth',
	'/settings?tab=email',
	'/keys',
	'/security-audit',
	'/automation',
	'/commercialization?tab=packages'
];

test.use({
	role: 'super_admin',
	hasTouch: true,
	isMobile: async ({ browserName }, use) => use(browserName === 'chromium')
});

/** Switch the console language for this browser context only (the account keeps its own) */
async function useLanguage(page: Page, lang: string, baseURL: string | undefined): Promise<void> {
	const url = new URL(baseURL ?? 'http://localhost');
	await page.context().addCookies([{ name: 'neodesk_lang', value: lang, domain: url.hostname, path: '/' }]);
}

async function open(page: Page, path: string, lang: string): Promise<void> {
	await page.goto(`${UI}${path}`);
	await expect(page.locator('h1').first()).toBeVisible({ timeout: 15_000 });
	await expect(page.locator('html')).toHaveAttribute('lang', lang);
	await page.waitForLoadState('networkidle');
}

/**
 * How far the page reaches past the right edge of the screen, and the
 * elements that stick out (not counting ones a scrolling box clips)
 */
async function pastTheEdge(page: Page): Promise<{ over: number; elements: string[] }> {
	return page.evaluate(() => {
		const vw = document.documentElement.clientWidth;
		const clippedByScroller = (el: Element) => {
			for (let p = el.parentElement; p && p !== document.body; p = p.parentElement) {
				if (/(auto|scroll|hidden|clip)/.test(getComputedStyle(p).overflowX)) {
					const r = p.getBoundingClientRect();
					if (r.right <= vw + 1) return true;
				}
			}
			return false;
		};
		const found: Element[] = [];
		for (const el of document.querySelectorAll('body *')) {
			const r = el.getBoundingClientRect();
			if (!r.width || !r.height || r.right <= vw + 1) continue;
			if (getComputedStyle(el).visibility === 'hidden' || clippedByScroller(el)) continue;
			// Only the outermost element that sticks out
			if (found.some((f) => f.contains(el))) continue;
			found.push(el);
		}
		const describe = (el: Element) => {
			const cls = typeof el.className === 'string' ? el.className.trim().split(/\s+/)[0] : '';
			const past = Math.round(el.getBoundingClientRect().right - vw);
			return `${el.tagName.toLowerCase()}${cls ? '.' + cls : ''} (${past} px)`;
		};
		return { over: document.documentElement.scrollWidth - vw, elements: found.slice(0, 10).map(describe) };
	});
}

for (const lang of LANGS) {
	for (const size of SIZES) {
		test.describe(`${lang} at ${size.width}x${size.height}`, () => {
			test.use({ viewport: size });

			// BUGS L316: the top bar was wider than the screen on every page
			test('no page is wider than the screen', async ({ page, baseURL }) => {
				test.setTimeout(120_000);
				await useLanguage(page, lang, baseURL);
				const wide: string[] = [];
				for (const path of PAGES) {
					await open(page, path, lang);
					const { over, elements } = await pastTheEdge(page);
					if (over > 0 || elements.length) wide.push(`${path}: ${over} px; ${elements.join(', ')}`);
				}
				expect(wide, 'pages wider than the screen').toEqual([]);
			});
		});
	}
}

/**
 * Whether a click on each item of the open menu lands on the menu: its
 * start, middle and end, on the item's middle line
 */
async function menuCovered(page: Page): Promise<string[]> {
	const menu = page.getByRole('menu');
	await expect(menu).toBeVisible();
	return menu.evaluate((m) => {
		const hidden: string[] = [];
		for (const item of m.querySelectorAll<HTMLElement>('[role="menuitem"]')) {
			const r = item.getBoundingClientRect();
			for (const x of [r.left + 4, r.left + r.width / 2, r.right - 4]) {
				const hit = document.elementFromPoint(x, r.top + r.height / 2);
				if (!hit || !m.contains(hit))
					hidden.push(
						`${item.textContent?.trim()} at ${Math.round(x - r.left)} px: ${hit?.className ?? 'nothing'}`
					);
			}
		}
		return hidden;
	});
}

for (const size of [
	{ width: 360, height: 640 },
	{ width: 1440, height: 900 }
]) {
	test.describe(`users at ${size.width}`, () => {
		test.use({ viewport: size });

		// BUGS L317: the sticky action cells of the rows below drew over the open menu
		test('a row menu is above the rows below it', async ({ page }) => {
			await open(page, '/users', 'en');
			const kebabs = page.locator('td.actions button[aria-haspopup="menu"]');
			await expect(kebabs.nth(2)).toBeVisible();
			await kebabs.first().click();
			expect(await menuCovered(page), 'menu items covered by other rows').toEqual([]);
		});
	});
}
