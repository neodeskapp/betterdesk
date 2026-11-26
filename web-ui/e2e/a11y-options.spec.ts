/**
 * The accessibility options in the running console: the browser's own
 * font size, More contrast with the system and under Windows contrast
 * themes, Readable font outside the main area, Pause live updates on a
 * page without its own line, saving for the account, the panel on the
 * sign-in page, and a focus ring that reaches 3:1 in every palette.
 *
 * Tests that change the account's settings put the defaults back.
 */
import type { Page } from '@playwright/test';
import { test, expect } from './fixtures.ts';
import { UI } from './roles.ts';

const KEY = 'neodesk_accessibility_v2';
const DEFAULTS = {
	reduceMotion: 'system',
	moreContrast: 'system',
	underlineLinks: false,
	readableFont: false,
	noSingleKeyShortcuts: false,
	pauseLiveUpdates: false
};

/** The signed-in account's settings, as the panel saves them */
async function withAccount(page: Page, settings: Partial<typeof DEFAULTS>) {
	const boot = await (await page.request.get(`${UI}/api/console/bootstrap`)).json();
	const res = await page.request.put(`${UI}/api/accessibility`, {
		headers: { 'X-CSRF-Token': boot.csrfToken },
		data: { ...DEFAULTS, ...settings }
	});
	expect(res.status()).toBe(200);
}

// The tests share the admin account's settings
test.describe.configure({ mode: 'serial' });
test.afterEach(async ({ page }) => withAccount(page, {}));

/** Chromium's default font size setting (Settings › Appearance › Font size) */
async function browserFontSize(page: Page, px: number) {
	const cdp = await page.context().newCDPSession(page);
	await cdp.send('Page.setFontSizes', { fontSizes: { standard: px, fixed: Math.round(px * 0.8125) } });
}

test.describe('the browser font size', () => {
	for (const px of [20, 24]) {
		test(`${px} px: text follows it and no page scrolls sideways`, async ({ page }, info) => {
			test.skip(info.project.name !== 'chromium', 'the font size setting is set through Chromium');
			await page.setViewportSize({ width: 1280, height: 800 });
			await browserFontSize(page, px);
			for (const path of ['/', '/devices', '/settings', '/help-requests', '/account']) {
				await page.goto(`${UI}${path}`);
				await expect(page.locator('#main-content').getByRole('heading', { level: 1 })).toBeVisible();
				const m = await page.evaluate(() => ({
					root: getComputedStyle(document.documentElement).fontSize,
					body: getComputedStyle(document.body).fontSize,
					overflow: document.documentElement.scrollWidth - innerWidth
				}));
				expect(m.root, path).toBe(`${px}px`);
				// --text-base is 0.875rem
				expect(m.body, path).toBe(`${px * 0.875}px`);
				expect(m.overflow, `${path} scrolls sideways`).toBeLessThanOrEqual(0);
				await page.screenshot({
					path: info.outputPath(`font-${px}${path.replace(/\//g, '-') || '-home'}.png`)
				});
			}
		});
	}
});

