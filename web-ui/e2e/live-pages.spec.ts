import { test, expect } from './fixtures.ts';
import { UI } from './roles.ts';

/**
 * The top bar has no Refresh button: the pages whose data other people
 * change ask for it again every 30 seconds while they are on screen, as
 * Help requests does. The clock is Playwright's, so the test does not
 * wait the 30 seconds.
 */

const PAGES = [
	{ name: 'Audit log', url: '/security-audit', api: '/api/settings/audit' },
	{ name: 'Network monitoring', url: '/network', api: '/api/network/targets' },
	{ name: 'Users', url: '/users', api: '/api/users' },
	{ name: 'Organizations', url: '/organizations', api: '/api/panel/org' },
	{ name: 'Device tokens', url: '/devices/add?tab=tokens', api: '/api/panel/tokens' },
	{ name: 'Billing sessions', url: '/billing?tab=sessions', api: '/api/panel/billing/sessions' }
];

test.use({ role: 'super_admin' });

for (const p of PAGES) {
	test(`${p.name} loads its list again after 30 seconds`, async ({ page }) => {
		await page.clock.install();
		let asked = 0;
		page.on('request', (r) => {
			if (r.method() === 'GET' && new URL(r.url()).pathname === p.api) asked++;
		});
		await page.goto(`${UI}${p.url}`);
		await expect.poll(() => asked, { message: 'the first load' }).toBeGreaterThan(0);
		await page.waitForLoadState('networkidle');
		const first = asked;
		// Nothing more before the 30 seconds are up
		await page.clock.runFor(20_000);
		await page.waitForLoadState('networkidle');
		expect(asked).toBe(first);
		await page.clock.runFor(11_000);
		await expect.poll(() => asked, { message: 'asked again after 30 s' }).toBeGreaterThan(first);
	});
}
