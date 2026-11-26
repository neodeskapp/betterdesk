/**
 * The main tasks with the keyboard alone: Tab, Shift+Tab, Enter, Space,
 * arrows and Escape, never a click. Controls are found by role and name,
 * as a screen reader user finds them, and every result a screen reader
 * must hear is checked in the announcer's live regions.
 *
 * Tasks that need a computer on the other end (a remote session, the
 * client side of quick support) or the website are covered elsewhere:
 * remote.spec.ts (the viewer with the mock device), the client's own
 * tests and the lab walkthroughs (.ui-rework/go/part10/a11y).
 */
import type { Locator, Page } from '@playwright/test';
import { test, expect } from './fixtures.ts';
import { UI } from './roles.ts';

/** Press Tab (or Shift+Tab) until the target has focus; fails when it never gets it */
async function tabTo(page: Page, target: Locator, { max = 120, back = false } = {}): Promise<void> {
	await expect(target).toBeVisible({ timeout: 15_000 });
	for (let i = 0; i < max; i++) {
		if (await target.evaluate((el) => el === document.activeElement)) return;
		await page.keyboard.press(back ? 'Shift+Tab' : 'Tab');
	}
	expect(
		await target.evaluate((el) => el === document.activeElement),
		`${target} reached with the keyboard`
	).toBe(true);
}

/** What the screen reader heard last, politely or right away */
function announced(page: Page) {
	return {
		polite: page.locator('[data-announcer="polite"]'),
		assertive: page.locator('[data-announcer="assertive"]')
	};
}

test.describe('keyboard only', () => {
	test.describe.configure({ timeout: 120_000 });

	test('find a computer and read its details', async ({ page }) => {
		await page.goto(`${UI}/devices`);
		const main = page.locator('#main-content');
		await tabTo(page, main.getByRole('searchbox').first());
		await page.keyboard.type('demo-db-primary');
		const row = main.getByRole('button', { name: /^demo-db-primary/ }).first();
		await tabTo(page, row);
		await page.keyboard.press('Enter');
		const details = page.getByRole('dialog', { name: /demo-db-primary/ });
		await expect(details).toBeVisible();
		// Focus went into the details and stays there
		await expect.poll(() => details.evaluate((d) => d.contains(document.activeElement))).toBe(true);
		await page.keyboard.press('Tab');
		await expect.poll(() => details.evaluate((d) => d.contains(document.activeElement))).toBe(true);
		await page.keyboard.press('Escape');
		await expect(details).toBeHidden();
		await expect(row).toBeFocused();
	});

	test('add a computer with an invite', async ({ page }) => {
		await page.goto(`${UI}/devices/add`);
		const main = page.locator('#main-content');
		await tabTo(page, main.getByRole('tab', { name: 'Invites and install links' }));
		await tabTo(page, main.getByRole('button', { name: 'Add a computer' }));
		await page.keyboard.press('Enter');
		const dialog = page.getByRole('dialog').first();
		await expect(dialog).toBeVisible();
		await expect.poll(() => dialog.evaluate((d) => d.contains(document.activeElement))).toBe(true);
		// The dialog's main action, reached with Tab
		const create = dialog.getByRole('button', { name: /create|add|make/i }).last();
		await tabTo(page, create);
		await page.keyboard.press('Enter');
		// The invite is there as text to copy, with a named copy button
		await expect(page.getByRole('button', { name: /copy/i }).first()).toBeVisible({ timeout: 15_000 });
		await page.keyboard.press('Escape');
	});

	test('answer a help request', async ({ page }) => {
		await page.goto(`${UI}/help-requests`);
		const main = page.locator('#main-content');
		const accept = main.getByRole('button', { name: 'Accept' }).first();
		await tabTo(page, accept);
		await page.keyboard.press('Enter');
		// Said politely; nothing went wrong
		await expect(announced(page).polite).not.toHaveText('', { timeout: 10_000 });
		await expect(announced(page).assertive).toHaveText('');
	});

	test('send a chat message', async ({ page }) => {
		await page.goto(`${UI}/chat`);
		const main = page.locator('#main-content');
		const conversation = main.getByRole('button', { name: /^demo-sales-laptop-01/ }).first();
		await tabTo(page, conversation);
		await page.keyboard.press('Enter');
		const field = main.getByRole('textbox', { name: /message/i }).first();
		await tabTo(page, field);
		const text = `Keyboard check ${Date.now()}`;
		await page.keyboard.type(text);
		await page.keyboard.press('Enter');
		await expect(main.getByText(text)).toBeVisible({ timeout: 10_000 });
		// The attach button is a named button, so a file goes without dragging
		await expect(main.getByRole('button', { name: /attach|file/i }).first()).toBeVisible();
	});

	test('change a setting and hear that it was saved', async ({ page }) => {
		await page.goto(`${UI}/settings`);
		const main = page.locator('#main-content');
		const direct = main.getByRole('radio', { name: /direct first/i });
		const relay = main.getByRole('radio', { name: /always through the relay/i });
		const status = page.locator('.save-bar [aria-live="polite"]');
		const save = page.getByRole('button', { name: 'Save changes' });
		await expect(direct).toBeChecked({ timeout: 15_000 });
		try {
			// A radio group: Tab reaches the chosen option, the arrow keys choose
			await tabTo(page, direct);
			await page.keyboard.press('ArrowDown');
			await expect(relay).toBeChecked();
			await tabTo(page, save);
			await page.keyboard.press('Enter');
			// The save bar's own status line is a polite live region
			await expect(status).toContainText(/saved/i, { timeout: 15_000 });
		} finally {
			// Back to the default
			await direct.check();
			await save.press('Enter');
			await expect(status).toContainText(/saved/i, { timeout: 15_000 });
		}
	});

	test('create a user, starting with the mistakes', async ({ page }) => {
		await page.goto(`${UI}/users`);
		await tabTo(page, page.getByRole('button', { name: 'Add user' }));
		await page.keyboard.press('Enter');
		const dialog = page.getByRole('dialog', { name: /user/i });
		await expect(dialog).toBeVisible();
		// Submit empty: focus lands on the first field that needs a value, which says why
		const submit = dialog.getByRole('button', { name: /add|create|invite|save/i }).last();
		await tabTo(page, submit);
		await page.keyboard.press('Enter');
		const invalid = dialog.locator('[aria-invalid="true"]').first();
		await expect(invalid).toBeFocused();
		await expect(invalid).toHaveAccessibleDescription(/.+/);
		const name = `kb_${Date.now()}`;
		await page.keyboard.type(name);
		// Submit again: each time focus goes to the next field that still needs a value
		for (let round = 0; round < 4 && (await dialog.isVisible()); round++) {
			await tabTo(page, submit);
			await page.keyboard.press('Enter');
			await page.waitForTimeout(500);
			if (!(await dialog.isVisible())) break;
			const next = dialog.locator('[aria-invalid="true"]').first();
			if (!(await next.count())) continue;
			await expect(next).toBeFocused();
			const type = await next.getAttribute('type');
			await page.keyboard.type(type === 'email' ? `${name}@example.test` : 'Kb-check-2026-pass!');
		}
		await expect(dialog).toBeHidden({ timeout: 15_000 });
		await expect(page.locator('#main-content').getByRole('button', { name, exact: true })).toBeVisible();
	});
});

