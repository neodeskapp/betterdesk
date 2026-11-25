import { test, expect, type Browser, type BrowserContext, type Page } from '@playwright/test';
import crypto from 'node:crypto';
import fs from 'node:fs';
import { expectNoAxeViolations } from './fixtures.ts';
import { UI } from './roles.ts';

/**
 * The setup guide on a fresh install, the way its first admin meets it:
 * the guide is there after the first sign-in, every step leads to its
 * place and back, the server ticks each step when it sees it done, Hide
 * closes it, Help and Settings bring it back, and the second admin never
 * gets it unasked. Then the overview with the guide at 1440 and 390 px,
 * light and dark, in English and German, with axe.
 *
 * It needs an instance without the seed (STACK_SEED=0): the seeded one
 * already has devices, sessions and people, so its guide is not fresh.
 * Its first account is admin, with the password in ADMIN_PASSWORD_FILE
 * (default /stack/secrets/admin_password). The walk changes the server, so
 * each browser gets an instance of its own. Without ND_FRESH=1 the file
 * is skipped:
 *
 *   E2E_ENV="-e ND_FRESH=1" e2e.sh <instance> --project=chromium e2e/setup-guide.spec.ts
 *
 * HTTPS cannot be set up from the browser. With ND_FRESH_HTTPS_URL (a TLS
 * proxy in front of the same instance, which trusts it: TRUST_PROXY) the
 * last test signs in through it and finds every step done.
 *
 * A remote session needs a client; the walk reports one the way a RustDesk
 * client does, to the API port (ND_API_URL, default the console's host on
 * 21121), for the computer that joined with the invite.
 */

const FRESH = process.env.ND_FRESH === '1';
test.skip(!FRESH, 'needs a fresh instance (ND_FRESH=1)');
test.describe.configure({ mode: 'serial' });

const ADMIN_FILE = process.env.ADMIN_PASSWORD_FILE ?? '/stack/secrets/admin_password';
const NEW_PASSWORD = 'Guide-walk-2026!x';
const SECOND = { username: 'second_admin', password: 'Second-admin-2026!x' };
const DEVICE = '900500001';
/** The uuid the computer joins with; a client names its device by it when it reports a session */
const DEVICE_UUID = Buffer.from('guide-walk-desk01').toString('base64');

let firstPassword = '';
let totpSecret = '';
let admin: BrowserContext;
let adminState: Awaited<ReturnType<BrowserContext['storageState']>>;

/** RFC 6238 code of a base32 secret, as an authenticator app shows it */
function totp(secret: string, at = Date.now()): string {
	const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';
	let bits = '';
	for (const c of secret.replace(/=+$/, '').toUpperCase())
		bits += alphabet.indexOf(c).toString(2).padStart(5, '0');
	const key = Buffer.from(bits.match(/.{8}/g)!.map((b) => parseInt(b, 2)));
	const counter = Buffer.alloc(8);
	counter.writeBigUInt64BE(BigInt(Math.floor(at / 30_000)));
	const mac = crypto.createHmac('sha1', key).update(counter).digest();
	const at4 = mac[mac.length - 1]! & 0xf;
	return String((mac.readUInt32BE(at4) & 0x7fffffff) % 1_000_000).padStart(6, '0');
}

/** A code with at least five seconds of its step left */
async function freshCode(secret: string): Promise<string> {
	if (30_000 - (Date.now() % 30_000) < 5_000) await new Promise((r) => setTimeout(r, 6_000));
	return totp(secret);
}

function apiURL(baseURL: string): string {
	if (process.env.ND_API_URL) return process.env.ND_API_URL;
	const u = new URL(baseURL);
	u.port = '21121';
	return u.origin;
}

async function signIn(page: Page, username: string, password: string, secret = ''): Promise<void> {
	await page.goto(`${UI}/login?autoLaunch=0`);
	await page.fill('#username', username);
	await page.fill('#password', password);
	await page.press('#password', 'Enter');
	if (secret) {
		const code = page.locator('#totp-code');
		await expect(code).toBeVisible();
		await code.fill(await freshCode(secret));
	}
	await page.waitForURL((u) => !/\/login$/.test(u.pathname));
	// The console starts on the overview; let its first requests finish before the test moves on
	await page.waitForLoadState('networkidle');
}

const guide = (page: Page) =>
	page.getByRole('region', { name: /Set up this server|Diesen Server einrichten/ });
