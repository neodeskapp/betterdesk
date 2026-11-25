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
	'/billing?tab=packages'
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

/** Status lines cut by a box around them or by the screen edge */
async function cutStatuses(page: Page): Promise<string[]> {
	return page.evaluate(() => {
		const vw = document.documentElement.clientWidth;
		const cut: string[] = [];
		for (const s of document.querySelectorAll<HTMLElement>('.status')) {
			const r = s.getBoundingClientRect();
			if (!r.width) continue;
			let right = vw;
			for (let p = s.parentElement; p && p !== document.body; p = p.parentElement) {
				if (getComputedStyle(p).overflowX !== 'visible')
					right = Math.min(right, p.getBoundingClientRect().right);
			}
			if (r.right > right + 1) cut.push(`${s.textContent?.trim()} (${Math.round(r.right - right)} px)`);
		}
		return cut;
	});
}

test.describe('quick support on phones', () => {
	test.use({ viewport: { width: 360, height: 640 } });

	// BUGS L318: the waiting line never wrapped; the dialog grew to 627 px and the row to 628 px
	test('the waiting line wraps: the dialog and the Help requests row fit the screen', async ({ page }) => {
		for (const size of [
			{ width: 360, height: 640 },
			{ width: 390, height: 844 }
		]) {
			await page.setViewportSize(size);
			await open(page, '/help-requests', 'en');
			await page.locator('.shell-topbar').getByRole('button', { name: 'Quick support' }).click();
			const dialog = page.getByRole('dialog');
			await expect(dialog.locator('.status').filter({ hasText: 'Waiting for the person' })).toBeVisible();
			expect(await pastTheEdge(page), `Quick support dialog at ${size.width}`).toEqual({
				over: 0,
				elements: []
			});
			expect(await cutStatuses(page)).toEqual([]);
			await dialog.getByRole('button', { name: 'Close', exact: true }).last().click();
			await expect(dialog).toBeHidden();
			const row = page.locator('.qs-sessions .list-row').first();
			await expect(row).toBeVisible();
			// The person's name keeps its line instead of one word per line
			const name = await row.locator('.row-main').evaluate((m) => m.getBoundingClientRect().width);
			expect(name, 'width of the name').toBeGreaterThan(150);
			expect(await pastTheEdge(page), `Help requests at ${size.width}`).toEqual({ over: 0, elements: [] });
			expect(await cutStatuses(page)).toEqual([]);
			await row.getByRole('button', { name: 'End session' }).click();
			await expect(page.locator('.qs-sessions')).toHaveCount(0);
		}
	});
});

test.describe('email settings in German at 360', () => {
	test.use({ viewport: { width: 360, height: 640 } });

	// BUGS L318: "Nicht eingerichtet. Die Konsole versendet keine E-M" was cut at the edge
	test('the current state line is not cut', async ({ page, baseURL }) => {
		await useLanguage(page, 'de', baseURL);
		await open(page, '/settings?tab=email', 'de');
		await expect(page.locator('.status').first()).toBeVisible();
		expect(await cutStatuses(page)).toEqual([]);
	});
});

test.describe('the copied toast', () => {
	test.beforeEach(async ({ context, browserName }) => {
		if (browserName === 'chromium') await context.grantPermissions(['clipboard-read', 'clipboard-write']);
	});

	// BUGS L329: "Copied to clipboard" covered the menu opened after it, and the sheet's Close button
	test('at 360 a menu opened while it shows is above it', async ({ page }) => {
		await page.setViewportSize({ width: 360, height: 640 });
		await open(page, '/', 'en');
		await page.locator('main').getByRole('button', { name: 'Copy all' }).click();
		await expect(page.locator('.toast')).toBeVisible();
		// Where the small-screen pass had it: low on the screen, so the menu opens towards the toast
		const more = page.locator('main').getByRole('button', { name: 'More', exact: true });
		await more.evaluate((b) => window.scrollBy(0, b.getBoundingClientRect().top - 430));
		await more.click();
		expect(await menuCovered(page), 'menu items covered by the toast').toEqual([]);
	});

	for (const size of [
		{ width: 768, height: 1024 },
		{ width: 1024, height: 768 }
	]) {
		test(`at ${size.width} it does not cover the sheet it was copied from`, async ({ page }) => {
			await page.setViewportSize(size);
			await open(page, '/', 'en');
			await page.locator('main').getByRole('button', { name: 'More', exact: true }).click();
			await page.getByRole('menuitem', { name: /Windows install script/ }).click();
			const sheet = page.getByRole('dialog');
			await sheet.getByRole('button', { name: 'Copy' }).click();
			await expect(page.locator('.toast')).toHaveCount(1);
			const close = sheet.getByRole('button', { name: 'Close' }).last();
			const covered = await close.evaluate((b) => {
				const r = b.getBoundingClientRect();
				const points = [
					[r.left + 4, r.top + 4],
					[r.left + r.width / 2, r.top + r.height / 2],
					[r.right - 4, r.bottom - 4]
				];
				return points.filter(([x, y]) => {
					const hit = document.elementFromPoint(x!, y!);
					return !hit || !b.contains(hit);
				}).length;
			});
			expect(covered, 'points of the Close button that something else covers').toBe(0);
		});
	}
});

