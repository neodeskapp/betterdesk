/**
 * Accessibility sweep (audit evidence, A11Y_SWEEP=1): every console route in
 * light and dark at 320, 768 and 1280 px, plus the states a script can
 * open. Runs all axe rules (WCAG 2.0 to 2.2 A and AA, best practice) and
 * writes each result to test-results/a11y-sweep/; it never fails on a
 * violation, the audit reads the files.
 */
import AxeBuilder from '@axe-core/playwright';
import fs from 'node:fs';
import type { Page } from '@playwright/test';
import { test } from './fixtures.ts';

const OUT = 'test-results/a11y-sweep';
const THEMES = ['light', 'dark'] as const;
const WIDTHS = [320, 768, 1280] as const;

const CONSOLE_ROUTES = [
	'/',
	'/account',
	'/activity',
	'/attestation',
	'/automation',
	'/billing',
	'/cdap',
	'/cdap/devices',
	'/chat',
	'/cross-platform',
	'/dataguard',
	'/devices',
	'/devices/add',
	'/fleet',
	'/help-requests',
	'/inventory',
	'/kit',
	'/languages',
	'/network',
	'/organizations',
	'/permissions',
	'/policies',
	'/registrations',
	'/remote/sessions',
	'/reports',
	'/resource-control',
	'/scaling',
	'/security-audit',
	'/security-checks',
	'/server-attestation',
	'/server-management',
	'/server/backups',
	'/server/config-files',
	'/server/updates',
	'/settings',
	'/settings?tab=auth',
	'/settings?tab=ldap',
	'/settings?tab=sso',
	'/settings?tab=joining',
	'/settings?tab=branding',
	'/settings?tab=brand',
	'/settings?tab=email',
	'/settings?tab=time',
	'/settings?tab=meshcentral',
	'/tickets',
	'/toolkit',
	'/users',
	'/cdap-studio',
	'/remote'
];

const PUBLIC_ROUTES = [
	'/login',
	'/portal',
	'/error/404',
	'/error/500',
	'/invite',
	'/set-password',
	'/join/AAAA-BBBB',
	'/s/AAAA-BBBB',
	'/d/unknown',
	'/remote/login',
	'/remote/guest',
	'/no-such-page'
];

