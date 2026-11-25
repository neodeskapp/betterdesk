/**
 * The guided first join and the invite page (L483). An admin makes an
 * invite in Add a computer; the computer's join is the request its client
 * sends (POST /api/onboard/join, no client involved); the invite sheet
 * follows it and walks through name, folder, how you reach it and a test
 * session. The invite page offers the download without JavaScript and fits
 * a phone. With SHOTS set, the steps are also saved as screenshots, light
 * and dark, at desktop and phone width, into test-results/onboarding/.
 * The computer is deleted again at the end.
 */
import type { Page } from '@playwright/test';
import { test, expect } from './fixtures.ts';
import { UI } from './roles.ts';

const SHOTS = !!process.env.SHOTS;

/** The parts of the answers this spec reads */
type Answer = { code?: string; invite?: { id: string } } | null;

async function csrf(page: Page): Promise<string> {
	return page.evaluate(async () => {
		const r = await fetch('/api/console/bootstrap', {
			credentials: 'same-origin',
			headers: { Accept: 'application/json' }
		});
		return (await r.json()).csrfToken as string;
	});
}

async function call(
	page: Page,
	method: string,
	path: string,
	body?: unknown
): Promise<{ status: number; json: Answer }> {
	const token = await csrf(page);
	return page.evaluate(
		async ({ method, path, token, body }) => {
			const r = await fetch(path, {
				method,
				credentials: 'same-origin',
				headers: { Accept: 'application/json', 'Content-Type': 'application/json', 'X-CSRF-Token': token },
				body: body === undefined ? undefined : JSON.stringify(body)
			});
			return { status: r.status, json: await r.json().catch(() => null) };
		},
		{ method, path, token, body }
	);
}

async function shot(page: Page, name: string) {
	if (!SHOTS) return;
	const scheme = (await page.evaluate(() => matchMedia('(prefers-color-scheme: dark)').matches))
		? 'dark'
		: 'light';
	const width = page.viewportSize()?.width ?? 0;
	await page.screenshot({
		path: `test-results/onboarding/${name}-${width < 600 ? 'phone' : 'desktop'}-${scheme}.png`,
		fullPage: true
	});
}

/** A device ID nobody else uses in this run */
function deviceId(): string {
	return '9006' + String(Date.now() % 100000).padStart(5, '0');
}

