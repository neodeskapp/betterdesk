/**
 * Screenshots of every console surface that holds a setting, for comparing
 * a settings overhaul before and after. Not a test of behaviour: it only
 * opens pages, sheets and dialogs and never saves anything.
 *
 * Runs only with SHOTS=1, as super admin (stored session, see
 * global-setup.ts). Each surface is shot at 1440x900 and 390x844, in the
 * light and the dark theme (the browser's theme choice in localStorage plus
 * prefers-color-scheme), as test-results/shots/<surface>-<width>-<theme>.png.
 * A page is shot full length; a sheet or dialog is shot with the window made
 * tall enough to show all of it. What did not load is written to
 * test-results/shots/_notes/<surface>-<theme>.json.
 *
 *   E2E_ENV="-e SHOTS=1" e2e.sh <instance> --project=chromium e2e/settings-shots.spec.ts
 */
import type { Locator, Page } from '@playwright/test';
import fs from 'node:fs';
import { test, expect } from './fixtures.ts';
import { UI } from './roles.ts';

type Open = (page: Page) => Promise<Locator | void>;
type Surface = {
	/** File name stem */
	name: string;
	/** Address, or a function that finds it (for a seeded organization's id) */
	path: string | ((page: Page) => Promise<string>);
	/** Opens a sheet, dialog or menu after the page loaded; returns what to wait for */
	open?: Open;
};

const OUT = 'test-results/shots';
const WIDTHS = [
	{ width: 1440, height: 900 },
	{ width: 390, height: 844 }
] as const;
const THEMES = ['light', 'dark'] as const;

/* ── Openers ── */

const button = (name: string | RegExp) => async (page: Page) => {
	await page
		.getByRole('button', { name, exact: typeof name === 'string' })
		.first()
		.click();
	return page.getByRole('dialog').last();
};

const byClass = (selector: string) => async (page: Page) => {
	await page.locator(selector).first().click();
	return page.getByRole('dialog').last();
};

/** A row's kebab menu, then one of its items */
const menu = (kebab: string, item: string | RegExp) => async (page: Page) => {
	await page.getByRole('button', { name: kebab, exact: true }).first().click();
	await page.getByRole('menuitem', { name: item }).first().click();
	return page.getByRole('dialog').last();
};

/** Devices › Access rules: open the closed section and wait for its rules */
async function openAccessRules(page: Page): Promise<void> {
	const heading = page.getByRole('heading', { name: 'Access rules', level: 2 });
	await heading.scrollIntoViewIfNeeded();
	await heading.click();
	await expect(page.getByRole('button', { name: 'Add rule', exact: true }).first()).toBeVisible(LOAD);
}

/** The first seeded organization's detail page, with a hash for the tab */
const orgPath =
	(hash = '') =>
	async (page: Page) => {
		const r = await page.request.get('/api/panel/org', { headers: { Accept: 'application/json' } });
		const body = await r.json();
		const list = (body.organizations ?? body.data ?? body) as { id: string }[];
		const first = Array.isArray(list) ? list[0] : undefined;
		if (!first) throw new Error('no seeded organization');
		return `/organizations/${encodeURIComponent(first.id)}${hash}`;
	};

/** Rules > Policies with an organization open: the named seeded one, or the first */
const policiesPath = (name?: string) => async (page: Page) => {
	const r = await page.request.get('/api/panel/org', { headers: { Accept: 'application/json' } });
	const body = await r.json();
	const list = (body.organizations ?? body.data ?? body) as { id: string; name?: string }[];
	const pick = Array.isArray(list) ? (list.find((o) => o.name === name) ?? list[0]) : undefined;
	if (!pick) throw new Error('no seeded organization');
	return `/policies/${encodeURIComponent(pick.id)}`;
};

/* ── The surfaces ── */