const step = (page: Page, id: string) => guide(page).locator(`li[data-step="${id}"]`);

async function expectDone(page: Page, id: string): Promise<void> {
	// The guide asks again when the overview opens; the server decides
	await expect(step(page, id)).toHaveClass(/is-done/, { timeout: 15_000 });
	await expect(step(page, id).locator('.guide-mark')).toHaveText('check_circle');
}

/** Follows a step to its page, checks the way back is there, does fn there, and goes back */
async function viaStep(page: Page, id: string, path: RegExp, fn: () => Promise<void>): Promise<void> {
	await step(page, id).getByRole('link').click();
	await expect(page).toHaveURL(path);
	await expect(page).toHaveURL(new RegExp(`guide=${id}`));
	const back = page.getByRole('link', { name: 'Back to the setup guide' });
	await expect(back).toBeVisible();
	await fn();
	// Right after a save, as a person might: the page waits for the save
	// and leaves without asking whether to discard the changes
	await back.click();
	await expect(guide(page)).toBeVisible();
	await expect(page.getByRole('dialog', { name: 'Discard unsaved changes?' })).toHaveCount(0);
	await expectDone(page, id);
}

function pageErrors(page: Page): string[] {
	const errors: string[] = [];
	page.on('pageerror', (e) => errors.push(`${e.message} ${e.stack ?? ''}`));
	return errors;
}

test.beforeAll(async ({ browser, baseURL }) => {
	firstPassword = fs.readFileSync(ADMIN_FILE, 'utf8').trim();
	admin = await browser.newContext({ baseURL, locale: 'en-US', viewport: { width: 1440, height: 900 } });
});

test.afterAll(async () => {
	await admin?.close();
});

test('the first admin of a new install finds the guide on the overview, nothing done yet', async () => {
	const page = await admin.newPage();
	const errors = pageErrors(page);
	await signIn(page, 'admin', firstPassword);
	await expect(page).toHaveURL(/\/$/);
	await expect(guide(page)).toBeVisible();
	const rows = guide(page).locator('li[data-step]');
	await expect(rows).toHaveCount(8);
	expect(await rows.evaluateAll((els) => els.map((e) => (e as HTMLElement).dataset.step))).toEqual([
		'password',
		'two_factor',
		'address',
		'https',
		'computer',
		'session',
		'people',
		'backup'
	]);
	await expect(guide(page).locator('li.is-done')).toHaveCount(0);
	await expect(guide(page).locator('.count-line')).toHaveText(
		'0 of 8 done. A step ticks itself once the server sees it done.'
	);
	// A checklist, not a tour: nothing covers the console
	await expect(page.locator('[role="dialog"]')).toHaveCount(0);
	await expect(page.getByRole('heading', { name: 'Needs attention' })).toBeVisible();
	await expectNoAxeViolations(page);
	expect(errors).toEqual([]);
	await page.close();
});