function slug(s: string): string {
	return s.replace(/^\//, '').replace(/[^a-z0-9]+/gi, '_') || 'home';
}

async function settle(page: Page): Promise<void> {
	await page.waitForLoadState('networkidle', { timeout: 10_000 }).catch(() => undefined);
	await page.evaluate(async () => {
		const ending = document
			.getAnimations()
			.filter((a) => a.playState === 'running' && a.effect?.getComputedTiming().iterations !== Infinity)
			.map((a) => a.finished.catch(() => undefined));
		await Promise.race([Promise.all(ending), new Promise((done) => setTimeout(done, 2_000))]);
	});
}

async function scan(
	page: Page,
	surface: string,
	theme: string,
	width: number,
	state = 'page'
): Promise<void> {
	await settle(page);
	const r = await new AxeBuilder({ page }).analyze();
	const facts = await page.evaluate(() => ({
		title: document.title,
		lang: document.documentElement.lang,
		dir: document.documentElement.dir,
		h1: [...document.querySelectorAll('h1')].map((h) => h.textContent?.trim().slice(0, 80)),
		landmarks: [
			...document.querySelectorAll(
				'header,nav,main,aside,footer,[role=banner],[role=navigation],[role=main],[role=complementary],[role=contentinfo],[role=search],[role=region]'
			)
		].map(
			(e) =>
				`${e.tagName.toLowerCase()}${e.getAttribute('role') ? '[' + e.getAttribute('role') + ']' : ''}${e.getAttribute('aria-label') ? ' "' + e.getAttribute('aria-label') + '"' : ''}`
		),
		hscroll: document.documentElement.scrollWidth > document.documentElement.clientWidth + 1,
		scrollWidth: document.documentElement.scrollWidth,
		positiveTabindex: document.querySelectorAll('[tabindex]:not([tabindex="0"]):not([tabindex="-1"])').length,
		skipLink: !!document.querySelector('a[href^="#"]')?.textContent?.match(/skip|main|inhalt/i)
	}));
	const rec = {
		surface,
		state,
		theme,
		width,
		url: page.url(),
		facts,
		violations: r.violations.map((v) => ({
			id: v.id,
			impact: v.impact,
			tags: v.tags,
			help: v.help,
			nodes: v.nodes.map((n) => ({
				target: n.target.join(' '),
				html: n.html.slice(0, 300),
				summary: n.failureSummary?.slice(0, 400)
			}))
		})),
		incomplete: r.incomplete.map((v) => ({ id: v.id, count: v.nodes.length })),
		passes: r.passes.length
	};
	fs.mkdirSync(OUT, { recursive: true });
	fs.writeFileSync(
		`${OUT}/${slug(surface)}__${slug(state)}__${theme}__${width}.json`,
		JSON.stringify(rec, null, 1)
	);
}

async function prepare(page: Page, theme: string, width: number): Promise<void> {
	await page.setViewportSize({ width, height: width === 320 ? 640 : 900 });
	await page.emulateMedia({ colorScheme: theme as 'light' | 'dark' });
	await page.addInitScript((t) => {
		try {
			localStorage.setItem('nd-theme-choice', t);
		} catch {
			/* blocked storage */
		}
	}, theme);
}

test.skip(!process.env.A11Y_SWEEP, 'audit sweep runs with A11Y_SWEEP=1');
test.describe.configure({ mode: 'parallel' });
test.use({ allowProblems: [/./] });

for (const theme of THEMES) {
	for (const width of WIDTHS) {
		test.describe(`${theme} ${width}`, () => {
			for (const route of CONSOLE_ROUTES) {
				test(`console ${route}`, async ({ page }) => {
					test.setTimeout(60_000);
					await prepare(page, theme, width);
					await page.goto(route);
					await scan(page, route, theme, width);
				});
			}
			test.describe('public', () => {
				test.use({ storageState: { cookies: [], origins: [] } });
				for (const route of PUBLIC_ROUTES) {
					test(`public ${route}`, async ({ page }) => {
						await prepare(page, theme, width);
						await page.goto(route);
						await scan(page, route, theme, width);
					});
				}
			});

			test('states: shell', async ({ page }) => {
				test.setTimeout(120_000);
				await prepare(page, theme, width);
				await page.goto('/devices');
				await settle(page);
				// Phone navigation drawer
				const navToggle = page.getByRole('button', { name: /menu|navigation|navigat/i }).first();
				if (width === 320 && (await navToggle.isVisible().catch(() => false))) {
					await navToggle.click();
					await scan(page, '/devices', theme, width, 'nav-drawer');
					await page.keyboard.press('Escape');
				}
				// Account menu
				const account = page.locator('header').getByRole('button').last();
				if (await account.isVisible().catch(() => false)) {
					await account.click();
					await scan(page, '/devices', theme, width, 'account-menu');
					const a11yItem = page.getByRole('menuitem', { name: /accessib/i });
					if (await a11yItem.isVisible().catch(() => false)) {
						await a11yItem.click();
						await scan(page, '/devices', theme, width, 'a11y-panel');
					}
					await page.keyboard.press('Escape');
				}
				// Search dialog
				await page.keyboard.press('Control+k');
				await page.waitForTimeout(300);
				if (
					await page
						.getByRole('dialog')
						.isVisible()
						.catch(() => false)
				) {
					await scan(page, '/devices', theme, width, 'search-dialog');
					await page.keyboard.type('set');
					await page.waitForTimeout(500);
					await scan(page, '/devices', theme, width, 'search-results');
					await page.keyboard.press('Escape');
				}
			});

			test('states: devices', async ({ page }) => {
				test.setTimeout(120_000);
				await prepare(page, theme, width);
				await page.goto('/devices');
				await settle(page);
				// First device's details
				const row = page.locator('main').getByRole('link').or(page.locator('main [role=row] button')).first();
				if (await row.isVisible().catch(() => false)) {
					await row.click();
					await scan(page, '/devices', theme, width, 'device-details');
					await page.keyboard.press('Escape');
				}
				// First menu button in main (row actions or toolbar)
				const more = page.locator('main button[aria-haspopup]').first();
				if (await more.isVisible().catch(() => false)) {
					await more.click();
					await scan(page, '/devices', theme, width, 'menu-open');
					await page.keyboard.press('Escape');
				}
				// Empty search result
				const search = page
					.locator('main')
					.getByRole('searchbox')
					.or(page.locator('main input[type=search]'))
					.first();
				if (await search.isVisible().catch(() => false)) {
					await search.fill('zzzz-no-such-device');
					await scan(page, '/devices', theme, width, 'no-results');
				}
			});

			test('states: users dialog', async ({ page }) => {
				test.setTimeout(120_000);
				await prepare(page, theme, width);
				await page.goto('/users');
				await settle(page);
				const add = page
					.locator('main')
					.getByRole('button', { name: /add|new|invite|create/i })
					.first();
				if (await add.isVisible().catch(() => false)) {
					await add.click();
					await scan(page, '/users', theme, width, 'add-dialog');
					// Submit empty: error state
					const submit = page
						.getByRole('dialog')
						.getByRole('button', { name: /add|create|invite|save/i })
						.last();
					if (await submit.isVisible().catch(() => false)) {
						await submit.click();
						await scan(page, '/users', theme, width, 'add-dialog-errors');
					}
					await page.keyboard.press('Escape');
				}
			});

			test.describe('states: public', () => {
				test.use({ storageState: { cookies: [], origins: [] } });
				test('login error', async ({ page }) => {
					await prepare(page, theme, width);
					await page.goto('/login?autoLaunch=0');
					await settle(page);
					await page.locator('#username').fill('nobody-a11y');
					await page.locator('#password').fill('wrong-password');
					await page.locator('#password').press('Enter');
					await page.waitForTimeout(1_500);
					await scan(page, '/login', theme, width, 'error');
				});
			});
		});
	}
}