test.describe('Add a computer, guided', () => {
	test.use({ role: 'admin' });

	for (const look of SHOTS ? (['light', 'dark'] as const) : (['light'] as const)) {
		for (const width of SHOTS ? [1440, 390] : [1440]) {
			test(`from the invite to the test session (${look}, ${width})`, async ({ page, request, baseURL }) => {
				await page.emulateMedia({ colorScheme: look });
				await page.setViewportSize({ width, height: width < 600 ? 844 : 900 });
				await page.goto(`${UI}/devices`);
				await page.getByRole('button', { name: 'Add a computer' }).first().click();
				const sheet = page.getByRole('dialog', { name: 'Add a computer' });
				await expect(sheet).toBeVisible();
				await sheet.getByLabel('Name', { exact: true }).fill("Anna's laptop");
				const made = page.waitForResponse(
					(r) => r.url().endsWith('/api/onboard/invites') && r.request().method() === 'POST'
				);
				await sheet.getByRole('button', { name: 'Create invite' }).click();
				const invite = await (await made).json();
				const dialog = page.getByRole('dialog');
				await expect(
					dialog.getByText(/^Still waiting for Anna's laptop\. The link works until /)
				).toBeVisible();
				await shot(page, '30-sheet-waiting');

				// The computer joins: the request its NeoDesk sends with the invite's code
				const id = deviceId();
				const joined = await request.post(`${baseURL}/api/onboard/join`, {
					data: {
						code: invite.code,
						device_id: id,
						uuid: `uuid-${id}`,
						hostname: 'anna-pc',
						platform: 'Windows'
					}
				});
				expect(joined.status(), await joined.text()).toBe(200);
				try {
					await expect(dialog.getByRole('heading', { name: "Anna's laptop joined" })).toBeVisible({
						timeout: 15_000
					});
					await expect(dialog.getByText('Step 1 of 4')).toBeVisible();
					await shot(page, '31-sheet-name');

					const name = dialog.getByLabel('Name', { exact: true });
					await expect(name).toHaveValue("Anna's laptop");
					await name.fill('Anna PC');
					await dialog.getByRole('button', { name: 'Next' }).click();

					await expect(dialog.getByText('Step 2 of 4')).toBeVisible();
					await shot(page, '32-sheet-folder');
					await dialog.getByRole('button', { name: 'Next' }).click();

					await expect(dialog.getByText('Step 3 of 4')).toBeVisible();
					await expect(dialog.getByText(/^Without a password: the person at the computer/)).toBeVisible();
					await expect(dialog.getByText(/^With its password: someone at the computer/)).toBeVisible();
					await shot(page, '33-sheet-reach');
					await dialog.getByRole('button', { name: 'Next' }).click();

					await expect(dialog.getByText('Step 4 of 4')).toBeVisible();
					await shot(page, '34-sheet-test');
					const viewer = page.waitForEvent('popup');
					await dialog.getByRole('button', { name: 'Start test session' }).click();
					const tab = await viewer;
					expect(new URL(tab.url()).pathname).toBe(`${UI}/remote/${id}`);
					await tab.close();
					await dialog.getByRole('button', { name: 'Done', exact: true }).click();
					await expect(dialog.getByText('Anna PC is ready.')).toBeVisible();
					await shot(page, '35-sheet-done');

					const device = await call(page, 'GET', `/api/devices/${id}`);
					expect(JSON.stringify(device.json)).toContain('Anna PC');
				} finally {
					await call(page, 'DELETE', `/api/devices/${id}?hard=true`);
				}
			});
		}
	}
});

test.describe('the invite page', () => {
	test.use({ role: 'admin' });

	test('offers the download without JavaScript and fits a phone', async ({ page, browser, baseURL }) => {
		await page.goto(`${UI}/devices`);
		const made = await call(page, 'POST', '/api/onboard/invites', { name: 'Page check' });
		expect(made.status).toBe(201);
		const code = made.json?.code ?? '';
		try {
			for (const look of SHOTS ? (['light', 'dark'] as const) : (['light'] as const)) {
				const plain = await browser.newContext({
					javaScriptEnabled: false,
					colorScheme: look,
					userAgent:
						'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130 Safari/537.36',
					viewport: { width: 390, height: 844 }
				});
				const p = await plain.newPage();
				await p.goto(`${baseURL}${UI}/join/${code}`);
				await expect(
					p.getByRole('heading', { level: 1, name: 'Set up remote support on this computer' })
				).toBeVisible();
				for (const step of ['Download and open NeoDesk.', 'Click Install.'])
					await expect(p.getByText(step, { exact: true })).toBeVisible();
				await expect(p.getByText(code.slice(0, 4) + '-' + code.slice(4))).toBeVisible();
				expect(await p.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
				await shot(p, '20-page-nojs');
				await plain.close();
			}
			for (const look of SHOTS ? (['light', 'dark'] as const) : (['light'] as const)) {
				for (const width of SHOTS ? [1440, 390] : [390]) {
					await page.emulateMedia({ colorScheme: look });
					await page.setViewportSize({ width, height: 900 });
					await page.goto(`${UI}/join/${code}`);
					await expect(page.getByRole('list').filter({ hasText: 'Click Install.' })).toBeVisible();
					expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
					await shot(page, '21-page');
				}
			}
		} finally {
			await page.goto(`${UI}/devices`);
			await call(page, 'DELETE', `/api/onboard/invites/${made.json?.invite?.id}`);
		}
	});
});