const SURFACES: Surface[] = [
	// Settings tabs (lib/settings/tabs.ts); Translations is its own page
	{ name: 'settings-general', path: '/settings' },
	{ name: 'settings-signin', path: '/settings?tab=auth' },
	{ name: 'settings-ldap', path: '/settings?tab=ldap' },
	{ name: 'settings-sso', path: '/settings?tab=sso' },
	{ name: 'settings-joining', path: '/settings?tab=joining' },
	{ name: 'settings-appearance', path: '/settings?tab=branding' },
	{ name: 'settings-brand', path: '/settings?tab=brand' },
	{ name: 'settings-email', path: '/settings?tab=email' },
	{ name: 'settings-time', path: '/settings?tab=time' },
	{ name: 'settings-meshcentral', path: '/settings?tab=meshcentral' },
	{ name: 'settings-translations', path: '/languages' },

	// Rules
	{ name: 'rules-policies', path: '/policies' },
	{ name: 'rules-policies-org', path: policiesPath('Demo Northwind Clinic') },
	{ name: 'rules-policies-unset', path: policiesPath('Demo Lab Collective') },
	{ name: 'rules-automation', path: '/automation' },
	{ name: 'rules-automation-rule-sheet', path: '/automation', open: button('Create rule') },
	{ name: 'rules-resource-control', path: '/resource-control' },
	{ name: 'rules-resource-control-sheet', path: '/resource-control', open: button(/^Edit rules:/) },
	{ name: 'rules-dataguard', path: '/dataguard' },
	{ name: 'rules-dataguard-policy-sheet', path: '/dataguard', open: button('Create policy') },
	{ name: 'rules-attestation', path: '/attestation' },

	// Server
	{ name: 'server-management', path: '/server-management' },
	{ name: 'server-scaling', path: '/scaling' },
	{ name: 'server-scaling-relay-sheet', path: '/scaling', open: byClass('.relay-add') },
	// The assignment rule sheet opens only once a relay server exists; seeded data has none
	{ name: 'server-backups', path: '/server/backups' },
	{ name: 'server-updates', path: '/server/updates' },
	{ name: 'server-config-files', path: '/server/config-files' },
	{ name: 'server-attestation', path: '/server-attestation' },

	// Permissions
	{ name: 'permissions', path: '/permissions' },
	{ name: 'permissions-server-admin', path: '/permissions?role=server_admin' },

	// Organizations
	{ name: 'org-settings-tab', path: orgPath('#settings') },
	{ name: 'org-edit-dialog', path: orgPath(), open: byClass('.edit-org') },

	// Users
	{ name: 'users', path: '/users' },
	{ name: 'users-edit-sheet', path: '/users', open: menu('More actions for e2e_operator', 'Edit') },
	{
		name: 'users-access-sheet',
		path: '/users',
		open: menu('More actions for e2e_operator', /^Console access/)
	},
	{ name: 'users-orgs-sheet', path: '/users', open: menu('More actions for e2e_operator', /^Organizations/) },
	{ name: 'users-add-sheet', path: '/users', open: button('Add user') },
	{ name: 'users-group-dialog', path: '/users', open: button('Create group') },

	// Your account
	{ name: 'account', path: '/account' },
	{ name: 'account-api-token-dialog', path: '/account', open: button('New token') },
	{
		name: 'account-accessibility-dialog',
		path: '/account',
		open: button('Accessibility settings')
	},

	// Billing
	{ name: 'billing', path: '/billing' },
	{ name: 'billing-settings', path: '/billing?settings=1' },

	// Chat
	{ name: 'chat', path: '/chat' },
	{
		name: 'chat-notifications-menu',
		path: '/chat',
		open: async (page) => {
			await page.getByRole('button', { name: 'Chat notifications' }).first().click();
			return page.getByRole('menu').last();
		}
	},
	{
		name: 'chat-server-settings-dialog',
		path: '/chat',
		open: async (page) => {
			await page.getByRole('button', { name: 'Chat notifications' }).first().click();
			await page.getByRole('menuitem', { name: 'Chat settings for this server' }).click();
			return page.getByRole('dialog').last();
		}
	},

	// Devices: access rules are a closed section under the list
	{ name: 'devices-group-sheet', path: '/devices', open: button('New group') },
	{
		name: 'devices-access-rules',
		path: '/devices',
		open: async (page) => {
			await openAccessRules(page);
		}
	},
	{
		name: 'devices-access-rule-sheet',
		path: '/devices',
		open: async (page) => {
			await openAccessRules(page);
			return button('Add rule')(page);
		}
	},
	{ name: 'devices-add-invites', path: '/devices/add?tab=invites' },
	{ name: 'devices-add-tokens', path: '/devices/add?tab=tokens' },
	{ name: 'devices-add-tokens-bulk-dialog', path: '/devices/add?tab=tokens', open: button('Create several') },
	{ name: 'devices-add-pairing', path: '/devices/add?tab=pairing' },
	{ name: 'devices-add-agents', path: '/devices/add?tab=agents' },
	{ name: 'devices-add-manual', path: '/devices/add?tab=manual' }
];

/* ── Helpers ── */

const LOAD = { timeout: 20_000 };

