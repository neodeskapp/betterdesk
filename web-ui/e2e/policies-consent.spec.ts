/**
 * A first-time admin's task on Rules > Policies: make every session in one
 * organization require the consent of the person at the computer. The page
 * must show that the organization has no rule yet, set the rule with
 * "Set for this organization", save it with the save bar, confirm what was
 * saved, and say plainly that no NeoDesk app applies the Connection group
 * yet. Once with the mouse, once by keyboard alone. Each test makes its own
 * organization and deletes it, so the browsers never share one.
 */
import type { Locator, Page } from '@playwright/test';
import { test, expect, expectNoAxeViolations } from './fixtures.ts';
import { UI } from './roles.ts';

test.use({ role: 'super_admin' });

const LOAD = { timeout: 30_000 };
const NOT_APPLIED = 'No NeoDesk app applies this group yet. It is saved and handed to devices that ask.';
const ATTENDED = 'Attended: the person at the device must accept';

async function csrf(page: Page): Promise<string> {
	const r = await page.request.get('/api/console/bootstrap', { headers: { Accept: 'application/json' } });
	return ((await r.json()) as { csrfToken: string }).csrfToken;
}

/** A new organization with no policies, named for this test and browser */
async function makeOrg(page: Page, label: string): Promise<{ id: string; name: string }> {
	await page.goto(`${UI}/policies`);
	const stamp = `${label}-${Date.now().toString(36)}`;
	const name = `E2E consent ${stamp}`;
	const r = await page.request.post('/api/panel/org', {
		data: { name, slug: `e2e-consent-${stamp}`.toLowerCase(), logo_url: '' },
		headers: { Accept: 'application/json', 'X-CSRF-Token': await csrf(page) }
	});
	expect(r.status(), await r.text()).toBeLessThan(300);
	const body = (await r.json()) as { id?: string; organization?: { id: string }; data?: { id: string } };
	const id = body.id ?? body.organization?.id ?? body.data?.id;
	if (!id) throw new Error(`no organization id in ${JSON.stringify(body)}`);
	return { id, name };
}

async function dropOrg(page: Page, id: string): Promise<void> {
	await page.request.delete(`/api/panel/org/${encodeURIComponent(id)}`, {
		headers: { Accept: 'application/json', 'X-CSRF-Token': await csrf(page) }
	});
}

/** What the server says applies for session approval */
async function storedMode(page: Page, id: string): Promise<{ value: unknown; source: string }> {
	const r = await page.request.get(`/api/panel/policies/${encodeURIComponent(id)}/resolved`, {
		headers: { Accept: 'application/json' }
	});
	const body = (await r.json()) as { connection: { fields: { mode: { value: unknown; source: string } } } };
	return body.connection.fields.mode;
}

/** Press Tab until the element has the focus; fails when it is not reachable */
async function tabTo(page: Page, target: Locator, max = 250): Promise<void> {
	const handle = await target.elementHandle();
	for (let i = 0; i < max; i++) {
		await page.keyboard.press('Tab');
		if (await handle?.evaluate((el) => el === document.activeElement)) return;
	}
	throw new Error('not reachable with Tab');
}

const group = (page: Page, name: string) => page.locator(`#pol-${name}`);

/** Before: no rule, the notice that no app applies Connection, none on Network */
async function expectNoRuleYet(page: Page): Promise<void> {
	const connection = group(page, 'connection');
	await expect(connection.locator('.group-status')).toHaveText('No rule here', LOAD);
	await expect(connection.getByText(NOT_APPLIED)).toBeVisible();
	await expect(connection.getByText('Devices get this when they next ask for their policy.')).toBeVisible();
	await expect(group(page, 'network').getByText(NOT_APPLIED)).toHaveCount(0);
	await expect(
		group(page, 'network').getByText('The server applies this to new connections within a minute.')
	).toBeVisible();
	// No value shown where nothing is set
	await expect(page.getByLabel('Session approval', { exact: true })).toHaveCount(0);
	await expect(page.getByText('No rule: each device keeps its own setting.').first()).toBeVisible();
	await expect(page.getByText(/Devices outside any organization get no policy\./)).toBeVisible();
}

/** After: the rule is saved, said in the toast, the group and the server */
async function expectSaved(page: Page, id: string): Promise<void> {
	await expect(page.getByText(`Saved: Session approval ${ATTENDED}.`)).toBeVisible(LOAD);
	const connection = group(page, 'connection');
	await expect(connection.locator('.group-status')).toHaveText('Set for this organization');
	// The rule is saved, and the page still says no app applies it yet
	await expect(connection.getByText(NOT_APPLIED)).toBeVisible();
	await expect(page.locator('.changes-bar')).toHaveCount(0);
	await expect(page.locator('#pol-audit')).toContainText('Connection', LOAD);
	expect(await storedMode(page, id)).toEqual({ value: 'attended', source: 'organization' });
}

test('make every session in an organization require the consent of the person at the computer', async ({
	page
}, info) => {
	const org = await makeOrg(page, `${info.project.name}-mouse`);
	try {
		await page.goto(`${UI}/policies`);
		await page.getByLabel('Organization', { exact: true }).selectOption({ label: org.name });
		await expect(page).toHaveURL(new RegExp(`${UI}/policies/${org.id}$`));
		await expect(page.getByText('0 devices are in this organization.', { exact: false })).toBeVisible(LOAD);
		await expectNoRuleYet(page);
		await expectNoAxeViolations(page);

		await page.getByRole('button', { name: 'Set for this organization: Session approval' }).click();
		const mode = page.getByLabel('Session approval', { exact: true });
		await expect(mode).toHaveValue('attended');
		await expect(mode.locator('option:checked')).toHaveText(ATTENDED);
		const bar = page.locator('.changes-bar');
		await expect(bar).toContainText('1 unsaved change');
		await expect(bar).toContainText(`Session approval: no rule → ${ATTENDED}`);
		await expectNoAxeViolations(page);
		await bar.getByRole('button', { name: 'Save policies' }).click();
		await expectSaved(page, org.id);
		await expectNoAxeViolations(page);
	} finally {
		await dropOrg(page, org.id);
	}
});

test('the same by keyboard alone', async ({ page }, info) => {
	const org = await makeOrg(page, `${info.project.name}-keys`);
	try {
		await page.goto(`${UI}/policies`);
		const row = page.locator('.org-list a', { hasText: org.name });
		await expect(row).toBeVisible(LOAD);
		await tabTo(page, row);
		await page.keyboard.press('Enter');
		await expect(page).toHaveURL(new RegExp(`${UI}/policies/${org.id}$`));
		await expectNoRuleYet(page);

		const setHere = page.getByRole('button', { name: 'Set for this organization: Session approval' });
		await tabTo(page, setHere);
		await page.keyboard.press('Enter');
		// The focus moves to the control that took the button's place
		const mode = page.getByLabel('Session approval', { exact: true });
		await expect(mode).toBeFocused();
		await expect(mode).toHaveValue('attended');
		await expect(page.locator('.changes-bar')).toContainText('1 unsaved change');
		await page.keyboard.press('Control+s');
		await expectSaved(page, org.id);
	} finally {
		await dropOrg(page, org.id);
	}
});
