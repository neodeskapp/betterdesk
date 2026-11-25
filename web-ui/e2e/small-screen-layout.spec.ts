import { test, expect } from './fixtures.ts';
import type { Page } from '@playwright/test';
import { UI } from './roles.ts';

/**
 * Every page and the dialogs people meet most, at phone width (390 px):
 * nothing scrolls sideways, no control sits outside the screen, and the
 * buttons of a dialog's footer and a page's head are at least 44 px high.
 */

const PAGES = [
	'/',
	'/devices',
	'/registrations',
	'/inventory',
	'/network',
	'/fleet',
	'/cross-platform',
	'/cdap',
	'/help-requests',
	'/tickets',
	'/chat',
	'/toolkit',
	'/users',
	'/organizations',
	'/permissions',
	'/security-audit',
	'/activity',
	'/reports',
	'/billing?tab=sessions',
	'/billing?tab=packages',
	'/billing?tab=reports',
	'/security-checks',
	'/settings',
	'/settings?tab=branding',
	'/settings?tab=auth',
	'/server/backups',
	'/server/updates',
	'/settings?tab=email',
	'/settings?tab=time',
	'/server/config-files',
	'/languages',
	'/keys',
	'/tokens',
	'/pairing',
	'/agents',
	'/client-branding',
	'/policies',
	'/automation',
	'/resource-control',
	'/dataguard',
	'/attestation',
	'/server-management',
	'/scaling',
	'/server-attestation',
	'/api-tokens'
];

type Open = (p: Page) => Promise<unknown>;
const button =
	(name: RegExp): Open =>
	(p) =>
		p.getByRole('button', { name }).first().click();

const DIALOGS: { name: string; url: string; open: Open }[] = [
	{ name: 'Quick support', url: '/devices', open: (p) => p.locator('.quick-support-btn').click() },
	{ name: 'Add a computer', url: '/devices', open: button(/^Add a computer$/) },
	{ name: 'Device panel', url: '/devices', open: (p) => p.locator('.device-card, tbody tr').first().click() },
	{ name: 'New API token', url: '/api-tokens', open: button(/^New token$/) },
	{ name: 'Add user', url: '/users', open: button(/^Add user$/) },
	{ name: 'Create device token', url: '/tokens', open: button(/^Create token$/) },
	{ name: 'Add target', url: '/network', open: button(/^Add target$/) },
	{ name: 'New ticket', url: '/tickets', open: button(/^New ticket$/) },
	{ name: 'Create organization', url: '/organizations', open: button(/^Create organization$/) },
	{ name: 'Create policy (DLP)', url: '/dataguard', open: button(/^Create policy$/) }
];

