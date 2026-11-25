/**
 * Plan step 4: Sign-in splits into Sign-in, Directory (LDAP), Single
 * sign-on and the join part of Joining (SignInTab's `part`). Each part
 * shows its own forms and loads only what it shows; the settings an install
 * sets once or never sit behind Advanced.
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

vi.mock('#lib/api/settings.ts', async (orig) => ({
	...(await orig<typeof import('../../../src/lib/api/settings.ts')>()),
	getEnrollment: vi.fn(async () => ({
		mode: 'managed',
		rich_approve: true,
		tag_picker: true,
		pending_count: 0
	})),
	getClientSessions: vi.fn(async () => ({ expiry_days: 7, sliding: true, max_days: 30 })),
	getLdap: vi.fn(async () => ({ enabled: true })),
	getOidc: vi.fn(async () => ({ enabled: true, auto_discovery: false })),
	getTotpEnabled: vi.fn(async () => false)
}));

import * as api from '../../../src/lib/api/settings.ts';
import SignInTab from '../../../src/lib/settings/tabs/SignInTab.svelte';
import { session } from '../../../src/lib/state/session.svelte.ts';

beforeEach(() => {
	session.isSuperAdmin = false;
	session.permissions = ['server.config'];
	for (const fn of [api.getEnrollment, api.getClientSessions, api.getLdap, api.getOidc])
		vi.mocked(fn).mockClear();
});

afterEach(() => {
	cleanup();
	session.permissions = [];
});

const byId = (id: string) => document.getElementById(id);
/** The closed Advanced disclosure an element sits in, or null */
const closedDetails = (id: string) => {
	const d = byId(id)?.closest('details');
	return d && !d.open ? d : null;
};

describe('the parts of sign-in', () => {
	test('Sign-in keeps client sessions and two-factor for everyone; nothing of your own account or the others', async () => {
		render(SignInTab);
		await screen.findByRole('heading', { name: 'settings.signin.for_everyone' });
		expect(byId('client-sessions-form')).not.toBeNull();
		expect(byId('require-two-factor')).not.toBeNull();
		// Your own password and two-factor sign-in are on Your account (/account)
		for (const gone of ['account-password', 'current-password', 'auth-panel-totp', 'your-account-title'])
			expect(byId(gone), gone).toBeNull();
		for (const gone of ['enrollment-settings-section', 'ldap-form', 'oidc-form'])
			expect(byId(gone), gone).toBeNull();
		expect(api.getLdap).not.toHaveBeenCalled();
		expect(api.getOidc).not.toHaveBeenCalled();
		expect(api.getEnrollment).not.toHaveBeenCalled();
	});

	test('Directory (LDAP): the form alone, the TLS check and the timeout behind Advanced', async () => {
		render(SignInTab, { part: 'ldap' });
		await screen.findByRole('heading', { level: 2, name: 'settings.ldap_title' });
		expect(byId('ldap-host')).not.toBeNull();
		expect(closedDetails('ldap-skip-tls-verify')).not.toBeNull();
		expect(closedDetails('ldap-conn-timeout')).not.toBeNull();
		expect(closedDetails('ldap-host')).toBeNull();
		expect(byId('oidc-form')).toBeNull();
		expect(byId('client-sessions-form')).toBeNull();
		expect(api.getLdap).toHaveBeenCalledTimes(1);
		expect(api.getOidc).not.toHaveBeenCalled();
	});

	test('Single sign-on: the endpoints and the private networks behind Advanced', async () => {
		render(SignInTab, { part: 'sso' });
		await screen.findByRole('heading', { level: 2, name: 'settings.oidc_title' });
		expect(closedDetails('oidc-issuer-url')).toBeNull();
		for (const id of [
			'oidc-allowed-private-cidrs',
			'oidc-auto-discovery',
			'oidc-authorization-url',
			'oidc-token-url',
			'oidc-userinfo-url'
		])
			expect(closedDetails(id), id).not.toBeNull();
		expect(byId('ldap-form')).toBeNull();
		expect(api.getOidc).toHaveBeenCalledTimes(1);
	});

	test('Joining: the approval form under the join mode', async () => {
		render(SignInTab, { part: 'joining', bare: true });
		await screen.findByRole('heading', { level: 2, name: 'tokens.enrollment_mode' });
		expect(byId('enrollment-rich-approve')).not.toBeNull();
		expect(byId('ldap-form')).toBeNull();
		expect(byId('account-password')).toBeNull();
		expect(api.getEnrollment).toHaveBeenCalledTimes(1);
	});
});