test('each step leads to its place and back, and the server ticks it', async ({ baseURL, request }) => {
	test.setTimeout(360_000);
	const page = await admin.newPage();
	const errors = pageErrors(page);
	await page.goto(`${UI}/`);

	await viaStep(page, 'password', /\/settings\?tab=auth/, async () => {
		await page.fill('#current-password', firstPassword);
		await page.fill('#new-password', NEW_PASSWORD);
		await page.fill('#confirm-password', NEW_PASSWORD);
		const saved = page.waitForResponse(
			(r) => r.url().endsWith('/api/auth/password') && r.request().method() === 'POST'
		);
		await page.getByRole('button', { name: 'Change password' }).click();
		expect((await saved).status()).toBe(200);
	});

	await viaStep(page, 'two_factor', /\/settings\?tab=auth/, async () => {
		const setup = page.waitForResponse((r) => r.url().endsWith('/api/auth/totp/setup'));
		await page.locator('#totp-setup-btn').click();
		totpSecret = (await (await setup).json()).secret;
		expect(totpSecret).toMatch(/^[A-Z2-7]{16,}$/);
		await page.fill('#totp-setup-code', await freshCode(totpSecret));
		await page.getByRole('button', { name: 'Verify and turn on' }).click();
		await page.getByRole('button', { name: 'I have saved the codes' }).click();
	});

	await viaStep(page, 'address', /\/settings\?guide=address/, async () => {
		await page.fill('#public-server-id', 'desk.example.com');
		await page.getByRole('button', { name: 'Save changes' }).click();
		const saved = page.waitForResponse(
			(r) => r.request().method() !== 'GET' && /public-endpoints/.test(r.url())
		);
		await page.getByRole('dialog').getByRole('button', { name: 'Save addresses' }).click();
		expect((await saved).ok()).toBe(true);
	});

	// HTTPS is set up outside the console: the step leads to the docs, and plain HTTP does not tick it
	const https = step(page, 'https').getByRole('link');
	await expect(https).toHaveAttribute('href', 'https://neode.sk/docs/operations/reverse-proxy-tls');
	await expect(https).toHaveAttribute('target', '_blank');
	await expect(step(page, 'https')).not.toHaveClass(/is-done/);

	// Add your first computer opens Add a computer right on the overview
	await step(page, 'computer').getByRole('button').click();
	const sheet = page.getByRole('dialog', { name: 'Add a computer' });
	await expect(sheet).toBeVisible();
	const made = page.waitForResponse(
		(r) => r.url().endsWith('/api/onboard/invites') && r.request().method() === 'POST'
	);
	await sheet.getByRole('button', { name: 'Create invite' }).click();
	const invite = await (await made).json();
	// The computer runs the installer, which joins it with the invite's code
	const joined = await request.post(`${baseURL}/api/onboard/join`, {
		data: {
			code: invite.code,
			device_id: DEVICE,
			uuid: DEVICE_UUID,
			hostname: 'guide-desk',
			platform: 'Windows'
		}
	});
	expect(joined.status(), await joined.text()).toBe(200);
	// The sheet follows the join into the computer's first steps (L483); Skip keeps it as it joined
	const dialog = page.getByRole('dialog');
	await expect(dialog.getByRole('heading', { name: 'guide-desk joined' })).toBeVisible({ timeout: 15_000 });
	await dialog.getByRole('button', { name: 'Skip' }).click();
	await dialog.getByRole('button', { name: 'Done' }).click();
	await expect(page.getByRole('dialog')).toHaveCount(0);
	await expectDone(page, 'computer');

	// A session opens on that computer; its client reports it to the server
	const reported = await request.post(`${apiURL(baseURL!)}/api/audit/conn`, {
		data: {
			host_id: DEVICE,
			host_uuid: DEVICE_UUID,
			peer_id: '900500002',
			action: 'new',
			conn_type: 0,
			session_id: 'guide-walk'
		}
	});
	expect(reported.status(), await reported.text()).toBe(200);
	await page.reload();
	await expectDone(page, 'session');

	await viaStep(page, 'people', /\/users\?guide=people/, async () => {
		await page.getByRole('button', { name: 'Add user' }).click();
		const form = page.getByRole('dialog', { name: 'Add user' });
		await form.getByLabel('Username').fill(SECOND.username);
		await form.getByLabel('Password', { exact: true }).fill(SECOND.password);
		await form.getByText('Super admin', { exact: true }).click();
		const created = page.waitForResponse(
			(r) => r.url().endsWith('/api/users') && r.request().method() === 'POST'
		);
		await form.getByRole('button', { name: 'Create user' }).click();
		expect((await created).ok()).toBe(true);
	});

	await viaStep(page, 'backup', /\/server\/backups/, async () => {
		const download = page.waitForEvent('download');
		await page.locator('#backup-download-btn').click();
		await download;
	});

	await expect(guide(page).locator('li.is-done')).toHaveCount(7);
	await expect(step(page, 'https')).not.toHaveClass(/is-done/);
	adminState = await admin.storageState();
	expect(errors).toEqual([]);
	await page.close();
});

test('Hide closes the guide for this account; Help and Settings bring it back', async () => {
	const page = await admin.newPage();
	await page.goto(`${UI}/`);
	await guide(page).getByRole('button', { name: 'Hide the guide' }).click();
	await expect(guide(page)).toBeHidden();
	await expect(page.getByText('The setup guide is hidden. Help and Settings bring it back.')).toBeVisible();
	await page.reload();
	await expect(page.getByRole('heading', { name: 'Needs attention' })).toBeVisible();
	await expect(guide(page)).toBeHidden();

	await page.getByRole('button', { name: 'Help', exact: true }).click();
	await page.getByRole('button', { name: 'Show the setup guide' }).click();
	await expect(guide(page)).toBeVisible();
	await expect(page.getByRole('button', { name: 'Show the setup guide' })).toBeHidden();

	await guide(page).getByRole('button', { name: 'Hide the guide' }).click();
	await expect(guide(page)).toBeHidden();
	await page.goto(`${UI}/settings`);
	await page.getByRole('button', { name: 'Show the setup guide' }).click();
	await expect(page).toHaveURL(/\/$/);
	await expect(guide(page)).toBeVisible();
	await page.close();
});

