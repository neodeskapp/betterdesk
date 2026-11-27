/**
 * Roles and permissions on the settings pattern: the role picker first with
 * the role names and what the chosen role can do beside it, no internal
 * keys on screen, edit then save as one batch,
 * the toast that names what changed and undoes it with the server's
 * `previous`, the note for a permission whose needed one is off with its
 * button, the views (what differs, compare, who has it), and super admin
 * roles without switches.
 */
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

const pageState = vi.hoisted(() => ({ url: new URL('http://localhost/permissions'), state: {} }));
vi.mock('$app/state', () => ({ page: pageState }));
vi.mock('$app/paths', () => ({ resolve: (p: string) => p || '/', asset: (p: string) => '/' + p }));
vi.mock('$app/navigation', () => ({
	goto: vi.fn(),
	pushState: vi.fn(),
	replaceState: vi.fn(),
	beforeNavigate: vi.fn()
}));

import Page from '../../../src/routes/(console)/permissions/+page.svelte';
import { session } from '../../../src/lib/state/session.svelte.ts';
import { toasts, dismissAll } from '../../../src/lib/ui/toasts.svelte.ts';
import { setStrings } from '../../../src/lib/i18n/i18n.svelte.ts';
import fs from 'node:fs';
import path from 'node:path';

const en = JSON.parse(
	fs.readFileSync(
		path.resolve(__dirname, '../../../../neodesk-server/console/static/files/lang/en.json'),
		'utf8'
	)
);

const ALL = ['device.view', 'device.connect', 'device.delete', 'server.keys', 'user.view'];
let effective: Record<string, string[]>;
let batches: { role: string; changes: { permission: string; granted: boolean | null }[] }[];

beforeEach(() => {
	setStrings('en', 'ltr', en);
	session.isSuperAdmin = true;
	pageState.url = new URL('http://localhost/permissions');
	batches = [];
	effective = {
		super_admin: ALL,
		operator: ['device.view', 'device.delete'],
		server_admin: ['device.view', 'server.keys']
	};
	vi.stubGlobal('fetch', async (input: string, init?: RequestInit) => {
		const url = new URL(input, location.origin);
		let data: unknown = { success: false };
		if (url.pathname === '/api/panel/roles')
			data = {
				success: true,
				data: {
					roles: [
						{ name: 'super_admin', level: 3, is_super_admin: true, permissions: ALL },
						{ name: 'operator', level: 1, permissions: ['device.view'] },
						{
							name: 'server_admin',
							level: 2,
							permissions: ['device.view', 'server.keys'],
							fixed: { 'user.create': 'assigns_no_role' }
						}
					],
					all_permissions: ALL,
					requires: { 'device.delete': [{ permission: 'device.view', reason: 'devices_page' }] }
				}
			};
		const m = url.pathname.match(/^\/api\/panel\/roles\/([^/]+)\/permissions$/);
		if (m) data = { success: true, data: { role: m[1], permissions: effective[m[1] ?? ''] ?? [] } };
		if (url.pathname === '/api/panel/role-permissions')
			data = {
				success: true,
				data: { overrides: [{ role: 'operator', permission: 'device.delete', granted: true }] }
			};
		if (url.pathname === '/api/panel/role-permissions/batch') {
			const body = JSON.parse(String(init?.body));
			batches.push(body);
			data = {
				success: true,
				data: {
					previous: body.changes.map((c: { permission: string }) => ({
						permission: c.permission,
						granted: c.permission === 'device.delete' ? true : null
					})),
					permissions: ['device.view', 'device.connect']
				}
			};
		}
		return {
			ok: true,
			status: 200,
			redirected: false,
			url: url.href,
			headers: new Headers({ 'content-type': 'application/json' }),
			json: async () => data,
			text: async () => JSON.stringify(data)
		} as unknown as Response;
	});
});

afterEach(() => {
	cleanup();
	setStrings('en', 'ltr', {});
	dismissAll();
	vi.unstubAllGlobals();
});

