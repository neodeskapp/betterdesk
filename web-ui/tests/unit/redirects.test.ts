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

	test('Security checks and Billing are pages of Activity now', () => {
		expect(go('/security-audit?tab=overview')).toBe('/security-checks?tab=checks');
		expect(go('/security-audit?tab=hardening')).toBe('/security-checks?tab=hardening');
		expect(go('/security-audit?tab=vulnerabilities')).toBe('/security-checks?tab=hardening');
		// The audit log and the server events stay
		expect(go('/security-audit')).toBeNull();
		expect(go('/security-audit?tab=events')).toBeNull();
		expect(go('/commercialization')).toBe('/billing');
		expect(go('/commercialization?tab=sessions')).toBe('/billing?tab=sessions');
		expect(go('/commercialization?settings=1#billing-settings-panel')).toBe(
			'/billing?settings=1#billing-settings-panel'
		);
	});

	test('Client app branding is a part of Settings › Branding', () => {
		expect(go('/client-branding')).toBe('/settings?tab=brand#client-app');
		expect(go('/Client-Branding#old')).toBe('/settings?tab=brand#client-app');
	});

	test('your password, two-factor sign-in, sessions and API tokens are on Your account', () => {
		for (const id of [
			'account-password',
			'current-password',
			'new-password',
			'confirm-password',
			'password-form'
		])
			expect(go(`/settings?tab=auth#${id}`), id).toBe('/account#password');
		expect(go('/settings?tab=auth#auth-panel-totp')).toBe('/account#two-factor');
		expect(go('/Settings?tab=Auth#Account-Password')).toBe('/account#password');
		// The old links without a tab went to the same place
		expect(go('/settings#account-password')).toBe('/account#password');
		expect(go('/settings#auth-panel-totp')).toBe('/account#two-factor');
		expect(go('/security-audit?tab=sessions')).toBe('/account#sessions');
		expect(go('/api-tokens')).toBe('/account#api-tokens');
		expect(go('/API-Tokens?x=1')).toBe('/account?x=1#api-tokens');
		// The rest of Sign-in stays, also with another #id
		expect(go('/settings?tab=auth')).toBeNull();
		expect(go('/settings?tab=auth#require-two-factor')).toBeNull();
		expect(go('/settings?tab=ldap#ldap-host')).toBeNull();
	});

	test('every way to add a computer is a tab of Devices › Add computers', () => {
		expect(go('/keys')).toBe('/devices/add?tab=manual');
		expect(go('/tokens')).toBe('/devices/add?tab=tokens');
		expect(go('/pairing')).toBe('/devices/add?tab=pairing');
		expect(go('/agents')).toBe('/devices/add?tab=agents');
		expect(go('/generator')).toBe('/devices/add?tab=agents');
		expect(go('/Keys#key-file')).toBe('/devices/add?tab=manual#key-file');
		expect(go('/tokens?guide=computer')).toBe('/devices/add?tab=tokens&guide=computer');
		// Enter a pairing code opens its dialog on the Pairing codes tab
		expect(go('/?activateAgent=1')).toBe('/devices/add?tab=pairing&pair=1');
		expect(go('/?activateAgent=1&x=2')).toBe('/devices/add?tab=pairing&pair=1&x=2');
		// The overview itself stays
		expect(go('/')).toBeNull();
		expect(go('/?activateAgent=0')).toBeNull();
	});

	test('a rule that needs a #id is for the app alone: without the #id nothing moves', () => {
		const forApp = REDIRECTS.filter((r) => r.hash);
		expect(forApp.length).toBeGreaterThan(0);
		for (const r of forApp) expect(go(r.from + (r.tab ? `?tab=${r.tab}` : '')), r.from).toBeNull();
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
			const query = new URLSearchParams(r.query);
			if (r.tab !== undefined) query.set('tab', r.tab);
			const q = query.toString();
			const from = `${r.from}${q ? `?${q}` : ''}${r.hash ? `#${r.hash[0]}` : ''}`;
			const to = go(from);
			expect(to, from).not.toBeNull();
			expect(to, from).not.toBe(from);
			// Following the new address goes nowhere else: no chains, no loops
			expect(go(to!), `${from} → ${to}`).toBeNull();
		}
	});
});
