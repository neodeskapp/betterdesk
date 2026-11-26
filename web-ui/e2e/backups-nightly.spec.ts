/**
 * Task 3 of the first-time admin: back up the server every night and keep
 * 14 copies. On Server › Backups: turn on scheduled backups, every day at
 * 02:00 in the server's time zone, keep 14, one Save. The next backup is
 * then at 02:00, not counted from the moment of saving, and the page says
 * when. Once with the mouse and once by keyboard alone. Each test puts the
 * schedule back as it found it.
 */
import type { Locator, Page } from '@playwright/test';
import { test, expect, expectNoAxeViolations } from './fixtures.ts';
import { UI } from './roles.ts';

const START = { timeout: 20000 };

interface ScheduleAnswer {
	schedule: Record<string, unknown> & { timeOfDay?: string; retentionCount?: number; enabled?: boolean };
	next: { at: string | null; timeZone: string };
}

async function schedule(page: Page): Promise<ScheduleAnswer> {
	return page.evaluate(async () => {
		const r = await fetch('/api/settings/backup/schedule', {
			credentials: 'same-origin',
			headers: { Accept: 'application/json' }
		});
		return (await r.json()).data;
	});
}

async function putSchedule(page: Page, body: unknown): Promise<number> {
	return page.evaluate(async (body) => {
		const boot = await (
			await fetch('/api/console/bootstrap', {
				credentials: 'same-origin',
				headers: { Accept: 'application/json' }
			})
		).json();
		const r = await fetch('/api/settings/backup/schedule', {
			method: 'PUT',
			credentials: 'same-origin',
			headers: {
				Accept: 'application/json',
				'Content-Type': 'application/json',
				'X-CSRF-Token': boot.csrfToken as string
			},
			body: JSON.stringify(body)
		});
		return r.status;
	}, body);
}

/** Tab through the page until `target` has the focus */
async function tabTo(page: Page, target: Locator, max = 80): Promise<void> {
	for (let i = 0; i < max; i++) {
		if (await target.evaluate((el) => el === document.activeElement)) return;
		await page.keyboard.press('Tab');
	}
	throw new Error('Tab never reached the element');
}

/** The schedule as saved: daily at 02:00, 14 kept, the next run at 02:00 within a day */
async function expectNightly(page: Page): Promise<void> {
	const saved = await schedule(page);
	expect(saved.schedule).toMatchObject({
		enabled: true,
		intervalHours: 24,
		timeOfDay: '02:00',
		retentionCount: 14
	});
	expect(saved.next.at).not.toBeNull();
	const at = new Date(saved.next.at as string);
	const now = Date.now();
	expect(at.getTime()).toBeGreaterThan(now);
	expect(at.getTime() - now).toBeLessThanOrEqual(24 * 3600 * 1000 + 60 * 1000);
	if (saved.next.timeZone === 'UTC') expect([at.getUTCHours(), at.getUTCMinutes()]).toEqual([2, 0]);
	// The page says when, once, and not "within a minute"
	await expect(page.getByText(/^The next backup runs on this schedule: /)).toBeVisible();
	await expect(page.locator('.settings-summary')).not.toContainText('Within a minute');
}

test.describe('back up the server every night and keep 14 copies', () => {
	test.use({ role: 'admin' });
	// Both tests change the one schedule of the server
	test.describe.configure({ mode: 'serial' });

	let before: ScheduleAnswer['schedule'] | null = null;

	test.beforeEach(async ({ page }) => {
		await page.goto(`${UI}/server/backups`);
		await expect(page.getByRole('heading', { level: 1, name: 'Backups' })).toBeVisible(START);
		before = (await schedule(page)).schedule;
		// Start from a schedule that is off, as on a new server
		expect(await putSchedule(page, { ...before, enabled: false, timeOfDay: '' })).toBe(200);
		await page.reload();
	});

	test.afterEach(async ({ page }) => {
		if (before) expect(await putSchedule(page, before)).toBe(200);
	});

	test('with the mouse', async ({ page }) => {
		const enable = page.getByRole('checkbox', { name: 'Make backups on a schedule' });
		await expect(enable).toBeVisible(START);
		await expect(enable).not.toBeChecked();
		await enable.click();
		await expect(enable).toBeChecked();

		await page.getByRole('combobox', { name: 'How often' }).selectOption({ label: 'Every day' });
		await page.getByRole('combobox', { name: 'Time of day' }).selectOption('02:00');
		await page.getByRole('spinbutton', { name: 'Keep scheduled backups' }).fill('14');
		// "Keep update backups" is a different setting, in its own group
		await expect(page.getByRole('group', { name: 'Keep update backups' })).toBeVisible();

		const bar = page.getByRole('region', { name: 'Unsaved changes' });
		await expect(bar).toContainText('3 unsaved changes');
		await expect(page.getByText('When you save, the next backup runs on the new schedule.')).toBeVisible();
		await expectNoAxeViolations(page);
		await bar.getByRole('button', { name: 'Save changes' }).click();
		await expect(page.getByText(/^Saved: /)).toContainText('Time of day 02:00');
		await expect(bar).toBeHidden();
		await expectNightly(page);

		// After a reload the page shows what was saved
		await page.reload();
		await expect(page.getByRole('combobox', { name: 'Time of day' })).toHaveValue('02:00', START);
		await expect(page.getByRole('spinbutton', { name: 'Keep scheduled backups' })).toHaveValue('14');
	});

	test('by keyboard alone', async ({ page }) => {
		const enable = page.getByRole('checkbox', { name: 'Make backups on a schedule' });
		await expect(enable).toBeVisible(START);
		await page.locator('body').focus();
		await tabTo(page, enable);
		await page.keyboard.press('Space');
		await expect(enable).toBeChecked();

		// Every day is what a new schedule starts with; the time of day is the next stop
		const often = page.getByRole('combobox', { name: 'How often' });
		await page.keyboard.press('Tab');
		await expect(often).toBeFocused();
		await expect(often).toHaveValue('24');
		const time = page.getByRole('combobox', { name: 'Time of day' });
		await page.keyboard.press('Tab');
		await expect(time).toBeFocused();
		for (let i = 0; i < 3; i++) await page.keyboard.press('ArrowDown');
		await expect(time).toHaveValue('02:00');

		const keep = page.getByRole('spinbutton', { name: 'Keep scheduled backups' });
		await page.keyboard.press('Tab');
		await expect(keep).toBeFocused();
		await page.keyboard.press('ControlOrMeta+A');
		await page.keyboard.type('14');
		await expect(keep).toHaveValue('14');

		// Ctrl+S saves while there are changes
		await page.keyboard.press('ControlOrMeta+S');
		await expect(page.getByText(/^Saved: /)).toContainText('Keep scheduled backups 14');
		await expectNightly(page);
	});
});
