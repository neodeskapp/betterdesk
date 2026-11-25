/**
 * The chat (A242) with two console accounts and a simulated computer: the
 * computer's chat window is a socket of version 2 with the stack's device
 * token (/stack/secrets/device_token, bound to 900100001). Support: what
 * the computer writes reaches the inbox with an unread count, an answer
 * reaches the computer, taking over and closing work. Team: a direct
 * message reaches the colleague, whose unread count is exact across
 * reloads. The panel opens over another page. Every text is new per run,
 * so runs in both browsers and again on the same stack don't see each
 * other's messages.
 */
import fs from 'node:fs';
import type { Browser, Page } from '@playwright/test';
import { test, expect, expectNoAxeViolations } from './fixtures.ts';
import { UI, authFile } from './roles.ts';

// Each browser has its own computer, so the two runs don't read each other's messages
const DEVICES: Record<string, { id: string; token: string }> = {
	chromium: { id: '900100001', token: '/stack/secrets/device_token' },
	firefox: { id: '900100002', token: '/stack/secrets/device_token_other' }
};
const TOKEN_FILE = '/stack/secrets/device_token';

/** A computer's chat window: sends frames, keeps what it gets */
class Device {
	frames: Record<string, unknown>[] = [];
	private ws: WebSocket;
	private constructor(ws: WebSocket) {
		this.ws = ws;
		ws.onmessage = (e) => this.frames.push(JSON.parse(String(e.data)));
	}
	static async open(baseURL: string, which: { id: string; token: string }): Promise<Device> {
		const token = fs.readFileSync(which.token, 'utf8').trim();
		const url = `${baseURL.replace(/^http/, 'ws')}/ws/chat/${which.id}?v=2&token=${encodeURIComponent(token)}`;
		const ws = new WebSocket(url);
		const d = new Device(ws);
		await new Promise<void>((ok, bad) => {
			ws.onopen = () => ok();
			ws.onerror = () => bad(new Error('the device socket did not open'));
		});
		await expect.poll(() => d.frames.some((f) => f.type === 'welcome')).toBe(true);
		return d;
	}
	send(frame: Record<string, unknown>) {
		this.ws.send(JSON.stringify(frame));
	}
	/** The texts of the messages it got */
	texts(): string[] {
		return this.frames
			.filter((f) => f.type === 'message.new')
			.map((f) => String((f.message as { text?: string }).text ?? ''));
	}
	close() {
		this.ws.close();
	}
}

const stamp = () => `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`;

/** The colleague of each browser's run: two pairs, so the runs don't read each other's messages */
const COLLEAGUE: Record<string, 'operator' | 'viewer'> = { chromium: 'operator', firefox: 'viewer' };

async function asColleague(browser: Browser, baseURL: string, role: 'operator' | 'viewer'): Promise<Page> {
	const ctx = await browser.newContext({ baseURL, storageState: authFile(role) });
	return ctx.newPage();
}

