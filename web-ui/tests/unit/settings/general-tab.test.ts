/**
 * Settings › General on the settings pattern: one save bar for the
 * addresses and the connection strategy that names every change, each part
 * sent once to its own endpoint, the source of each value, a locked
 * address, the checks per field, and a part the server refuses keeping its
 * change while the other part is saved.
 */
import { cleanup, render, screen, within } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { Reply, serve } from './serve.ts';

vi.mock('$app/state', () => ({ page: { url: new URL('http://localhost/settings'), state: {} } }));
vi.mock('$app/paths', () => ({ resolve: (p: string) => p || '/', asset: (p: string) => '/' + p }));
vi.mock('$app/navigation', () => ({ goto: vi.fn(), beforeNavigate: vi.fn(), replaceState: vi.fn() }));
vi.mock('#lib/ui/dialogs.svelte.ts', () => ({ confirm: vi.fn(async () => true), prompt: vi.fn() }));

import GeneralTab from '../../../src/lib/settings/tabs/GeneralTab.svelte';
import { session } from '../../../src/lib/state/session.svelte.ts';
import { confirm } from '../../../src/lib/ui/dialogs.svelte.ts';

const ENDPOINTS = '/api/settings/public-endpoints';
const CONNECTION = '/api/settings/connection-mode';

function routes(extra: Record<string, unknown> = {}) {
	return serve({
		'GET /api/settings/info': { app: { version: '3.6.4' } },
		[`GET ${ENDPOINTS}`]: {
			public_server_id: '',
			public_relay_server: '',
			public_api_url: '',
			env_locked: []
		},
		[`GET ${CONNECTION}`]: {
			mode: 'p2p_first',
			source: 'systemd',
			p2p_fallback_ms: 2000,
			same_nat_relay: false,
			allow_shared_nat_initiator: false,
			logged_in_only_initiator: false,
			operator_only_outbound: false
		},
		'GET /api/dashboard/client-config': { server_id: 'auto.example.com', relay_server: '', api_url: '' },
		'GET /api/panel/settings/sources': {
			'public_endpoints.public_server_id': {
				value_source: 'default',
				env_var: 'PUBLIC_SERVER_ID',
				locked: false
			},
			'connection_mode.same_nat_relay': { value_source: 'stored', locked: false, default: true },
			'connection_mode.mode': { value_source: 'stored', locked: false, default: 'p2p_first' }
		},
		...extra
	});
}

const idField = () => screen.findByRole('textbox', { name: 'settings.public_endpoints_server_id' });

beforeEach(() => {
	session.permissions = ['server.config'];
	vi.mocked(confirm).mockClear();
});

afterEach(() => {
	cleanup();
	vi.unstubAllGlobals();
	session.permissions = [];
});