/** The page's own requests have answered: no visible loading line or busy region left */
async function settled(page: Page, notes: string[]): Promise<void> {
	await page.waitForLoadState('domcontentloaded');
	try {
		await expect(page.locator('h1').first()).toBeVisible(LOAD);
	} catch {
		notes.push('no heading');
	}
	try {
		await expect(page.locator('[role="status"]:visible', { hasText: /^Loading/ })).toHaveCount(0, LOAD);
		await expect(page.locator('[aria-busy="true"]:visible')).toHaveCount(0, LOAD);
		// Some lists say Loading... in a plain paragraph (the chat list)
		await expect(page.getByText(/^Loading\.\.\.$/).filter({ visible: true })).toHaveCount(0, LOAD);
	} catch {
		notes.push('still loading after 20 s');
	}
	await page.evaluate(async () => {
		await document.fonts.ready;
		const ending = document
			.getAnimations()
			.filter((a) => a.playState === 'running' && a.effect?.getComputedTiming().iterations !== Infinity)
			.map((a) => a.finished.catch(() => undefined));
		await Promise.race([Promise.all(ending), new Promise((done) => setTimeout(done, 2_000))]);
	});
	// Lazy images and a last layout pass
	await page.waitForTimeout(400);
}

/** Visible error lines (role alert), for the notes */
async function alerts(page: Page): Promise<string[]> {
	return (await page.locator('[role="alert"]:visible').allInnerTexts())
		.map((s) => s.trim().replace(/\s+/g, ' '))
		.filter(Boolean);
}

/**
 * How much taller the window must be to show all of an open sheet or
 * dialog: its tallest scrolling part's hidden height
 */
async function overlayOverflow(target: Locator): Promise<number> {
	return target.evaluate((root) => {
		let extra = 0;
		for (const el of [root, ...root.querySelectorAll('*')] as HTMLElement[]) {
			const oy = getComputedStyle(el).overflowY;
			if ((oy === 'auto' || oy === 'scroll') && el.scrollHeight > el.clientHeight + 2)
				extra = Math.max(extra, el.scrollHeight - el.clientHeight);
		}
		return extra;
	});
}

function writeNotes(file: string, data: unknown): void {
	fs.mkdirSync(`${OUT}/_notes`, { recursive: true });
	fs.writeFileSync(`${OUT}/_notes/${file}.json`, JSON.stringify(data, null, 2));
}

test.skip(process.env.SHOTS !== '1', 'screenshots only with SHOTS=1');
test.use({ role: 'super_admin', allowProblems: [/.*/] });

for (const theme of THEMES) {
	test.describe(`settings shots, ${theme}`, () => {
		test.use({ colorScheme: theme });
		test.beforeEach(async ({ page }) => {
			// This browser's theme choice (lib/state/theme.svelte.ts), read by console-boot.js before first paint
			await page.addInitScript((choice) => {
				try {
					localStorage.setItem('nd-theme-choice', choice);
				} catch {
					/* storage blocked: prefers-color-scheme still applies */
				}
			}, theme);
		});

		for (const s of SURFACES) {
			test(s.name, async ({ page, problems }) => {
				test.setTimeout(120_000);
				// A missing button fails its shot in 20 s instead of using up the test's time
				page.setDefaultTimeout(20_000);
				const notes: string[] = [];
				let route = typeof s.path === 'string' ? s.path : '';
				for (const size of WIDTHS) {
					await page.setViewportSize(size);
					const file = `${OUT}/${s.name}-${size.width}-${theme}.png`;
					try {
						if (typeof s.path !== 'string') route = await s.path(page);
						await page.goto(`${UI}${route}`);
						await settled(page, notes);
						const shown = await page.evaluate(() => document.documentElement.getAttribute('data-theme'));
						if (shown !== theme) notes.push(`${size.width}: theme shows ${shown}`);
						let overlay: Locator | void = undefined;
						if (s.open) {
							overlay = await s.open(page);
							if (overlay) await expect(overlay).toBeVisible(LOAD);
							await settled(page, notes);
						}
						for (const a of await alerts(page)) notes.push(`${size.width}: alert "${a.slice(0, 160)}"`);
						if (overlay) {
							const extra = await overlayOverflow(overlay);
							if (extra > 0) {
								await page.setViewportSize({
									width: size.width,
									height: Math.min(size.height + extra, 8000)
								});
								await page.waitForTimeout(300);
							}
							await page.screenshot({ path: file, animations: 'disabled' });
							await page.keyboard.press('Escape');
						} else {
							// From the top, without a focus ring: a page that scrolled to an opened
							// section draws its sticky top bar and sidebar half-way down otherwise
							await page.evaluate(() => {
								(document.activeElement as HTMLElement | null)?.blur();
								window.scrollTo(0, 0);
							});
							await page.waitForTimeout(200);
							await page.screenshot({ path: file, fullPage: true, animations: 'disabled' });
						}
					} catch (e) {
						notes.push(`${size.width}: failed: ${String((e as Error).message).split('\n')[0]}`);
						await page.screenshot({ path: file, fullPage: true }).catch(() => undefined);
					}
				}
				const http = [...new Set(problems.list.filter((p) => /^http|^failed/.test(p)))];
				writeNotes(`${s.name}-${theme}`, { surface: s.name, route, theme, notes: [...new Set(notes)], http });
			});
		}
	});
}