async function problems(page: Page, scope: string): Promise<string[]> {
	return page.evaluate((scopeSel) => {
		const out: string[] = [];
		const vw = document.documentElement.clientWidth;
		const doc = document.scrollingElement ?? document.documentElement;
		if (doc.scrollWidth > vw + 1) out.push(`the page scrolls sideways: ${doc.scrollWidth} > ${vw}`);
		const root = document.querySelector(scopeSel) ?? document.body;
		const dialog = document.querySelector<HTMLElement>('[role="dialog"][aria-modal="true"]');
		if (dialog) {
			const body = dialog.querySelector<HTMLElement>('.modal-body, .sheet-body') ?? dialog;
			if (body.scrollWidth > body.clientWidth + 1)
				out.push(`the dialog scrolls sideways: ${body.scrollWidth} > ${body.clientWidth}`);
		}
		const name = (el: Element) =>
			(el.getAttribute('aria-label') || el.textContent || el.tagName)
				.replace(/\s+/g, ' ')
				.trim()
				.slice(0, 40);
		const visible = (el: Element) => {
			const r = el.getBoundingClientRect();
			const s = getComputedStyle(el);
			return r.width > 0 && r.height > 0 && s.visibility !== 'hidden' && !el.closest('[inert], [hidden]');
		};
		// Controls outside the screen; a frame that scrolls on its own (tables, code) may hold them
		for (const el of root.querySelectorAll('button, a[href], input, select, textarea')) {
			if (!visible(el)) continue;
			let clipped = false;
			for (let p = el.parentElement; p && p !== document.body; p = p.parentElement) {
				const ox = getComputedStyle(p).overflowX;
				if (ox === 'auto' || ox === 'scroll' || ox === 'hidden' || ox === 'clip') {
					clipped = true;
					break;
				}
			}
			if (clipped) continue;
			const r = el.getBoundingClientRect();
			if (r.right > vw + 1 || r.left < -1)
				out.push(`outside the screen: ${name(el)} (${Math.round(r.left)}..${Math.round(r.right)})`);
		}
		// Touch targets in a dialog's footer and a page head's actions
		for (const el of document.querySelectorAll(
			'.modal-footer button, .sheet-footer button, .page-head .page-actions button, .page-head .page-actions a'
		)) {
			if (!visible(el)) continue;
			const r = el.getBoundingClientRect();
			if (r.height < 43.5) out.push(`smaller than 44 px: ${name(el)} (${Math.round(r.height)} px)`);
		}
		// In a dialog, a field is as wide as its row (numbers and ports may stay short)
		if (dialog) {
			for (const el of dialog.querySelectorAll<HTMLElement>('input, select, textarea')) {
				if (!visible(el) || el.classList.contains('is-short')) continue;
				if (
					el instanceof HTMLInputElement &&
					/^(checkbox|radio|number|color|range|file|hidden)$/.test(el.type)
				)
					continue;
				// The field and what sits beside it (a Show button) fill the row together
				const parent = el.parentElement;
				const row = parent?.getBoundingClientRect();
				const right = Math.max(...[...(parent?.children ?? [])].map((c) => c.getBoundingClientRect().right));
				const r = { width: right - el.getBoundingClientRect().left };
				if (row && right < row.right - 2)
					out.push(
						`narrower than its row: ${name(el.closest('.form-group, .field') ?? el)} (${Math.round(r.width)} of ${Math.round(row.width)} px)`
					);
			}
		}
		return out;
	}, scope);
}

test.use({ role: 'super_admin', viewport: { width: 390, height: 844 }, hasTouch: true });

test.describe('pages at 390 px', () => {
	for (const url of PAGES) {
		test(url, async ({ page }) => {
			await page.goto(`${UI}${url}`);
			await page.waitForLoadState('networkidle');
			await expect(page.locator('main')).toBeVisible();
			expect(await problems(page, 'main')).toEqual([]);
		});
	}
});

test.describe('dialogs at 390 px', () => {
	for (const d of DIALOGS) {
		test(d.name, async ({ page }) => {
			await page.goto(`${UI}${d.url}`);
			await page.waitForLoadState('networkidle');
			await d.open(page);
			const dialog = page.locator('[role="dialog"][aria-modal="true"]').last();
			await expect(dialog).toBeVisible();
			await page.waitForTimeout(300);
			expect(await problems(page, '[role="dialog"][aria-modal="true"]')).toEqual([]);
			// The dialog fills the screen on a phone
			const box = await dialog.boundingBox();
			expect(box && [Math.round(box.width), Math.round(box.height)]).toEqual([390, 844]);
		});
	}
});

test.describe('sidebar', () => {
	test.use({ viewport: { width: 1440, height: 900 }, hasTouch: false });

	test('no entry is cut off: long names wrap', async ({ page }) => {
		await page.goto(`${UI}/devices`);
		await expect(page.locator('.nav-label').first()).toBeVisible();
		const cut = await page.evaluate(() =>
			[...document.querySelectorAll<HTMLElement>('.nav-label')]
				.filter((el) => el.getClientRects().length > 0)
				.filter((el) => {
					// Cut off: the text needs more room than the label has
					const range = document.createRange();
					range.selectNodeContents(el);
					return range.getBoundingClientRect().width > el.getBoundingClientRect().width + 1;
				})
				.map((el) => el.textContent?.trim())
		);
		expect(cut).toEqual([]);
	});
});

test.describe('top bar', () => {
	test.use({ viewport: { width: 1440, height: 900 }, hasTouch: false });

	test('the search field shows its whole label on a wide screen', async ({ page }) => {
		await page.goto(`${UI}/devices`);
		const label = page.locator('.search-trigger .search-label');
		await expect(label).toBeVisible();
		const cut = await label.evaluate((el) => {
			const range = document.createRange();
			range.selectNodeContents(el);
			return range.getBoundingClientRect().width > el.getBoundingClientRect().width + 1;
		});
		expect(cut).toBe(false);
	});
});