test.describe('empty tables at 360', () => {
	test.use({ viewport: { width: 360, height: 640 } });

	// BUGS L319: the sentence sat in a cell as wide as the table, mostly off the screen
	test('the empty-table sentence wraps inside the visible part of the table', async ({ page }) => {
		const cut: string[] = [];
		let seen = 0;
		for (const path of ['/automation', '/billing?tab=packages', '/dataguard', '/fleet', '/activity']) {
			await open(page, path, 'en');
			const cells = await page.evaluate(() =>
				[...document.querySelectorAll<HTMLElement>('.table-scroll td.state-cell')].map((td) => {
					const box = td.closest('.table-scroll')!.getBoundingClientRect();
					const text = document.createRange();
					text.selectNodeContents(td);
					const r = text.getBoundingClientRect();
					return { text: td.textContent!.trim(), past: Math.round(r.right - box.right) };
				})
			);
			seen += cells.length;
			cut.push(...cells.filter((c) => c.past > 0).map((c) => `${path}: "${c.text}" ${c.past} px`));
		}
		expect(seen, 'empty tables on these pages').toBeGreaterThan(0);
		expect(cut, 'empty-table sentences past the visible part of their table').toEqual([]);
	});
});

for (const size of [
	{ width: 768, height: 1024 },
	{ width: 1024, height: 768 }
]) {
	test.describe(`device table at ${size.width}`, () => {
		test.use({ viewport: size });

		// BUGS L320: a long display name ran out of its column over Platform
		test('a long device name stays in its column', async ({ page }) => {
			await page.route(/\/api\/devices(\?|$)/, async (route) => {
				const response = await route.fetch();
				const body = await response.json();
				const list = (body.devices ?? body.data?.devices) as Record<string, unknown>[];
				Object.assign(list[0]!, {
					display_name: 'CEO MacBook Pro 16 inch with a long display name',
					platform: 'Windows 10 Enterprise'
				});
				await route.fulfill({ response, json: body });
			});
			await open(page, '/devices', 'en');
			await expect(page.getByText('CEO MacBook Pro 16 inch with a long display name')).toBeVisible();
			const spill = await page.evaluate(() =>
				[...document.querySelectorAll<HTMLElement>('.devices-table tbody tr[data-key]')].flatMap((tr) => {
					const cell = tr.querySelector<HTMLElement>('.col-name');
					const name = cell?.querySelector<HTMLElement>('.device-name');
					if (!cell || !name) return [];
					const text = document.createRange();
					text.selectNodeContents(name);
					const past = text.getBoundingClientRect().right - cell.getBoundingClientRect().right;
					return past > 1 ? [`${name.textContent} ${Math.round(past)} px`] : [];
				})
			);
			expect(spill, 'names past the end of their column').toEqual([]);
		});
	});
}

test.describe('list rows in German at 360', () => {
	test.use({ viewport: { width: 360, height: 640 } });

	// BUGS L321: "Schlüsseldatei herunterladen" covered the row's text, which was one word per line
	test('a long button at the end of a row neither covers nor squeezes the text', async ({
		page,
		baseURL
	}) => {
		await useLanguage(page, 'de', baseURL);
		const bad: string[] = [];
		for (const path of ['/keys', '/permissions', '/settings?tab=branding']) {
			await open(page, path, 'de');
			bad.push(
				...(await page.evaluate(() =>
					[...document.querySelectorAll<HTMLElement>('li.list-row')].flatMap((row) => {
						const main = row.querySelector<HTMLElement>('.row-main');
						const end = row.querySelector<HTMLElement>('.row-end, .row-actions');
						if (!main || !end || !end.getBoundingClientRect().width) return [];
						const text = document.createRange();
						text.selectNodeContents(main);
						const t = text.getBoundingClientRect();
						const e = end.getBoundingClientRect();
						const title = main.querySelector('.row-title')?.textContent?.trim() ?? '';
						const overlap =
							t.right > e.left + 1 && t.left < e.right - 1 && t.bottom > e.top + 1 && t.top < e.bottom - 1;
						const squeezed = main.getBoundingClientRect().width < 160;
						return overlap || squeezed
							? [`${location.pathname}: ${title}${overlap ? ' covered' : ''}${squeezed ? ' squeezed' : ''}`]
							: [];
					})
				))
			);
		}
		expect(bad, 'rows whose text is covered or squeezed').toEqual([]);
	});
});