test.describe('chat', () => {
	test.skip(!fs.existsSync(TOKEN_FILE), 'no device token on this stack');
	test.describe.configure({ mode: 'serial' });

	test('support: a computer writes, the inbox shows it unread, the answer reaches the computer', async ({
		page,
		baseURL
	}, testInfo) => {
		const which = DEVICES[testInfo.project.name] ?? DEVICES.chromium!;
		const DEVICE = which.id;
		const device = await Device.open(baseURL ?? '', which);
		const hello = `printer stopped ${stamp()}`;
		await page.goto(`${UI}/chat?s=support`);
		await expect(page.locator('.chat-list .rows, .chat-list .chat-list-empty').first()).toBeVisible({
			timeout: 20_000
		});
		device.send({ type: 'send', text: hello, client_id: 'e2e' });
		const row = page.locator('.chat-list .row', { hasText: hello });
		await expect(row).toBeVisible({ timeout: 15_000 });
		await expect(row.locator('.row-badge')).toBeVisible();
		await expect(page.locator('.chat-btn-count')).toBeVisible();
		await row.click();
		await expect(page).toHaveURL(new RegExp(`c=${DEVICE}`));
		await expect(page.locator('.chat-msg', { hasText: hello })).toBeVisible();
		// Read once in view: the row loses its count
		await expect(row.locator('.row-badge')).toHaveCount(0, { timeout: 10_000 });
		await expectNoAxeViolations(page);

		const answer = `looking at it ${stamp()}`;
		const field = page.getByRole('textbox', { name: /^Message to / });
		await field.fill(answer);
		await field.press('Enter');
		await expect(page.locator('.chat-msg.is-mine', { hasText: answer })).toBeVisible();
		await expect.poll(() => device.texts()).toContain(answer);
		// The computer reads it: "Seen"
		device.send({ type: 'read' });
		await expect(page.locator('.chat-msg.is-mine', { hasText: answer }).getByText('Seen')).toBeVisible();

		// Take it and close it with a note
		const take = page.getByRole('button', { name: 'Take over' });
		if (await take.count()) await take.click();
		await expect(page.getByText('You have it').first()).toBeVisible();
		await expect.poll(() => device.frames.filter((f) => f.type === 'helper').at(-1)?.name).toBeTruthy();
		await page.getByRole('button', { name: 'Close', exact: true }).first().click();
		await page.getByRole('dialog').getByRole('textbox').fill('driver reinstalled');
		await expectNoAxeViolations(page);
		await page.getByRole('dialog').getByRole('button', { name: 'Close', exact: true }).last().click();
		await expect(page.getByText(/closed the conversation: driver reinstalled/)).toBeVisible();
		device.close();
	});

	test('team: a direct message reaches the colleague, unread is exact across reloads', async ({
		page,
		browser,
		baseURL
	}, testInfo) => {
		const role = COLLEAGUE[testInfo.project.name] ?? 'operator';
		const operator = await asColleague(browser, baseURL ?? '', role);
		await operator.goto(`${UI}/chat?s=team`);
		await expect(operator.locator('.chat-list')).toBeVisible();
		await page.goto(`${UI}/chat?s=team`);
		// Admin writes to the operator from the colleagues list
		const colleague = page.locator('.chat-list .row', { hasText: `e2e_${role}` }).first();
		await expect(colleague).toBeVisible({ timeout: 20_000 });
		await colleague.click();
		const text = `lunch ${stamp()}`;
		const field = page.getByRole('textbox', { name: /^Message to / });
		await field.fill(text);
		await field.press('Enter');
		await expect(page.locator('.chat-msg.is-mine', { hasText: text })).toBeVisible();

		const row = operator.locator('.chat-list .row', { hasText: text });
		await expect(row).toBeVisible({ timeout: 15_000 });
		await expect(row.locator('.row-badge')).toBeVisible();
		await operator.reload();
		await expect(operator.locator('.chat-list .row', { hasText: text }).locator('.row-badge')).toBeVisible({
			timeout: 15_000
		});
		await operator.locator('.chat-list .row', { hasText: text }).click();
		await expect(operator.locator('.chat-msg', { hasText: text })).toBeVisible();
		await operator.goto(`${UI}/chat?s=team`);
		await expect(operator.locator('.chat-list .row', { hasText: text })).toBeVisible({ timeout: 15_000 });
		await expect(operator.locator('.chat-list .row', { hasText: text }).locator('.row-badge')).toHaveCount(0);
		await operator.context().close();
	});

	test('search: a message is found by its text and opens marked in its conversation', async ({
		page,
		baseURL
	}, testInfo) => {
		const which = DEVICES[testInfo.project.name] ?? DEVICES.chromium!;
		const device = await Device.open(baseURL ?? '', which);
		const word = `toner${stamp()}`;
		device.send({ type: 'send', text: `the ${word} is empty`, client_id: 'e2e-s1' });
		// Newer messages after it, so it is not the newest line
		for (let i = 0; i < 3; i++)
			device.send({ type: 'send', text: `later line ${i} ${stamp()}`, client_id: `e2e-s${i + 2}` });
		await page.goto(`${UI}/chat?s=support`);
		await expect(page.locator('.chat-list .rows, .chat-list .chat-list-empty').first()).toBeVisible({
			timeout: 20_000
		});
		await page.getByRole('searchbox', { name: /Find a computer/ }).fill(word.toUpperCase());
		const found = page.getByRole('list', { name: 'Messages' }).locator('.row');
		await expect(found).toHaveCount(1, { timeout: 15_000 });
		await expect(found.locator('mark')).toHaveText(word);
		await expectNoAxeViolations(page);
		await found.click();
		await expect(page).toHaveURL(new RegExp(`c=${which.id}`));
		await expect(page.locator('.chat-msg.is-marked', { hasText: word })).toBeVisible();
		device.close();
	});

	test('panel: the chat opens over another page and from a computer', async ({ page }) => {
		await page.goto(`${UI}/devices`);
		await page.locator('.chat-btn').click();
		const panel = page.getByRole('dialog', { name: 'Chat' });
		await expect(panel).toBeVisible();
		await expect(panel.locator('.chat-list')).toBeVisible();
		await expectNoAxeViolations(page);
		await page.keyboard.press('Escape');
		await expect(panel).toHaveCount(0);
	});
});
