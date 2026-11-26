/**
 * The Policies page on the settings pattern: an organization's settings
 * show what applies now and where it comes from, nothing is filled in
 * that the organization did not set, setting a rule and removing it are
 * explicit, Save writes only the changed groups (PUT with the rules,
 * DELETE when none is left) and the toast offers Undo. Every admin role
 * may read; writing needs org.edit (A83).
 */
import { cleanup, render, screen, within } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

const pageState = vi.hoisted(() => ({
	url: new URL('http://localhost/app/policies/org-1'),
	params: { orgId: 'org-1' } as Record<string, string>
}));
vi.mock('$app/state', () => ({ page: pageState }));
vi.mock('$app/paths', () => ({ resolve: (p: string) => '/app' + (p || '/'), asset: (p: string) => '/' + p }));
vi.mock('$app/navigation', () => ({
	goto: vi.fn(),
	pushState: vi.fn(),
	replaceState: vi.fn(),
	beforeNavigate: vi.fn()
}));
const confirm = vi.hoisted(() => vi.fn(async () => true));
vi.mock('#lib/ui/dialogs.svelte.ts', () => ({ confirm }));

import Page from '../../../src/routes/(console)/policies/[[orgId]]/+page.svelte';
import { session } from '../../../src/lib/state/session.svelte.ts';
import { dismissAll, toasts } from '../../../src/lib/ui/toasts.svelte.ts';

type Fields = Record<
	string,
	{ value: unknown; source: string; server_value?: unknown; server_setting?: unknown }
>;

const GROUP_FIELDS: Record<string, string[]> = {
	connection: ['idle_lock_timeout', 'max_session_duration', 'mode', 'wallpaper'],
	features: ['allow_audio', 'allow_clipboard', 'allow_file_transfer', 'allow_recording'],
	security: [
		'allowed_operators',
		'password_max_age_days',
		'password_min_length',
		'password_require_special',
		'require_2fa'
	],
	network: ['allowed_relay_servers', 'block_direct_p2p', 'ip_allowlist'],
	update: ['auto_update', 'channel']
};

/** The resolved answer for what the organization stored */
function resolvedFor(stored: Record<string, Record<string, unknown>>, deviceAppApplies = false) {
	const out: Record<string, unknown> = {};
	for (const [group, names] of Object.entries(GROUP_FIELDS)) {
		const values = stored[group] ?? {};
		const fields: Fields = {};
		for (const name of names) {
			const has = name in values;
			fields[name] = { value: has ? values[name] : null, source: has ? 'organization' : 'none' };
		}
		if (group === 'network') {
			fields.block_direct_p2p = {
				...fields.block_direct_p2p!,
				source: 'block_direct_p2p' in values ? 'organization' : 'server',
				server_value: true,
				server_setting: {
					label_key: 'settings.connection_mode_title',
					href: '/settings#connection-mode-section'
				}
			};
		}
		out[group] = {
			set: Object.keys(values).length > 0,
			applied_by: group === 'network' ? 'server' : 'device',
			device_app_applies: group === 'network' ? false : deviceAppApplies,
			timing: group === 'network' ? 'server_minute' : 'device_next_check',
			fields
		};
	}
	return out;
}

let writes: { call: string; body: unknown }[] = [];
let stored: Record<string, Record<string, unknown>> = {};
let deviceAppApplies = false;

function answer(path: string): unknown {
	switch (path) {
		case '/api/panel/org':
			return { organizations: [{ id: 'org-1', name: 'Front office' }] };
		case '/api/panel/policies/org-1/resolved':
			return resolvedFor(stored, deviceAppApplies);
		case '/api/panel/policies/org-1/audit':
			return { entries: [] };
		case '/api/panel/org/org-1/devices':
			return { devices: [{ id: 'a' }, { id: 'b' }] };
		case '/api/panel/roles':
			return {
				success: true,
				data: { roles: [{ name: 'admin' }, { name: 'remote_operator' }], all_permissions: [] }
			};
		case '/api/panel/scaling/relays':
			return [{ id: 'r1', name: 'Frankfurt', address: 'relay-fra.example.net:21117' }];
		default:
			return { success: true };
	}
}

beforeEach(() => {
	writes = [];
	stored = {};
	deviceAppApplies = false;
	confirm.mockClear();
	vi.stubGlobal('fetch', async (input: string, init: RequestInit = {}) => {
		const url = new URL(input, location.origin);
		const method = init.method ?? 'GET';
		if (method !== 'GET') {
			const body = init.body ? JSON.parse(String(init.body)) : undefined;
			writes.push({ call: `${method} ${url.pathname}`, body });
			const group = url.pathname.split('/').pop() ?? '';
			if (method === 'PUT') stored[group] = body as Record<string, unknown>;
			if (method === 'DELETE')
				stored = Object.fromEntries(Object.entries(stored).filter(([k]) => k !== group));
		}
		return new Response(JSON.stringify(answer(url.pathname)), {
			status: 200,
			headers: { 'Content-Type': 'application/json' }
		});
	});
});

