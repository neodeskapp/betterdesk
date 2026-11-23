/**
 * The console on phones and tablets (touch, as the small-screen pass of
 * 3.6.0-rc.2 ran it), in English and German: nothing is wider than the
 * screen, so no page pans sideways.
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
