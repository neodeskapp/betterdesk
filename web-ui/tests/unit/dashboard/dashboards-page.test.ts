/**
 * Customizing the overview: adding, setting up, moving and removing
 * widgets saves the account's own dashboards only on Done; shared
 * dashboards show for the accounts they are for, can be hidden, and are
 * published by people with server.config.
 */
import { cleanup, render, screen, within } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeAll, beforeEach, describe, expect, test, vi } from 'vitest';

const pageUrl = vi.hoisted(() => ({ url: new URL('http://localhost/app'), state: {} }));
vi.mock('$app/state', () => ({ page: pageUrl }));
vi.mock('$app/paths', () => ({ resolve: (p: string) => '/app' + (p || '/'), asset: (p: string) => '/' + p }));
vi.mock('$app/navigation', () => ({ goto: vi.fn(), pushState: vi.fn(), replaceState: vi.fn() }));
const confirmed = vi.hoisted(() => ({ answer: true, asked: [] as string[] }));
vi.mock('../../../src/lib/ui/dialogs.svelte.ts', async (load) => ({
	...(await load<object>()),
	confirm: (o: { title: string }) => {
		confirmed.asked.push(o.title);
		return Promise.resolve(confirmed.answer);
	}
}));

import Page from '../../../src/routes/(console)/+page.svelte';
import { session } from '../../../src/lib/state/session.svelte.ts';
import { dismissAll } from '../../../src/lib/ui/toasts.svelte.ts';

beforeAll(async () => {
	await import('../../../src/lib/people/Trends.svelte');
}, 60_000);

let writes: { method: string; path: string; body: unknown }[] = [];
let answers: Record<string, unknown> = {};

const dashboards = (data: object) => ({
	success: true,
	data: { own: null, shared: [], canPublish: false, ...data }
});