const row = (perm: string) => document.getElementById(`perm-${perm.replace(/\./g, '-')}`) as HTMLElement;
const box = (perm: string) => within(row(perm)).getByRole('checkbox');

describe('the role comes first', () => {
	test('a picker with every role by name; the page opens on the first role that can be changed', async () => {
		render(Page);
		const picker = await screen.findByRole('radiogroup', { name: 'Role to change' });
		const radios = within(picker).getAllByRole('radio');
		expect(radios).toHaveLength(3);
		expect(within(picker).getByRole('radio', { name: 'Remote operator' })).toBeChecked();
		// Names only: what the chosen role can do is said once, beside the picker
		expect(within(picker).queryByText(/^Connects to devices/)).toBeNull();
		expect(document.querySelector('.perm-scope .perm-summary .perm-sentence')?.textContent).toMatch(
			/^People with this role can/
		);
		// The picker comes before the permissions
		const first = document.querySelector('#perm-role');
		expect(first?.compareDocumentPosition(row('device.view')) ?? 0).toBe(Node.DOCUMENT_POSITION_FOLLOWING);
	});

	test('a super admin role has a note and no switches', async () => {
		pageState.url = new URL('http://localhost/permissions?role=super_admin');
		render(Page);
		await screen.findAllByText('This role always has every permission. It cannot be changed.');
		expect(document.querySelectorAll('[id^="perm-device"]')).toHaveLength(0);
	});
});

describe('rows', () => {
	test('no internal key on screen; the filter still finds a permission by it', async () => {
		render(Page);
		await waitFor(() => expect(box('server.keys')).toBeEnabled());
		expect(row('server.keys').textContent).not.toContain('server.keys');
		expect(row('server.keys')).toHaveAttribute('data-setting', 'server.keys');
		await fireEvent.input(screen.getByRole('searchbox', { name: 'Filter permissions' }), {
			target: { value: 'server.keys' }
		});
		await waitFor(() => expect(row('device.view')).not.toBeVisible());
		expect(row('server.keys')).toBeVisible();
	});

	test('Reset all changes shows only while the role has a saved change', async () => {
		render(Page);
		// operator has a stored override (device.delete)
		expect(await screen.findByRole('button', { name: /Reset all/ })).toBeTruthy();
		cleanup();
		pageState.url = new URL('http://localhost/permissions?role=server_admin');
		render(Page);
		await waitFor(() => expect(box('server.keys')).toBeChecked());
		expect(screen.queryByRole('button', { name: /Reset all/ })).toBeNull();
	});
});

describe('edit, then save', () => {
	test('nothing is sent until Save; Save sends one batch, the toast names the changes and Undo sends previous back', async () => {
		render(Page);
		await waitFor(() => expect(box('device.connect')).toBeEnabled());
		const sentence = () => document.querySelector('.perm-summary .perm-sentence')?.textContent;
		// The summary names the role it describes
		const picked = screen.getAllByRole('radio').find((r) => (r as HTMLInputElement).checked);
		const pickedName =
			picked?.closest('label')?.querySelector('.check-label, strong, span')?.textContent?.trim() ?? '';
		expect(pickedName).not.toBe('');
		expect(screen.getByRole('heading', { level: 2, name: `What ${pickedName} can do` })).toBeInTheDocument();
		expect(sentence()).toBe(
			"People with this role can see and change devices. They can't connect to devices, see people, or change the server."
		);
		await fireEvent.click(box('device.connect'));
		await fireEvent.click(box('device.delete'));
		expect(batches).toEqual([]);
		// Changed rows are marked; the summary follows the unsaved changes
		expect(within(row('device.connect')).getByText('Changed, not saved')).toBeTruthy();
		expect(screen.getByText('With your unsaved changes.')).toBeTruthy();
		expect(sentence()).toBe(
			"People with this role can connect to devices and see devices. They can't change devices, see people, or change the server."
		);

		const bar = screen.getByRole('region', { name: 'Unsaved changes' });
		expect(within(bar).getByText('2 unsaved changes')).toBeTruthy();
		await fireEvent.click(within(bar).getByRole('button', { name: 'Save changes' }));
		await waitFor(() => expect(batches).toHaveLength(1));
		expect(batches[0]).toEqual({
			role: 'operator',
			changes: [
				{ permission: 'device.connect', granted: true },
				// The default for operator is off: back to the default, not a second override
				{ permission: 'device.delete', granted: null }
			]
		});
		await waitFor(() => expect(toasts.items.length).toBe(1));
		const done = toasts.items[0]!;
		expect(done.message).toBe('Saved: Connect to devices allowed, Delete devices default (not allowed).');
		expect(done.action?.label).toBe('Undo');
		done.action?.onclick();
		await waitFor(() => expect(batches).toHaveLength(2));
		expect(batches[1]).toEqual({
			role: 'operator',
			changes: [
				{ permission: 'device.connect', granted: null },
				{ permission: 'device.delete', granted: true }
			]
		});
	});

	test('Back to the default appears for a stored override and joins the draft', async () => {
		render(Page);
		await waitFor(() => expect(box('device.delete')).toBeChecked());
		await fireEvent.click(within(row('device.delete')).getByRole('button', { name: /Back to the default/ }));
		expect(box('device.delete')).not.toBeChecked();
		expect(batches).toEqual([]);
		expect(
			within(screen.getByRole('region', { name: 'Unsaved changes' })).getByText('1 unsaved change')
		).toBeTruthy();
	});
});

