/**
 * Several displays of one device in the viewer, with the dev mock device
 * (three displays; Display 3 sits left of the primary one at a negative
 * origin): the Displays menu and its map, the three layouts, clicks that
 * reach the device as points of its desktop, a display in a window of its
 * own, the per-display numbers, the layout kept per device, a narrow
 * window, and an older device that switches one display at a time. axe
 * checks the menu, the tiles and the display's own window.
 *
 * Needs the console with ND_DEV_MOCK_REMOTE=1, like the viewer tests in
 * remote.spec.ts; skipped without it.
 */
import type { Page } from '@playwright/test';
import { test, expect, expectNoAxeViolations } from './fixtures.ts';
import { UI } from './roles.ts';

const MOCK_DEVICE = '900100012';

interface MockDevice {
	streaming: number[];
	press: { x: number; y: number; display: number } | null;
}

async function mockOn(page: Page): Promise<boolean> {
	const r = await page.request.get(`/api/remote/viewer/${MOCK_DEVICE}?mock=1`);
	if (!r.ok()) return false;
	return !!(await r.json()).mockRemote;
}

/** What the mock device last received */
function device(page: Page): Promise<MockDevice> {
	return page.evaluate(() => Reflect.get(window, '__ndMockDevice') as MockDevice);
}

/** The viewer streaming from the mock device, layouts forgotten */
async function streaming(page: Page, scenario = 'ok'): Promise<void> {
	await page.goto(`${UI}/devices`);
	await page.waitForLoadState('networkidle');
	await page.evaluate(() => {
		for (const k of Object.keys(localStorage))
			if (k.startsWith('neodesk_remote_displays_')) localStorage.removeItem(k);
	});
	await page.goto(`${UI}/remote/${MOCK_DEVICE}?mock=1&scenario=${scenario}`);
	await expect(page.locator('.session-panel.streaming')).toBeVisible({ timeout: 60_000 });
}

/** Opens the controls and the Displays menu */
async function displaysMenu(page: Page) {
	const controls = page.getByRole('group', { name: 'Session controls' });
	if (!(await controls.isVisible())) await page.getByRole('button', { name: /^Controls/ }).click();
	await controls.getByRole('button', { name: 'Displays', exact: true }).click();
	const menu = page.getByRole('menu');
	await expect(menu).toBeVisible();
	return menu;
}

async function chooseLayout(page: Page, name: 'One at a time' | 'As arranged' | 'Grid') {
	const menu = await displaysMenu(page);
	await menu.getByRole('menuitemradio', { name, exact: true }).click();
	await page.keyboard.press('Escape');
}

const tile = (page: Page, n: number) => page.locator(`.tile-canvas[data-display="${n}"]`);

