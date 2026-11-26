/**
 * Settings › Directory (LDAP) and Single sign-on on the settings pattern:
 * each loads only its own settings; what an install sets once sits behind
 * Advanced (structure plan rows 35, 36, 56, 66 to 68); a filter narrows
 * the rows and opens what it finds; groups say once that they do nothing
 * while the sign-in is off; the default role is a picker of the server's
 * roles; the group to role map and the private networks are lists with a
 * message per wrong row; wrong values are said at their field before
 * anything is sent.
 */
import { cleanup, render, screen, within } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { Reply, serve } from './serve.ts';

vi.mock('$app/paths', () => ({ resolve: (p: string) => p || '/', asset: (p: string) => '/' + p }));
vi.mock('#lib/ui/dialogs.svelte.ts', () => ({ confirm: vi.fn(async () => true), prompt: vi.fn() }));

import LdapTab from '../../../src/lib/settings/tabs/LdapTab.svelte';
import SsoTab from '../../../src/lib/settings/tabs/SsoTab.svelte';
import { session } from '../../../src/lib/state/session.svelte.ts';

const LDAP = '/api/settings/ldap';
const OIDC = '/api/settings/oidc';
const ROLES = {
	'GET /api/panel/roles': { roles: [{ name: 'viewer' }, { name: 'operator' }, { name: 'admin' }] }
};

/** The closed disclosure an element sits in, or null */
const closedDetails = (id: string) => {
	const d = document.getElementById(id)?.closest('details');
	return d && !d.open ? d : null;
};

const ldapRaw = (o: Record<string, unknown> = {}) => ({
	enabled: true,
	host: 'ldap.example.com',
	port: 389,
	base_dn: 'dc=example,dc=com',
	default_role: 'viewer',
	group_role_map: 'CN=Admins,DC=example,DC=com=admin',
	...o
});

function ldapServer(raw: Record<string, unknown> = ldapRaw()) {
	// The LDAP read answers the object itself, without { success, data }
	return serve({ ...ROLES, [`GET ${LDAP}`]: new Reply(raw) });
}

beforeEach(() => {
	session.permissions = ['server.config'];
});

afterEach(() => {
	cleanup();
	vi.unstubAllGlobals();
	session.permissions = [];
});

describe('Directory (LDAP)', () => {
	test('its own settings only; the TLS check and the timeout behind Advanced', async () => {
		const server = ldapServer();
		render(LdapTab);
		await screen.findByRole('textbox', { name: 'settings.ldap_host' });
		expect(closedDetails('ldap-skip-tls-verify')).not.toBeNull();
		expect(closedDetails('ldap-conn-timeout')).not.toBeNull();
		expect(closedDetails('ldap-host')).toBeNull();
		const paths = server.calls.map((c) => c.path);
		expect(paths).toContain(LDAP);
		expect(paths).not.toContain(OIDC);
		expect(document.getElementById('client-sessions-form')).toBeNull();
	});

	test('the filter narrows the rows and opens Advanced for what it finds', async () => {
		ldapServer();
		render(LdapTab);
		await screen.findByRole('textbox', { name: 'settings.ldap_host' });
		await userEvent.type(screen.getByRole('searchbox', { name: 'Filter directory settings' }), 'timeout');
		expect(document.getElementById('ldap-conn-timeout')).toBeVisible();
		expect(closedDetails('ldap-conn-timeout')).toBeNull();
		expect(document.getElementById('ldap-host')).not.toBeVisible();
	});

	test('while it is off, each group says once that its settings do nothing', async () => {
		ldapServer(ldapRaw({ enabled: false }));
		render(LdapTab);
		await screen.findByRole('textbox', { name: 'settings.ldap_host' });
		expect(screen.getAllByText(/These settings do nothing while LDAP sign-in is off/)).toHaveLength(3);
	});

	test('the default role is a picker of the roles; the map is one row per group; Save sends the stored form', async () => {
		const server = ldapServer();
		const user = userEvent.setup();
		render(LdapTab);
		const roles = await screen.findByRole('radiogroup', { name: 'settings.ldap_default_role' });
		expect(
			within(roles)
				.getAllByRole('radio')
				.map((r) => (r as HTMLInputElement).value)
		).toEqual(['viewer', 'operator', 'admin']);
		await user.click(within(roles).getByRole('radio', { name: /users\.role_operator|operator/ }));
		expect(screen.getByRole('textbox', { name: 'Group 1' })).toHaveValue('CN=Admins,DC=example,DC=com');
		await user.click(screen.getByRole('button', { name: 'Add a group' }));
		await user.type(screen.getByRole('textbox', { name: 'Group 2' }), 'Ops');
		await user.selectOptions(screen.getByRole('combobox', { name: 'Role for group 2' }), 'operator');
		await user.click(screen.getByRole('button', { name: 'Save changes' }));
		await vi.waitFor(() => expect(server.sent('PUT', LDAP)).toHaveLength(1));
		expect(server.sent('PUT', LDAP)[0]).toMatchObject({
			default_role: 'operator',
			group_role_map: 'CN=Admins,DC=example,DC=com=admin|Ops=operator'
		});
	});

	test('a host with a scheme or a map row without a role is said at its place, nothing is sent', async () => {
		const server = ldapServer();
		const user = userEvent.setup();
		render(LdapTab);
		const host = await screen.findByRole('textbox', { name: 'settings.ldap_host' });
		await user.clear(host);
		await user.type(host, 'ldaps://ldap.example.com');
		expect(document.getElementById('ldap-host-error')?.textContent).toBeTruthy();
		await user.click(screen.getByRole('button', { name: 'Add a group' }));
		await user.type(screen.getByRole('textbox', { name: 'Group 2' }), 'Ops');
		expect(screen.getByText(/settings\.signin\.check_map_line|needs a group and a role/)).toBeInTheDocument();
		await user.click(screen.getByRole('button', { name: 'Save changes' }));
		expect(server.sent('PUT', LDAP)).toEqual([]);
	});
});

