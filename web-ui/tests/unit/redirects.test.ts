import { describe, expect, test } from 'vitest';
import { REDIRECTS, redirectTarget } from '../../src/lib/redirects.ts';

const go = (address: string) => {
	const url = new URL(address, 'http://console.test');
	return redirectTarget(url.pathname, url.search, url.hash);
};

describe('the old addresses', () => {
	test('the server tabs of Settings are pages of the Server section now', () => {
		expect(go('/settings?tab=backup')).toBe('/server/backups');
		expect(go('/settings?tab=updates')).toBe('/server/updates');
		expect(go('/settings?tab=advanced')).toBe('/server/config-files');
	});

	test('the other parameters and the #id come along; the tab is left out', () => {
		expect(go('/settings?tab=backup&guide=backup#backup-create')).toBe(
			'/server/backups?guide=backup#backup-create'
		);
		expect(go('/settings?tab=Updates')).toBe('/server/updates');
		expect(go('/Settings?tab=advanced')).toBe('/server/config-files');
	});

	test('a tab that stayed, and pages that did not move, have no new address', () => {
		expect(go('/settings')).toBeNull();
		expect(go('/settings?tab=email')).toBeNull();
		expect(go('/server/backups')).toBeNull();
		expect(go('/devices?tab=backup')).toBeNull();
	});

	test('every rule leads to a different address than its own', () => {
		for (const r of REDIRECTS) {
			const from = r.tab === undefined ? r.from : `${r.from}?tab=${r.tab}`;
			const to = go(from);
			expect(to, from).not.toBeNull();
			expect(to, from).not.toBe(from);
			// Following the new address goes nowhere else: no chains, no loops
			expect(go(to!), `${from} → ${to}`).toBeNull();
		}
	});
});