test.describe('More contrast', () => {
	/** A token as the browser computes it, rgb(...) */
	const token = (page: Page, name: string) =>
		page.evaluate((n) => {
			const probe = document.createElement('div');
			probe.style.color = `var(${n})`;
			document.body.append(probe);
			const c = getComputedStyle(probe).color;
			probe.remove();
			return c;
		}, name);
	const text = (page: Page) => token(page, '--text');

	test('follows the system, and On and Off override it', async ({ page, browserName }) => {
		await page.emulateMedia({ colorScheme: 'light', contrast: 'more' });
		await page.goto(`${UI}/devices`);
		await expect(page.locator('html')).toHaveAttribute('data-a11y-more-contrast', 'true');
		expect(await text(page)).toBe('rgb(0, 0, 0)');

		await page.emulateMedia({ colorScheme: 'light', contrast: 'no-preference' });
		// Playwright's Firefox sends no change event for an emulated contrast
		// (seen in CI): there the page reads the new setting on its next load
		if (browserName === 'firefox') await page.reload();
		await expect(page.locator('html')).toHaveAttribute('data-a11y-more-contrast', 'false');
		expect(await text(page)).not.toBe('rgb(0, 0, 0)');
	});

	test('the high-contrast palette of the dark theme', async ({ page }) => {
		await withAccount(page, { moreContrast: 'on' });
		await page.addInitScript(() => localStorage.setItem('nd-theme-choice', 'dark'));
		await page.goto(`${UI}/devices`);
		await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
		// A browser without a copy gets the account's settings with the bootstrap
		await expect(page.locator('html')).toHaveAttribute('data-a11y-more-contrast', 'true');
		expect(await text(page)).toBe('rgb(255, 255, 255)');
		expect(await token(page, '--bg')).toBe('rgb(0, 0, 0)');
	});

	test('a Windows contrast theme wins, and the console still works under it', async ({ page }) => {
		await withAccount(page, { moreContrast: 'on' });
		await page.emulateMedia({ forcedColors: 'active' });
		await page.goto(`${UI}/devices`);
		await expect(page.locator('html')).toHaveAttribute('data-a11y-more-contrast', 'true');
		await expect(page.locator('#main-content').getByRole('heading', { level: 1 })).toBeVisible();
		// The token keeps the theme's own value: the system's colours are drawn over it
		const raw = () =>
			page.evaluate(() => getComputedStyle(document.documentElement).getPropertyValue('--text').trim());
		const forced = await raw();
		await page.emulateMedia({ forcedColors: 'none' });
		expect(await raw()).toMatch(/^#0{3}(0{3})?$/);
		await page.emulateMedia({ forcedColors: 'active' });
		expect(forced).not.toMatch(/^#0{3}(0{3})?$/);
		// The page and a dialog still work
		await page.keyboard.press('Control+k');
		await expect(page.getByRole('dialog')).toBeVisible();
		await page.keyboard.press('Escape');
		await page.getByRole('button', { name: /e2e_admin/ }).click();
		await expect(page.getByRole('menu')).toBeVisible();
	});
});

test('Readable font reaches the dialogs outside the main area', async ({ page }) => {
	await withAccount(page, { readableFont: true });
	await page.goto(`${UI}/devices`);
	await expect(page.locator('html')).toHaveAttribute('data-a11y-readable-font', 'true');
	await expect(page.locator('#main-content').getByRole('heading', { level: 1 })).toBeVisible();
	await page.keyboard.press('Control+k');
	const dialog = page.getByRole('dialog');
	await expect(dialog).toBeVisible();
	const s = await dialog.evaluate((el) => {
		const input = el.querySelector('input')!;
		const cs = getComputedStyle(input);
		return { family: cs.fontFamily, letter: parseFloat(cs.letterSpacing), size: parseFloat(cs.fontSize) };
	});
	expect(s.family).toMatch(/^Verdana/);
	expect(s.letter / s.size).toBeCloseTo(0.035, 3);
});

test.describe('Pause live updates', () => {
	test('a page without its own line gets one with Refresh from the shell', async ({ page }) => {
		await withAccount(page, { pauseLiveUpdates: true });
		await page.goto(`${UI}/tickets`);
		await expect(page.locator('#main-content').getByRole('heading', { level: 1 })).toBeVisible();
		await expect(page.getByText('Live updates are paused.')).toBeVisible();
		const loads: string[] = [];
		page.on('request', (r) => {
			if (new URL(r.url()).pathname.startsWith('/api/tickets')) loads.push(r.url());
		});
		await page.getByRole('button', { name: 'Refresh' }).click();
		await expect.poll(() => loads.length).toBeGreaterThan(0);
		await expect(page.getByText(/^Last updated /)).toBeVisible();
	});

	test('the devices list has its own line, and only one', async ({ page }) => {
		await withAccount(page, { pauseLiveUpdates: true });
		await page.goto(`${UI}/devices`);
		await expect(page.getByText('Live updates are paused.')).toHaveCount(1);
		await expect(page.getByRole('button', { name: 'Refresh' })).toHaveCount(1);
	});
});

test('a change is saved for the account and announced', async ({ page, browser }) => {
	await page.goto(`${UI}/devices`);
	try {
		await page.getByRole('button', { name: /e2e_admin/ }).click();
		await page.getByRole('menuitem', { name: 'Accessibility' }).click();
		const sheet = page.getByRole('dialog', { name: 'Accessibility' });
		await expect(sheet.getByText(/saved for your account/)).toBeVisible();
		await sheet.getByRole('checkbox', { name: 'Underline links' }).check();
		await expect(page.locator('[data-announcer="polite"]')).toHaveText('Saved');
		await expect(page.locator('html')).toHaveAttribute('data-a11y-underline-links', 'true');

		// Another browser of the same account, with no copy of its own
		const other = await browser.newContext({ storageState: await page.context().storageState() });
		const second = await other.newPage();
		await second.addInitScript(() => localStorage.clear());
		await second.goto(`${UI}/devices`);
		await expect(second.locator('html')).toHaveAttribute('data-a11y-underline-links', 'true');
		await other.close();
	} finally {
		await withAccount(page, {});
	}
});

test('the sign-in page offers the panel', async ({ browser }) => {
	const ctx = await browser.newContext({ storageState: undefined });
	const page = await ctx.newPage();
	await page.goto(`${UI}/login`);
	await page.getByRole('button', { name: 'Accessibility' }).click();
	const sheet = page.getByRole('dialog', { name: 'Accessibility' });
	await expect(sheet.getByText(/saved in this browser/)).toBeVisible();
	await sheet.getByRole('group', { name: 'Reduce motion' }).getByRole('radio', { name: 'On' }).check();
	await expect(page.locator('html')).toHaveAttribute('data-a11y-reduce-motion', 'true');
	expect(await page.evaluate((k) => JSON.parse(localStorage.getItem(k)!).reduceMotion, KEY)).toBe('on');
	await ctx.close();
});

test('the focus ring reaches 3:1 against every surface, in every palette', async ({ page }) => {
	const results: string[] = [];
	for (const theme of ['light', 'dark'] as const) {
		for (const more of [false, true]) {
			await page.addInitScript((t) => localStorage.setItem('nd-theme-choice', t), theme);
			await withAccount(page, { moreContrast: more ? 'on' : 'off' });
			await page.goto(`${UI}/devices`);
			await expect(page.locator('html')).toHaveAttribute('data-theme', theme);
			await expect(page.locator('html')).toHaveAttribute('data-a11y-more-contrast', String(more));
			const ratios = await page.evaluate(() => {
				const probe = document.createElement('div');
				document.body.append(probe);
				const rgb = (token: string) => {
					probe.style.color = `var(${token})`;
					const [r, g, b] = getComputedStyle(probe)
						.color.match(/\d+(\.\d+)?/g)!
						.map(Number);
					return [r!, g!, b!];
				};
				const lum = ([r, g, b]: number[]) => {
					const c = [r!, g!, b!].map((v) => {
						const s = v / 255;
						return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
					});
					return 0.2126 * c[0]! + 0.7152 * c[1]! + 0.0722 * c[2]!;
				};
				const ratio = (a: number[], b: number[]) => {
					const [x, y] = [lum(a), lum(b)].sort((p, q) => q - p);
					return (x! + 0.05) / (y! + 0.05);
				};
				const focus = rgb('--focus');
				const out = Object.fromEntries(
					['--bg', '--bg-subtle', '--bg-float'].map((s) => [s, ratio(focus, rgb(s))])
				);
				probe.remove();
				return out;
			});
			for (const [surface, r] of Object.entries(ratios))
				if (r < 3) results.push(`${theme}${more ? ' more contrast' : ''}: ${surface} ${r.toFixed(2)}`);
		}
	}
	expect(results).toEqual([]);
});

test.describe('the focus ring is never clipped', () => {
	test.describe.configure({ timeout: 120_000 });
	for (const path of ['/devices', '/settings', '/account', '/help-requests']) {
		test(path, async ({ page }) => {
			await page.setViewportSize({ width: 1280, height: 800 });
			await page.goto(`${UI}${path}`);
			await expect(page.locator('#main-content').getByRole('heading', { level: 1 })).toBeVisible();
			const clipped = new Set<string>();
			for (let i = 0; i < 60; i++) {
				await page.keyboard.press('Tab');
				await page.waitForTimeout(60);
				const hit = await page.evaluate(() => {
					const el = document.activeElement as HTMLElement | null;
					if (!el || el === document.body) return null;
					const cs = getComputedStyle(el);
					if (cs.outlineStyle === 'none') return null;
					// The ring: outline width plus offset around the box (an inset ring has a negative offset)
					const grow = Math.max(0, parseFloat(cs.outlineWidth) + parseFloat(cs.outlineOffset));
					const r = el.getBoundingClientRect();
					const ring = { l: r.left - grow, t: r.top - grow, r: r.right + grow, b: r.bottom + grow };
					for (let p = el.parentElement; p && p !== document.documentElement; p = p.parentElement) {
						const ps = getComputedStyle(p);
						if (ps.overflowX === 'visible' && ps.overflowY === 'visible') continue;
						const pr = p.getBoundingClientRect();
						// Only the edges the box clips: a scrolled-away part is not a clipped ring
						const left = pr.left + p.clientLeft;
						const top = pr.top + p.clientTop;
						const right = left + p.clientWidth;
						const bottom = top + p.clientHeight;
						const inside = r.left >= left && r.right <= right && r.top >= top && r.bottom <= bottom;
						if (!inside) continue;
						if (ring.l < left - 0.5 || ring.t < top - 0.5 || ring.r > right + 0.5 || ring.b > bottom + 0.5) {
							const name = (el.getAttribute('aria-label') || el.textContent || el.tagName)
								.trim()
								.slice(0, 40);
							return `${name} in ${p.tagName.toLowerCase()}.${[...p.classList].join('.')}`;
						}
					}
					return null;
				});
				if (hit) clipped.add(hit);
			}
			expect([...clipped]).toEqual([]);
		});
	}
});