beforeEach(() => {
	writes = [];
	confirmed.answer = true;
	confirmed.asked = [];
	answers = {
		'/api/stats': { success: true, data: { devices: { total: 33, online: 14, offline: 19, banned: 2 } } },
		'/api/server/status': {
			success: true,
			data: { hbbs: { status: 'running' }, hbbr: { status: 'running' } }
		},
		'/api/dashboard/client-config': { success: true, data: { server_id: 'desk.example.com' } },
		'/api/dashboards': dashboards({})
	};
	vi.stubGlobal('fetch', async (input: string, init: RequestInit = {}) => {
		const url = new URL(input, location.origin);
		const method = init.method ?? 'GET';
		const body = typeof init.body === 'string' ? (JSON.parse(init.body) as unknown) : undefined;
		if (method !== 'GET') writes.push({ method, path: url.pathname, body });
		// A write answers what it was sent, as the server does after checking it
		const data =
			answers[`${method} ${url.pathname}`] ??
			(method === 'PUT' ? { success: true, data: body } : (answers[url.pathname] ?? { success: true }));
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
	pageUrl.url = new URL('http://localhost/app');
	session.isSuperAdmin = false;
	session.permissions = ['device.view'];
	session.nav = [];
});

afterEach(() => {
	cleanup();
	dismissAll();
	vi.unstubAllGlobals();
});

const early = () => ({
	data: {
		early: {
			stats: Promise.resolve(null),
			status: Promise.resolve(null),
			config: Promise.resolve(null),
			dashboards: Promise.resolve(null)
		}
	}
});
const loaded = () => screen.findByRole('heading', { name: 'dashboard.attention_panel' });
const ownWrites = () => writes.filter((w) => w.path === '/api/dashboards' && w.method === 'PUT');
type Saved = {
	boards: { id: string; name: string; widgets: { type: string; config?: unknown }[] }[];
	hidden: string[];
};

describe('customizing the built-in dashboard', () => {
	test('a note added and filled in is saved with Done, after the built-in widgets', async () => {
		const user = userEvent.setup();
		render(Page, early());
		await loaded();
		await user.click(screen.getByRole('button', { name: 'Customize' }));
		await user.click(screen.getByRole('button', { name: 'Add widget' }));
		await user.click(screen.getByRole('button', { name: /^Note/ }));
		// A custom widget opens its settings at once
		const dialog = await screen.findByRole('dialog', { name: 'Widget settings' });
		await user.type(within(dialog).getByLabelText('Text'), 'Call Jan for keys');
		await user.click(within(dialog).getByRole('button', { name: 'Apply' }));
		expect(ownWrites()).toHaveLength(0);

		await user.click(screen.getByRole('button', { name: 'Done' }));
		await vi.waitFor(() => expect(ownWrites()).toHaveLength(1));
		const saved = ownWrites()[0]!.body as Saved;
		expect(saved.boards).toHaveLength(1);
		expect(saved.boards[0]!.widgets.map((w) => w.type)).toEqual([
			'attention',
			'devices',
			'client-setup',
			'trends',
			'server-status',
			'note'
		]);
		expect(saved.boards[0]!.widgets.at(-1)!.config).toEqual({ text: 'Call Jan for keys' });
		// Out of edit mode, the note shows its text
		expect(await screen.findByText('Call Jan for keys')).toBeInTheDocument();
		expect(screen.queryByRole('button', { name: 'Done' })).toBeNull();
	});

	test('moving a widget earlier changes the saved order', async () => {
		const user = userEvent.setup();
		render(Page, early());
		await loaded();
		await user.click(screen.getByRole('button', { name: 'Customize' }));
		const devices = screen.getByRole('group', { name: 'Devices' });
		await user.click(within(devices).getByRole('button', { name: 'Move earlier' }));
		await user.click(screen.getByRole('button', { name: 'Done' }));
		await vi.waitFor(() => expect(ownWrites()).toHaveLength(1));
		const saved = ownWrites()[0]!.body as Saved;
		expect(saved.boards[0]!.widgets.slice(0, 2).map((w) => w.type)).toEqual(['devices', 'attention']);
	});

	test('Cancel after removing a widget asks first, saves nothing and brings it back', async () => {
		const user = userEvent.setup();
		render(Page, early());
		await loaded();
		await user.click(screen.getByRole('button', { name: 'Customize' }));
		const attention = screen.getByRole('group', { name: 'Needs attention' });
		await user.click(within(attention).getByRole('button', { name: 'Widget options' }));
		await user.click(await screen.findByRole('menuitem', { name: 'Remove' }));
		expect(screen.queryByRole('group', { name: 'Needs attention' })).toBeNull();

		await user.click(screen.getByRole('button', { name: 'common.cancel' }));
		expect(confirmed.asked).toEqual(['Discard your changes?']);
		expect(await loaded()).toBeInTheDocument();
		expect(writes).toHaveLength(0);
	});
});

describe('what each account sees', () => {
	test('a widget the account may not see, or of a type this console doesn’t know, is not drawn', async () => {
		answers['/api/dashboards'] = dashboards({
			own: {
				boards: [
					{
						id: 'mine',
						name: 'Mine',
						widgets: [
							{ id: 'a', type: 'attention', size: 'm' },
							{ id: 't', type: 'trends', size: 'l' },
							{ id: 'f', type: 'from-the-future', size: 'm' }
						]
					}
				],
				active: '',
				hidden: []
			}
		});
		render(Page, early());
		await loaded();
		// trends needs audit.view
		expect(screen.queryByRole('heading', { name: 'reports.trends_title' })).toBeNull();
		expect(screen.queryByText(/doesn’t know this widget/)).toBeNull();
		expect(screen.queryByRole('heading', { name: 'nav.devices' })).toBeNull();
	});

	test('a shared home opens first; it can be hidden but not customized without server.config', async () => {
		answers['/api/dashboards'] = dashboards({
			shared: [
				{
					id: 'helpdesk',
					name: 'Helpdesk',
					widgets: [{ id: 'n', type: 'note', size: 'full', config: { text: 'Shared rules' } }],
					roles: [],
					locked: false,
					home: true
				}
			]
		});
		const user = userEvent.setup();
		render(Page, early());
		expect(await screen.findByText('Shared rules')).toBeInTheDocument();
		expect(screen.queryByRole('button', { name: 'Customize' })).toBeNull();
		expect(screen.queryByRole('tablist')).toBeNull();

		await user.click(screen.getByRole('button', { name: 'Dashboard options' }));
		await user.click(await screen.findByRole('menuitem', { name: 'Hide for me' }));
		await vi.waitFor(() => expect(ownWrites()).toHaveLength(1));
		expect((ownWrites()[0]!.body as Saved).hidden).toEqual(['helpdesk']);
		// The account's own list starts empty: the shared home took the built-in dashboard's place
		expect((ownWrites()[0]!.body as Saved).boards).toEqual([]);
	});

	test('a locked shared dashboard has no Hide', async () => {
		answers['/api/dashboards'] = dashboards({
			shared: [{ id: 'rules', name: 'Rules', widgets: [], roles: [], locked: true, home: true }]
		});
		const user = userEvent.setup();
		render(Page, early());
		await user.click(await screen.findByRole('button', { name: 'Dashboard options' }));
		await screen.findByRole('menuitem', { name: 'Make a copy' });
		expect(screen.queryByRole('menuitem', { name: 'Hide for me' })).toBeNull();
	});
});

describe('publishing', () => {
	test('server.config shares a copy of a dashboard with chosen roles', async () => {
		session.permissions = ['device.view', 'server.config'];
		answers['/api/dashboards'] = dashboards({ canPublish: true });
		answers['/api/panel/roles'] = {
			success: true,
			data: { roles: [{ name: 'operator' }, { name: 'viewer' }], all_permissions: [] }
		};
		const user = userEvent.setup();
		render(Page, early());
		await loaded();
		await user.click(screen.getByRole('button', { name: 'Dashboard options' }));
		await user.click(await screen.findByRole('menuitem', { name: 'Share with others' }));
		const dialog = await screen.findByRole('dialog', { name: 'Share with others' });
		await user.click(within(dialog).getByRole('radio', { name: 'Accounts with these roles' }));
		await user.click(await within(dialog).findByRole('checkbox', { name: /operator/ }));
		await user.click(within(dialog).getByRole('checkbox', { name: 'Open it first' }));
		await user.click(within(dialog).getByRole('button', { name: 'Share' }));

		await vi.waitFor(() => expect(writes.some((w) => w.path === '/api/dashboards/shared')).toBe(true));
		const body = writes.find((w) => w.path === '/api/dashboards/shared')!.body as {
			boards: { name: string; roles: string[]; home: boolean; locked: boolean; widgets: unknown[] }[];
		};
		expect(body.boards).toHaveLength(1);
		expect(body.boards[0]).toMatchObject({
			name: 'nav.dashboard',
			roles: ['operator'],
			home: true,
			locked: false
		});
		expect(body.boards[0]!.widgets).toHaveLength(5);
	});

	test('without a role chosen, sharing with some roles is refused in the dialog', async () => {
		session.permissions = ['device.view', 'server.config'];
		answers['/api/dashboards'] = dashboards({ canPublish: true });
		const user = userEvent.setup();
		render(Page, early());
		await loaded();
		await user.click(screen.getByRole('button', { name: 'Dashboard options' }));
		await user.click(await screen.findByRole('menuitem', { name: 'Share with others' }));
		const dialog = await screen.findByRole('dialog', { name: 'Share with others' });
		await user.click(within(dialog).getByRole('radio', { name: 'Accounts with these roles' }));
		await user.click(within(dialog).getByRole('button', { name: 'Share' }));
		expect(await within(dialog).findByRole('alert')).toHaveTextContent('Choose at least one role');
		expect(writes).toHaveLength(0);
	});
});
