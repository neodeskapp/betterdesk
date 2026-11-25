/**
 * Several sessions per account (A239): an account signed in on two
 * browsers stays signed in on both; Signed-in devices in the account menu
 * lists them with "This browser" marked, and Sign out on the other entry
 * sends exactly that browser to the sign-in page with a plain message. On
 * the user's page an admin sees the account's sessions and signs it out
 * everywhere; that browser is told an administrator did it.
 *
 * The spec makes its own account (one per browser project, deleted at the
 * end), so it never signs out the shared e2e sessions. It needs the
 * server's default of several sessions per account (SESSION_MAX_PER_USER
 * empty): with a limit of one it skips.
 */
import type { Browser, BrowserContext, Page } from '@playwright/test';
import { test, expect } from './fixtures.ts';
import { UI } from './roles.ts';

async function bootstrap(page: Page): Promise<{ csrf: string; maxSessions: number }> {
	await page.goto(`${UI}/`);
	return page.evaluate(async () => {
		const r = await fetch('/api/console/bootstrap', {
			credentials: 'same-origin',
			headers: { Accept: 'application/json' }
		});
		const b = await r.json();
		return { csrf: b.csrfToken, maxSessions: b.sessionPolicy?.maxSessionsPerUser ?? 0 };
	});
}

/** A request with the admin page's session, outside the page */
function send(page: Page, csrf: string, method: string, path: string, body?: unknown) {
	return page.request.fetch(path, {
		method,
		headers: { Accept: 'application/json', 'Content-Type': 'application/json', 'X-CSRF-Token': csrf },
		data: body === undefined ? undefined : JSON.stringify(body)
	});
}

/** A browser of its own, signed in as the account through the sign-in page */
async function signedInBrowser(browser: Browser, baseURL: string, username: string, password: string) {
	const context = await browser.newContext({ baseURL, storageState: { cookies: [], origins: [] } });
	const page = await context.newPage();
	await page.goto(`${UI}/login?autoLaunch=0`);
	await page.fill('#username', username);
	await page.fill('#password', password);
	await page.press('#password', 'Enter');
	await page.waitForURL((u) => !/\/login$/.test(u.pathname), { timeout: 15_000 });
	return { context, page };
}

/**
 * Open a console page and land on the sign-in page. The browser may have
 * left for it already (a request noticed the ended session first): a page
 * opened now is told the same reason, so the check never depends on which
 * request was first.
 */
async function openAPage(p: Page): Promise<void> {
	await p.goto(`${UI}/devices`);
	await p.waitForURL(/\/login\?/);
}

async function stillSignedIn(context: BrowserContext): Promise<boolean> {
	return (await context.request.get('/api/auth/verify', { headers: { Accept: 'application/json' } })).ok();
}

test('one account on two browsers: both stay signed in, and each can be signed out from elsewhere', async ({
	page,
	browser,
	browserName,
	baseURL
}) => {
	test.setTimeout(90_000);
	const { csrf, maxSessions } = await bootstrap(page);
	test.skip(maxSessions === 1, 'the server allows one session per account (SESSION_MAX_PER_USER=1)');

	const username = `e2e_sessions_${browserName}`;
	const password = `Sess-${Math.random().toString(36).slice(2, 10)}!x7`;
	// A leftover of an earlier run goes first
	const list = await (await send(page, csrf, 'GET', '/api/users')).json();
	const old = list.data.users.find((u: { username: string }) => u.username === username);
	if (old) await send(page, csrf, 'DELETE', `/api/users/${old.id}`);
	const created = await send(page, csrf, 'POST', '/api/users', { username, password, role: 'viewer' });
	expect(created.ok()).toBe(true);
	const userId = (await created.json()).data.id;

	const first = await signedInBrowser(browser, baseURL!, username, password);
	const second = await signedInBrowser(browser, baseURL!, username, password);
	try {
		// A second sign-in no longer ends the first
		expect(await stillSignedIn(first.context)).toBe(true);
		expect(await stillSignedIn(second.context)).toBe(true);

		// The first browser's list: two entries, its own marked
		const a = first.page;
		await a
			.getByRole('button', { name: new RegExp(username) })
			.first()
			.click();
		await a.getByRole('menuitem', { name: 'Signed-in devices' }).click();
		const sheet = a.getByRole('dialog', { name: 'Signed-in devices' });
		await expect(sheet.getByRole('listitem')).toHaveCount(2);
		const mine = sheet.getByRole('listitem').filter({ hasText: 'This browser' });
		await expect(mine).toHaveCount(1);
		await expect(mine.getByRole('button', { name: /^Sign out/ })).toHaveCount(0);

		// Sign out the other one
		await sheet
			.getByRole('listitem')
			.filter({ hasNotText: 'This browser' })
			.getByRole('button', { name: /^Sign out/ })
			.click();
		await a.getByRole('dialog').last().getByRole('button', { name: 'Sign out' }).click();
		await expect(sheet.getByRole('listitem')).toHaveCount(1);
		expect(await stillSignedIn(first.context)).toBe(true);

		// The second browser goes to the sign-in page at its next page and is
		// told why, also when a poll or a closed socket noticed first
		await openAPage(second.page);
		await expect(second.page.getByRole('status')).toHaveText(
			'You were signed out from another device. Please sign in again.'
		);
		expect(await stillSignedIn(second.context)).toBe(false);

		// The admin ends the first browser's session from the user's page
		await page.goto(`${UI}/users?search=${username}`);
		await page.getByRole('button', { name: `More actions for ${username}` }).click();
		await page.getByRole('menuitem', { name: 'Signed-in devices…' }).click();
		const userSheet = page.getByRole('dialog', { name: `Signed-in devices of ${username}` });
		await expect(userSheet.getByRole('listitem')).toHaveCount(1);
		await userSheet.getByRole('button', { name: 'Sign out everywhere' }).click();
		await page.getByRole('dialog').last().getByRole('button', { name: 'Sign out everywhere' }).click();
		await expect(userSheet.getByText(`${username} is not signed in anywhere.`)).toBeVisible();

		await openAPage(a);
		await expect(a.getByRole('status')).toHaveText('An administrator signed you out. Please sign in again.');
	} finally {
		await first.context.close();
		await second.context.close();
		await send(page, csrf, 'DELETE', `/api/users/${userId}`);
	}
});