test('the second admin gets no guide unasked, but may ask for it', async ({ browser, baseURL }) => {
	const ctx = await browser.newContext({ baseURL, locale: 'en-US' });
	const page = await ctx.newPage();
	await signIn(page, SECOND.username, SECOND.password);
	await page.goto(`${UI}/`);
	await expect(page.getByRole('heading', { name: 'Needs attention' })).toBeVisible();
	await expect(guide(page)).toBeHidden();
	await page.getByRole('button', { name: 'Help', exact: true }).click();
	await expect(page.getByRole('button', { name: 'Show the setup guide' })).toBeVisible();
	await ctx.close();
});

const VIEWS = [
	{ width: 1440, height: 900 },
	{ width: 390, height: 844 }
] as const;

for (const size of VIEWS) {
	for (const scheme of ['light', 'dark'] as const) {
		for (const locale of ['en-US', 'de-DE'] as const) {
			test(`the guide at ${size.width} px, ${scheme}, ${locale}`, async ({ browser, baseURL }) => {
				const ctx = await viewContext(browser, baseURL!, size, scheme, locale);
				const page = await ctx.newPage();
				const errors = pageErrors(page);
				const german = locale === 'de-DE';
				await page.goto(`${UI}/`);
				await expect(guide(page)).toBeVisible();
				await expect(guide(page).getByRole('heading', { level: 2 })).toHaveText(
					german ? 'Diesen Server einrichten' : 'Set up this server'
				);
				await expect(page.locator('html')).toHaveAttribute('lang', german ? 'de' : 'en');
				// Nothing wider than the screen, and every row a full-width target
				expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(
					size.width
				);
				for (const box of await guide(page)
					.locator('.guide-row')
					.evaluateAll((els) => els.map((e) => e.getBoundingClientRect().height)))
					expect(box).toBeGreaterThanOrEqual(44);
				await expectNoAxeViolations(page);

				// A step's page offers the way back in the same language. The page opens
				// scrolled down to the step's setting; on a phone the way back stays on the screen
				await step(page, 'two_factor').getByRole('link').click();
				const back = page.getByRole('link', {
					name: german ? 'Zurück zur Einrichtungsanleitung' : 'Back to the setup guide'
				});
				await expect(back).toBeVisible();
				await expect(page.locator('#auth-panel-totp')).toBeInViewport();
				if (size.width < 768) await expect(back).toBeInViewport();
				expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(
					size.width
				);
				await expectNoAxeViolations(page, '.guide-return');
				await back.click();
				await expect(guide(page)).toBeVisible();
				expect(errors).toEqual([]);
				await ctx.close();
			});
		}
	}
}

async function viewContext(
	browser: Browser,
	baseURL: string,
	viewport: { width: number; height: number },
	colorScheme: 'light' | 'dark',
	locale: string
): Promise<BrowserContext> {
	const phone = viewport.width < 600;
	return browser.newContext({
		baseURL,
		viewport,
		colorScheme,
		locale,
		// The session only: the browser's saved language would win over the locale
		storageState: { cookies: adminState.cookies, origins: [] },
		...(phone && browser.browserType().name() !== 'firefox' ? { isMobile: true, hasTouch: true } : {})
	});
}

test('over HTTPS the last step ticks too, and the guide says everything is done', async ({ browser }) => {
	const https = process.env.ND_FRESH_HTTPS_URL;
	test.skip(!https, 'needs ND_FRESH_HTTPS_URL');
	const ctx = await browser.newContext({ baseURL: https, ignoreHTTPSErrors: true, locale: 'en-US' });
	const page = await ctx.newPage();
	await signIn(page, 'admin', NEW_PASSWORD, totpSecret);
	await page.goto(`${UI}/`);
	await expectDone(page, 'https');
	await expect(guide(page).locator('li.is-done')).toHaveCount(8);
	await expect(guide(page).getByText('All steps are done. You can hide the guide now.')).toBeVisible();
	await ctx.close();
});