test.describe('security checks in German at 360', () => {
	test.use({ viewport: { width: 360, height: 640 } });

	// BUGS L322: "Was zu tun" was clipped at the right edge mid-word
	test('the checks table fits the screen', async ({ page, baseURL }) => {
		await useLanguage(page, 'de', baseURL);
		await open(page, '/security-checks', 'de');
		const box = page.locator('.checks .table-scroll');
		await expect(box.locator('tbody tr').first()).toBeVisible();
		const { scroll, client } = await box.evaluate((el) => ({
			scroll: el.scrollWidth,
			client: el.clientWidth
		}));
		expect(scroll, 'width of the checks table in its box').toBeLessThanOrEqual(client);
	});
});

test.describe('skip link', () => {
	// BUGS L324: the hidden link's shadow showed as a gray smudge at the top edge
	test('neither the hidden link nor its shadow reaches into the screen', async ({ page }) => {
		await open(page, '/', 'en');
		const reach = await page.locator('.skip-link').evaluate((el) => {
			const r = el.getBoundingClientRect();
			// Each shadow: x, y, blur and spread in px; the lowest point it paints
			const shadows = getComputedStyle(el).boxShadow;
			let below = 0;
			if (shadows !== 'none') {
				for (const s of shadows.split(/,(?![^(]*\))/)) {
					const px = (s.replace(/rgba?\([^)]*\)/, '').match(/-?[\d.]+px/g) ?? []).map(parseFloat);
					const [, y = 0, blur = 0, spread = 0] = px;
					below = Math.max(below, y + blur + spread);
				}
			}
			return r.bottom + below;
		});
		expect(reach, 'lowest point of the skip link and its shadow').toBeLessThanOrEqual(0);
	});
});

test.describe('top bar on a narrow phone', () => {
	test.use({ viewport: { width: 360, height: 640 } });

	// Small-screen pass, usability 1: the breadcrumb was cut to one letter ("U...")
	test('shows no cut page name; screen readers still have it', async ({ page }) => {
		await open(page, '/users', 'en');
		const crumb = page.locator('.shell-breadcrumb [aria-current="page"]');
		await expect(crumb).toHaveText('Users');
		const width = await crumb.evaluate((el) => el.getBoundingClientRect().width);
		expect(width, 'visible width of the page name').toBeLessThanOrEqual(1);
		await page.setViewportSize({ width: 768, height: 1024 });
		await expect(crumb).toBeVisible();
		expect(await crumb.evaluate((el) => el.getBoundingClientRect().width)).toBeGreaterThan(20);
	});
});

test.describe('tab strips on a phone', () => {
	test.use({ viewport: { width: 360, height: 640 } });

	// Small-screen pass, usability 3: a page opened on a later tab showed a strip with no tab selected
	test('the open tab is in view when the page opens on it', async ({ page }) => {
		await open(page, '/security-audit?tab=events', 'en');
		const strip = page.getByRole('tablist').first();
		const tab = strip.getByRole('tab', { selected: true });
		await expect(tab).toBeVisible();
		const [s, t] = await Promise.all([strip.boundingBox(), tab.boundingBox()]);
		expect(t!.x, 'start of the open tab').toBeGreaterThanOrEqual(s!.x);
		expect(t!.x + t!.width, 'end of the open tab').toBeLessThanOrEqual(s!.x + s!.width);
	});
});

test.describe('chat on a small phone', () => {
	test.use({ viewport: { width: 360, height: 640 } });

	// Small-screen pass, usability 5: the contact list showed two or three rows in a short box
	test('the contact list takes the page height instead of a short box', async ({ page }) => {
		await open(page, '/chat', 'en');
		const panel = page.locator('.chat-tabs [role="tabpanel"]');
		await expect(panel.locator('li').nth(3)).toBeAttached();
		const { scroll, client } = await panel.evaluate((el) => ({
			scroll: el.scrollHeight,
			client: el.clientHeight
		}));
		expect(scroll, 'height of the contacts in their box').toBeLessThanOrEqual(client + 1);
	});
});