test.describe('signed out, keyboard only', () => {
	test.use({ storageState: { cookies: [], origins: [] }, allowProblems: [/(http|status of) 40[01]/] });

	test('read and act on a sign-in error', async ({ page }) => {
		await page.goto(`${UI}/login?autoLaunch=0`);
		const user = page.getByRole('textbox', { name: /username/i });
		await expect(user).toBeFocused({ timeout: 15_000 });
		await page.keyboard.type('nobody-kb');
		await page.keyboard.press('Tab');
		await page.keyboard.type('wrong password');
		await page.keyboard.press('Enter');
		// The error is said right away, and the password is selected to type it again
		const alert = page.getByRole('alert').first();
		await expect(alert).toBeVisible({ timeout: 10_000 });
		await expect(alert).not.toHaveText('');
		await expect(page.locator('#password')).toBeFocused();
	});

	test('sign in with a two-factor code, pasted', async ({ page }) => {
		await page.route('**/api/auth/login', (r) =>
			r.fulfill({
				status: 200,
				contentType: 'application/json',
				body: '{"success":true,"totpRequired":true}'
			})
		);
		let sent = '';
		await page.route('**/api/auth/totp/verify', async (r) => {
			sent = r.request().postData() ?? '';
			await r.fulfill({
				status: 401,
				contentType: 'application/json',
				body: '{"success":false,"error":"The code is not valid. Check the time on your phone and try again."}'
			});
		});
		await page.goto(`${UI}/login?autoLaunch=0`);
		await expect(page.getByRole('textbox', { name: /username/i })).toBeFocused({ timeout: 15_000 });
		await page.keyboard.type('someone');
		await page.keyboard.press('Tab');
		await page.keyboard.type('a password');
		await page.keyboard.press('Enter');
		const code = page.getByRole('textbox', { name: /code/i }).first();
		await expect(code).toBeFocused();
		await expect(code).toHaveAttribute('autocomplete', 'one-time-code');
		// A pasted code (insertText is what a paste or a password manager does)
		await page.keyboard.insertText('123 456');
		await expect.poll(() => sent).toContain('123456');
		await expect(page.getByRole('alert').first()).toContainText(/code/i);
	});
});