afterEach(() => {
	cleanup();
	dismissAll();
	vi.unstubAllGlobals();
	session.permissions = [];
	session.user = null;
});

function signIn(role: string, permissions: string[]) {
	session.isSuperAdmin = false;
	session.permissions = permissions;
	session.user = { id: 1, username: 'demo', role, preferredLanguage: null, avatarVersion: null };
}

const EDITOR = ['org.edit', 'org.manage_users', 'user.view', 'device.view', 'server.config'];

async function openPage() {
	render(Page, { params: { orgId: 'org-1' } } as never);
	await vi.waitFor(() =>
		expect(screen.getByRole('heading', { name: 'policies.connection_title' })).toBeInTheDocument()
	);
}

const group = (name: string) => screen.getByRole('heading', { name }).closest('section') as HTMLElement;
const setHere = (label: string) =>
	screen.getByRole('button', { name: `Set for this organization: ${label}` });

describe('policies page', () => {
	test('server_admin (no org.edit) reads everything but can set nothing and writes nothing', async () => {
		signIn('server_admin', ['server.config', 'user.view', 'device.view']);
		stored = { connection: { mode: 'unattended' } };
		await openPage();
		expect(screen.getByText('You can view this page but not change it.')).toBeInTheDocument();
		expect(screen.getByLabelText('policies.connection_mode')).toBeDisabled();
		expect(setHere('policies.allow_audio')).toBeDisabled();
		expect(screen.queryByRole('button', { name: /Remove all rules/ })).toBeNull();
		expect(writes).toEqual([]);
	});

	test('a group never saved shows no values, only "No rule" and "Set for this organization"', async () => {
		signIn('global_admin', EDITOR);
		await openPage();
		const features = group('policies.features_title');
		expect(within(features).getByText('No rule here')).toBeInTheDocument();
		expect(within(features).queryAllByRole('combobox')).toEqual([]);
		expect(within(features).queryAllByRole('checkbox')).toEqual([]);
		expect(within(features).getAllByText('No rule: each device keeps its own setting.')).toHaveLength(4);
		expect(setHere('policies.allow_audio')).toBeEnabled();
		// The connection strategy is where "Always connect through a relay" comes from
		const network = group('policies.network_title');
		expect(within(network).getByText(/From the server: Always through the relay\./)).toBeInTheDocument();
		expect(within(network).getByRole('link', { name: 'settings.connection_mode_title' })).toHaveAttribute(
			'href',
			'/app/settings#connection-mode-section'
		);
	});

	test('the organization line counts its devices and says devices outside get no policy', async () => {
		signIn('global_admin', EDITOR);
		await openPage();
		await vi.waitFor(() =>
			expect(screen.getByText(/2 devices are in this organization\./)).toBeInTheDocument()
		);
		expect(screen.getByText(/Devices outside any organization get no policy\./)).toBeInTheDocument();
		expect(screen.getByRole('link', { name: 'Put devices into this organization' })).toHaveAttribute(
			'href',
			'/app/organizations/org-1#devices'
		);
	});

	test('the device groups say no app applies them yet; the network group says when the server does', async () => {
		signIn('global_admin', EDITOR);
		await openPage();
		const notice = 'No NeoDesk app applies this group yet. It is saved and handed to devices that ask.';
		for (const g of ['connection', 'features', 'security', 'update'])
			expect(within(group(`policies.${g}_title`)).getByText(notice)).toBeInTheDocument();
		const network = group('policies.network_title');
		expect(within(network).queryByText(notice)).toBeNull();
		expect(within(network).getByText('policies.effect_server')).toBeInTheDocument();
		expect(
			within(group('policies.update_title')).getByText(
				'Devices get this when they next ask for their policy.'
			)
		).toBeInTheDocument();
		// Sound: the place that turns it off today
		expect(
			within(group('policies.features_title')).getByRole('link', { name: 'Open access rules' })
		).toHaveAttribute('href', '/app/devices');
	});

	test('once an app applies the device groups, the notice is gone', async () => {
		signIn('global_admin', EDITOR);
		deviceAppApplies = true;
		await openPage();
		expect(
			within(group('policies.features_title')).getByText(
				'Devices get this when they next ask for their policy.'
			)
		).toBeInTheDocument();
		expect(screen.queryByText(/No NeoDesk app applies this group yet/)).toBeNull();
	});

	test('task 5: sound off where nothing was set is a change, saved as the only rule of the group, with Undo', async () => {
		signIn('global_admin', EDITOR);
		const user = userEvent.setup();
		await openPage();
		await user.click(setHere('policies.allow_audio'));
		const audio = screen.getByLabelText('policies.allow_audio') as HTMLSelectElement;
		expect(audio.value).toBe('off');
		expect(screen.getByText('1 unsaved change')).toBeInTheDocument();
		await user.click(screen.getByRole('button', { name: 'policies.save_button' }));
		await vi.waitFor(() =>
			expect(writes).toEqual([
				{ call: 'PUT /api/panel/policies/org-1/features', body: { allow_audio: false } }
			])
		);
		await vi.waitFor(() =>
			expect(within(group('policies.features_title')).getByText('policies.configured')).toBeInTheDocument()
		);
		const saved = toasts.items.at(-1);
		expect(saved?.message).toMatch(/policies\.allow_audio Off/);
		saved?.action?.onclick();
		await vi.waitFor(() =>
			expect(writes.at(-1)).toEqual({ call: 'DELETE /api/panel/policies/org-1/features', body: undefined })
		);
	});

	test('changing one setting sends only that group with only its rules', async () => {
		signIn('global_admin', EDITOR);
		stored = { connection: { mode: 'unattended' }, update: { channel: 'beta' } };
		const user = userEvent.setup();
		await openPage();
		await user.selectOptions(screen.getByLabelText('policies.connection_mode'), 'attended');
		await user.click(screen.getByRole('button', { name: 'policies.save_button' }));
		await vi.waitFor(() =>
			expect(writes).toEqual([
				{ call: 'PUT /api/panel/policies/org-1/connection', body: { mode: 'attended' } }
			])
		);
	});

	test('removing the last rule of a group clears the group', async () => {
		signIn('global_admin', EDITOR);
		stored = { update: { channel: 'beta' } };
		const user = userEvent.setup();
		await openPage();
		await user.click(screen.getByRole('button', { name: 'Remove the rule: policies.update_channel' }));
		await user.click(screen.getByRole('button', { name: 'policies.save_button' }));
		await vi.waitFor(() =>
			expect(writes).toEqual([{ call: 'DELETE /api/panel/policies/org-1/update', body: undefined }])
		);
	});

	test('"Remove all rules of this group" asks, then clears the group at once', async () => {
		signIn('global_admin', EDITOR);
		stored = { security: { require_2fa: true, password_min_length: 12 } };
		const user = userEvent.setup();
		await openPage();
		expect(screen.getByRole('button', { name: 'Remove all rules of policies.update_title' })).toBeDisabled();
		await user.click(screen.getByRole('button', { name: 'Remove all rules of policies.security_title' }));
		expect(confirm).toHaveBeenCalledOnce();
		await vi.waitFor(() =>
			expect(writes).toEqual([{ call: 'DELETE /api/panel/policies/org-1/security', body: undefined }])
		);
		await vi.waitFor(() =>
			expect(within(group('policies.security_title')).getByText('No rule here')).toBeInTheDocument()
		);
	});

	test('operator roles are picked from the roles; a stored name that is no role stays visible', async () => {
		signIn('global_admin', EDITOR);
		stored = { security: { allowed_operators: ['remote_operator', 'demo_operator'] } };
		await openPage();
		await vi.waitFor(() =>
			expect(screen.getByRole('checkbox', { name: 'demo_operator (not a role)' })).toBeChecked()
		);
		expect(screen.getByRole('checkbox', { name: 'remote_operator' })).toBeChecked();
		expect(screen.getByRole('checkbox', { name: 'admin' })).not.toBeChecked();
	});

	test('a wrong IP range is named under its own row and nothing is saved (BUGS L395)', async () => {
		signIn('global_admin', EDITOR);
		const user = userEvent.setup();
		await openPage();
		await user.click(setHere('policies.ip_allowlist'));
		await user.click(screen.getByRole('button', { name: 'Add an IP range' }));
		await user.type(screen.getByRole('textbox', { name: 'IP range 1' }), 'not-a-cidr');
		expect(screen.getByRole('textbox', { name: 'IP range 1' })).toHaveAccessibleDescription(
			'Not an IP address or range.'
		);
		await user.click(screen.getByRole('button', { name: 'policies.save_button' }));
		expect(writes).toEqual([]);
		await user.clear(screen.getByRole('textbox', { name: 'IP range 1' }));
		await user.type(screen.getByRole('textbox', { name: 'IP range 1' }), '10.0.0.0/8');
		await user.click(screen.getByRole('button', { name: 'policies.save_button' }));
		await vi.waitFor(() =>
			expect(writes).toEqual([
				{ call: 'PUT /api/panel/policies/org-1/network', body: { ip_allowlist: ['10.0.0.0/8'] } }
			])
		);
	});

	test('relay servers from the Relay servers list are offered with one click', async () => {
		signIn('global_admin', EDITOR);
		const user = userEvent.setup();
		await openPage();
		await user.click(setHere('policies.allowed_relays'));
		await user.click(await screen.findByRole('button', { name: 'Frankfurt (relay-fra.example.net:21117)' }));
		expect(screen.getByRole('textbox', { name: 'Relay server 1' })).toHaveValue(
			'relay-fra.example.net:21117'
		);
	});
});