describe('dependencies', () => {
	test('a permission whose needed one is off says so, and its button turns the needed one on in the draft', async () => {
		render(Page);
		await waitFor(() => expect(box('device.view')).toBeChecked());
		await fireEvent.click(box('device.view'));
		// The needed one names what stops working
		expect(
			within(row('device.view')).getByText('While this is off, these do nothing: Delete devices.')
		).toBeTruthy();
		const fix = within(row('device.delete')).getByRole('button', { name: 'Turn on View devices' });
		expect(
			within(row('device.delete')).getByText(/^Needs View devices: its buttons are on All devices/)
		).toBeTruthy();
		await fireEvent.click(fix);
		expect(box('device.view')).toBeChecked();
		expect(within(row('device.delete')).queryByRole('button', { name: /Turn on/ })).toBeNull();
		expect(batches).toEqual([]);
	});

	test('Manage server API keys says the role can only list and delete when it can give no role', async () => {
		pageState.url = new URL('http://localhost/permissions?role=server_admin');
		render(Page);
		await waitFor(() => expect(box('server.keys')).toBeChecked());
		expect(within(row('server.keys')).getByText(/but not create them/)).toBeTruthy();
	});
});

describe('views', () => {
	test('only what differs from the default', async () => {
		render(Page);
		await waitFor(() => expect(row('device.connect')).toBeTruthy());
		await fireEvent.click(screen.getByRole('checkbox', { name: 'Only show what differs from the default' }));
		expect(row('device.delete')).toBeTruthy();
		expect(row('device.connect')).toBeNull();
		expect(row('device.view')).toBeNull();
	});

	test('compare with a second role, differences said with words', async () => {
		render(Page);
		const select = await screen.findByRole('combobox', { name: 'Compare with' });
		await fireEvent.change(select, { target: { value: 'server_admin' } });
		expect(within(row('server.keys')).getByText('Server admin: allowed')).toBeTruthy();
		expect(within(row('server.keys')).getByText('Differs')).toBeTruthy();
		expect(within(row('device.view')).getByText('Same')).toBeTruthy();
	});

	test('which roles have a permission', async () => {
		render(Page);
		await waitFor(() => expect(row('server.keys')).toBeTruthy());
		const who = within(row('server.keys'))
			.getByText('Which roles have this?')
			.closest('details') as HTMLElement;
		const names = within(who)
			.getAllByRole('listitem', { hidden: true })
			.map((li) => li.textContent);
		expect(names).toEqual(['Super admin (always)', 'Server admin']);
	});
});