test.describe('several displays of one device', () => {
	test.use({ role: 'admin' });

	test.beforeEach(async ({ page }) => {
		test.skip(!(await mockOn(page)), 'the console runs without ND_DEV_MOCK_REMOTE=1');
		// Three displays drawn at once, each through its own screen
		test.slow();
	});

	test('the Displays menu shows the three displays on a map, in their places', async ({ page }) => {
		await streaming(page);
		const menu = await displaysMenu(page);
		const map = menu.locator('.map-display');
		await expect(map).toHaveCount(3);
		await expect(menu.getByRole('menuitemradio', { name: /^Display 1,/ })).toHaveAttribute(
			'aria-checked',
			'true'
		);
		// Display 3 is left of Display 1, Display 2 right of it
		const [one, two, three] = await Promise.all([0, 1, 2].map((i) => map.nth(i).boundingBox()));
		expect(three!.x).toBeLessThan(one!.x);
		expect(two!.x).toBeGreaterThan(one!.x);
		await expectNoAxeViolations(page, '.session-bar, .controls, [role="menu"]');
		// Picking Display 2 on the map shows it
		await menu.getByRole('menuitemradio', { name: /^Display 2,/ }).click();
		await expect.poll(async () => (await device(page)).streaming).toEqual([1]);
	});

	test('as arranged: each display has its own tile, where it sits on the desktop', async ({ page }) => {
		await streaming(page);
		await chooseLayout(page, 'As arranged');
		await expect(page.locator('.tile-canvas')).toHaveCount(3);
		await expect(page.locator('.session-canvas')).toBeHidden();
		const [one, two, three] = await Promise.all([0, 1, 2].map((i) => tile(page, i).boundingBox()));
		expect(three!.x + three!.width).toBeLessThanOrEqual(one!.x);
		expect(two!.x).toBeGreaterThanOrEqual(one!.x + one!.width);
		// The device streams all three
		await expect.poll(async () => (await device(page)).streaming).toEqual([0, 1, 2]);
		await expectNoAxeViolations(page, '.session-panel');
	});

	test('a click on a tile reaches the device at that point of its desktop', async ({ page }) => {
		await streaming(page);
		await chooseLayout(page, 'As arranged');
		// The top left corner of Display 3's picture: its origin, left of the primary display
		const box = (await tile(page, 2).boundingBox())!;
		await page.mouse.click(box.x + box.width * 0.03, box.y + box.height * 0.05);
		const left = (await device(page)).press!;
		expect(left.display).toBe(2);
		expect(left.x).toBeGreaterThanOrEqual(-1600);
		expect(left.x).toBeLessThan(-1450);
		expect(left.y).toBeGreaterThanOrEqual(180);
		const right = (await tile(page, 1).boundingBox())!;
		await page.mouse.click(right.x + right.width / 2, right.y + right.height / 2);
		const middle = (await device(page)).press!;
		expect(middle.display).toBe(1);
		expect(middle.x).toBeGreaterThan(1920 + 500);
		expect(middle.x).toBeLessThan(1920 + 780);
	});

	test('the grid: equal cells; paused background displays stop streaming', async ({ page }) => {
		await streaming(page);
		await chooseLayout(page, 'Grid');
		await expect(page.locator('.tile-canvas')).toHaveCount(3);
		const [one, two, three] = await Promise.all([0, 1, 2].map((i) => tile(page, i).boundingBox()));
		expect(Math.round(one!.width)).toBe(Math.round(two!.width));
		expect(three!.y).toBeGreaterThan(one!.y);
		// Work on Display 2, pause the others
		await tile(page, 1).click();
		const menu = await displaysMenu(page);
		await menu.getByRole('menuitemradio', { name: 'Paused', exact: true }).click();
		await page.keyboard.press('Escape');
		await expect.poll(async () => (await device(page)).streaming).toEqual([1]);
		await expect(page.locator('.tile', { hasText: 'Paused' })).toHaveCount(2);
		// The session info lists each display
		await page
			.getByRole('group', { name: 'Session controls' })
			.getByRole('button', { name: 'Session info', exact: true })
			.click();
		const sheet = page.locator('.rd-sheet');
		await expect(sheet.locator('dd[data-display]')).toHaveCount(3);
		await expect(sheet.locator('dd[data-display="1"]')).toContainText('fps');
		await expect(sheet.locator('dd[data-display="0"]')).toHaveText('Paused');
		await expectNoAxeViolations(page, '.rd-sheet');
	});

	test('the layout stays with the device; shortcuts pick displays and layouts', async ({ page }) => {
		await streaming(page);
		await page.locator('.session-canvas').focus();
		await page.keyboard.press('Control+Alt+Shift+KeyL');
		await expect(page.locator('.tile-canvas')).toHaveCount(3);
		await page.keyboard.press('Control+Alt+Shift+Digit3');
		await expect(page.locator('.tile.is-active .tile-canvas')).toHaveAttribute('data-display', '2');
		await page.reload();
		await expect(page.locator('.session-panel.streaming')).toBeVisible({ timeout: 60_000 });
		await expect(page.locator('.tile-canvas')).toHaveCount(3);
	});

	test('a display in a window of its own: one connection, back to the layout when it closes', async ({
		page,
		context
	}) => {
		await streaming(page);
		await chooseLayout(page, 'As arranged');
		await tile(page, 1).click();
		const menu = await displaysMenu(page);
		const [popup] = await Promise.all([
			context.waitForEvent('page'),
			menu.getByRole('menuitem', { name: 'Open in a window of its own' }).click()
		]);
		await popup.waitForLoadState();
		await expect(popup.getByRole('heading', { level: 1 })).toContainText('Display 2');
		await expect(popup.locator('#nd-popout-canvas')).toBeVisible();
		// The main window keeps the other two; the device still streams the three
		await expect(page.locator('.tile-canvas')).toHaveCount(2);
		await expect.poll(async () => (await device(page)).streaming).toEqual([0, 1, 2]);
		// A click in the window lands on Display 2
		const box = (await popup.locator('#nd-popout-canvas').boundingBox())!;
		await popup.mouse.click(box.x + box.width / 2, box.y + box.height / 2);
		await expect.poll(async () => (await device(page)).press?.display).toBe(1);
		await expectNoAxeViolations(popup);
		// The button closes its window: the click has nothing to wait for afterwards
		await Promise.all([
			popup.waitForEvent('close'),
			popup.getByRole('button', { name: 'Back to the main window' }).click({ noWaitAfter: true })
		]);
		await expect(page.locator('.tile-canvas')).toHaveCount(3);
	});

	test('closing the viewer closes the windows of its displays', async ({ page, context }) => {
		await streaming(page);
		const menu = await displaysMenu(page);
		const [popup] = await Promise.all([
			context.waitForEvent('page'),
			menu.getByRole('menuitem', { name: 'Open in a window of its own' }).click()
		]);
		await expect(popup.locator('#nd-popout-canvas')).toBeVisible();
		await page.close({ runBeforeUnload: true });
		await expect.poll(() => popup.isClosed(), { timeout: 10_000 }).toBe(true);
	});

	test('a narrow window shows one display at a time', async ({ page }) => {
		await page.setViewportSize({ width: 820, height: 900 });
		await streaming(page);
		const menu = await displaysMenu(page);
		await expect(menu.getByRole('menuitemradio', { name: 'As arranged', exact: true })).toBeDisabled();
		await expect(menu.getByText(/at least 900 pixels/)).toBeVisible();
		await expectNoAxeViolations(page, '[role="menu"]');
	});
});

test.describe('an older device', () => {
	test.use({ role: 'admin' });

	test('switches its displays one at a time, without the Displays menu', async ({ page }) => {
		test.skip(!(await mockOn(page)), 'the console runs without ND_DEV_MOCK_REMOTE=1');
		test.slow();
		await streaming(page, 'one-at-a-time');
		await page.getByRole('button', { name: /^Controls/ }).click();
		const controls = page.getByRole('group', { name: 'Session controls' });
		await expect(controls.getByRole('button', { name: 'Displays', exact: true })).toHaveCount(0);
		const strip = controls.getByRole('group', { name: 'Select monitor' });
		await expect(strip.getByRole('button')).toHaveCount(3);
		await strip.getByRole('button', { name: /^Screen 3/ }).click();
		await expect(strip.getByRole('button', { name: /^Screen 3/ })).toHaveAttribute('aria-pressed', 'true');
		await expect.poll(async () => (await device(page)).streaming).toEqual([2]);
		await expect(page.locator('.tile-canvas')).toHaveCount(0);
		await expectNoAxeViolations(page, '.controls');
	});
});