describe('Single sign-on', () => {
	const oidc = (o: Record<string, unknown> = {}) => ({
		enabled: true,
		client_id: 'neodesk',
		issuer_url: 'https://login.example.com',
		redirect_url: `${location.origin}/api/auth/oidc/callback`,
		auto_discovery: true,
		allowed_private_cidrs: '10.20.0.0/16',
		group_role_map: 'neodesk-admins=admin',
		...o
	});

	test('the endpoints and the private networks behind Advanced; its own settings only', async () => {
		const server = serve({ ...ROLES, [`GET ${OIDC}`]: new Reply(oidc()) });
		render(SsoTab);
		await screen.findByRole('textbox', { name: 'settings.oidc_issuer_url' });
		expect(closedDetails('oidc-issuer-url')).toBeNull();
		for (const id of [
			'oidc-allowed-private-cidrs',
			'oidc-auto-discovery',
			'oidc-authorization-url',
			'oidc-token-url',
			'oidc-userinfo-url'
		])
			expect(closedDetails(id), id).not.toBeNull();
		expect(server.calls.map((c) => c.path)).not.toContain(LDAP);
	});

	test('a public network is refused on its row; the lists go back as the server stores them', async () => {
		const server = serve({ ...ROLES, [`GET ${OIDC}`]: new Reply(oidc()) });
		const user = userEvent.setup();
		render(SsoTab);
		await screen.findByRole('textbox', { name: 'settings.oidc_issuer_url' });
		await user.click(screen.getByRole('button', { name: 'Add a network' }));
		await user.type(screen.getByRole('textbox', { name: 'Private network 2' }), '8.8.8.0/24');
		expect(
			screen.getByText(/settings\.signin\.check_network_invalid|not a private network/i)
		).toBeInTheDocument();
		await user.click(screen.getByRole('button', { name: 'Save changes' }));
		expect(server.sent('PUT', OIDC)).toEqual([]);

		await user.clear(screen.getByRole('textbox', { name: 'Private network 2' }));
		await user.type(screen.getByRole('textbox', { name: 'Private network 2' }), '192.168.50.10');
		await user.click(screen.getByRole('button', { name: 'Save changes' }));
		await vi.waitFor(() => expect(server.sent('PUT', OIDC)).toHaveLength(1));
		expect(server.sent('PUT', OIDC)[0]).toMatchObject({
			allowed_private_cidrs: '10.20.0.0/16, 192.168.50.10',
			group_role_map: 'neodesk-admins=admin'
		});
	});

	test('without auto-discovery the endpoints are needed, said at their fields', async () => {
		const server = serve({ ...ROLES, [`GET ${OIDC}`]: new Reply(oidc()) });
		const user = userEvent.setup();
		render(SsoTab);
		await screen.findByRole('textbox', { name: 'settings.oidc_issuer_url' });
		expect(document.getElementById('oidc-token-url')).toHaveTextContent(
			/Used only while Auto-discovery is off/
		);
		await user.click(screen.getByRole('checkbox', { name: /settings\.oidc_auto_discovery/ }));
		expect(document.getElementById('oidc-token-url-error')?.textContent).toBeTruthy();
		await user.click(screen.getByRole('button', { name: 'Save changes' }));
		expect(server.sent('PUT', OIDC)).toEqual([]);
	});
});