describe('one save for the addresses and the connection strategy', () => {
	test('the bar names both changes; Save sends each part once', async () => {
		const server = routes();
		const user = userEvent.setup();
		render(GeneralTab);
		await user.type(await idField(), 'desk.example.com');
		await user.click(screen.getByRole('radio', { name: /settings\.general\.conn_relay/ }));
		const bar = screen.getByRole('region', { name: 'Unsaved changes' });
		expect(bar).toHaveTextContent('2 unsaved changes');
		expect(bar).toHaveTextContent(
			'settings.public_endpoints_server_id: found automatically → desk.example.com'
		);
		await user.click(within(bar).getByRole('button', { name: 'Save changes' }));
		await vi.waitFor(() => expect(server.sent('PUT', CONNECTION)).toHaveLength(1));
		expect(confirm).toHaveBeenCalledTimes(1);
		expect(server.sent('PUT', ENDPOINTS)).toEqual([
			{ public_server_id: 'desk.example.com', public_relay_server: '', public_api_url: '' }
		]);
		expect(server.sent('PUT', CONNECTION)[0]).toMatchObject({ mode: 'relay_only', same_nat_relay: false });
	});

	test('only the part that changed is sent, and the addresses are not asked about', async () => {
		const server = routes();
		const user = userEvent.setup();
		render(GeneralTab);
		await idField();
		await user.click(screen.getByRole('checkbox', { name: /settings\.general\.conn_logged_in/ }));
		await user.click(screen.getByRole('button', { name: 'Save changes' }));
		await vi.waitFor(() => expect(server.sent('PUT', CONNECTION)).toHaveLength(1));
		expect(server.sent('PUT', ENDPOINTS)).toEqual([]);
		expect(confirm).not.toHaveBeenCalled();
	});

	test('a part the server refuses keeps its change and says why at the field; the other part is saved', async () => {
		const server = routes({
			[`PUT ${ENDPOINTS}`]: new Reply(
				{ success: false, error: 'Not a host the clients can use', field: 'public_server_id' },
				400
			)
		});
		const user = userEvent.setup();
		render(GeneralTab);
		await user.type(await idField(), 'desk.example.com');
		await user.click(screen.getByRole('radio', { name: /settings\.general\.conn_relay/ }));
		await user.click(screen.getByRole('button', { name: 'Save changes' }));
		await vi.waitFor(() => expect(server.sent('PUT', CONNECTION)).toHaveLength(1));
		const bar = await screen.findByRole('region', { name: 'Unsaved changes' });
		await vi.waitFor(() => expect(bar).toHaveTextContent('1 unsaved change'));
		expect(bar).toHaveTextContent('Not a host the clients can use');
		expect(document.getElementById('public-server-id-error')).toHaveTextContent(
			'Not a host the clients can use'
		);
	});
});

describe('what each row says', () => {
	test('an address set nowhere is the one found automatically; a stored rule offers Reset to its default', async () => {
		routes();
		const user = userEvent.setup();
		render(GeneralTab);
		await idField();
		expect(document.getElementById('public-server-id')).toHaveTextContent(/found automatically/);
		const reset = screen.getByRole('button', { name: 'Reset to default: settings.general.conn_same_nat' });
		await user.click(reset);
		expect(screen.getByRole('checkbox', { name: /settings\.general\.conn_same_nat/ })).toBeChecked();
		expect(
			screen.queryByRole('button', { name: 'Reset to default: settings.general.conn_same_nat' })
		).toBeNull();
	});

	test('an address the environment locks is shown, can not be changed, and has no Reset', async () => {
		routes({
			[`GET ${ENDPOINTS}`]: {
				public_server_id: 'env.example.com',
				public_relay_server: '',
				public_api_url: '',
				env_locked: ['public_server_id']
			},
			'GET /api/panel/settings/sources': {
				'public_endpoints.public_server_id': {
					value_source: 'environment',
					env_var: 'PUBLIC_SERVER_ID',
					locked: true
				}
			}
		});
		render(GeneralTab);
		const field = await idField();
		await vi.waitFor(() => expect(field).toHaveValue('env.example.com'));
		expect(field).toBeDisabled();
		expect(document.getElementById('public-server-id')).toHaveTextContent(
			'Set in the environment file (PUBLIC_SERVER_ID), change it there'
		);
		expect(
			screen.queryByRole('button', { name: /Reset to default: settings\.public_endpoints_server_id/ })
		).toBeNull();
	});

	test('a wrong address is said at its field and nothing is sent', async () => {
		const server = routes();
		const user = userEvent.setup();
		render(GeneralTab);
		await user.type(await idField(), 'https://desk.example.com:21116');
		expect(document.getElementById('public-server-id-error')).toHaveTextContent(/without http/);
		await user.click(screen.getByRole('button', { name: 'Save changes' }));
		expect(screen.getByRole('region', { name: 'Unsaved changes' })).toHaveTextContent(/marked on the page/);
		expect(server.sent('PUT', ENDPOINTS)).toEqual([]);
	});

	test('relay only says the direct-connection rules do nothing', async () => {
		routes();
		const user = userEvent.setup();
		render(GeneralTab);
		await idField();
		expect(screen.queryAllByText(/every session goes through the relay server/)).toHaveLength(0);
		await user.click(screen.getByRole('radio', { name: /settings\.general\.conn_relay/ }));
		expect(screen.getAllByText('Has no effect: every session goes through the relay server.')).toHaveLength(
			2
		);
	});
});
