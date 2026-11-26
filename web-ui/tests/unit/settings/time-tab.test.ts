/**
 * Settings › Time on the settings pattern: the time servers one per row
 * with a message per wrong entry, the source of each value with Reset to
 * the default, one save that asks first on a native install and sends the
 * servers as the server stores them.
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import { Reply, serve } from './serve.ts';

vi.mock('$app/paths', () => ({ resolve: (p: string) => p || '/', asset: (p: string) => '/' + p }));
vi.mock('#lib/ui/dialogs.svelte.ts', () => ({ confirm: vi.fn(async () => true), prompt: vi.fn() }));

import TimeTab from '../../../src/lib/settings/tabs/TimeTab.svelte';
import { session } from '../../../src/lib/state/session.svelte.ts';
import { confirm } from '../../../src/lib/ui/dialogs.svelte.ts';

const SETTINGS = '/api/panel/billing/clock/settings';

function routes() {
	return serve({
		// The clock routes answer without { success, data }
		'GET /api/panel/billing/clock/detect-timezone': new Reply({
			timezone: 'UTC',
			common: ['UTC', 'Europe/Berlin']
		}),
		[`GET ${SETTINGS}`]: new Reply({
			settings: {
				ntp_servers: 'time.example.com',
				max_skew_ms: 2000,
				require_synced_clock: true,
				trust_os_ntp: true,
				billing_timezone: 'Europe/Berlin'
			},
			dockerMode: false
		}),
		[`PUT ${SETTINGS}`]: new Reply({ settings: null, dockerMode: false }),
		'GET /api/panel/settings/sources': {
			'clock.ntp_servers': {
				value_source: 'environment',
				env_var: 'NTP_SERVERS',
				locked: false,
				default: 'pool.ntp.org,time.google.com'
			}
		}
	});
}

beforeEach(() => {
	session.permissions = ['server.config'];
	vi.mocked(confirm).mockClear();
});

afterEach(() => {
	cleanup();
	vi.unstubAllGlobals();
	session.permissions = [];
});

test('the time servers are rows; a wrong one is said on its row and nothing is sent', async () => {
	const server = routes();
	const user = userEvent.setup();
	render(TimeTab);
	const first = await screen.findByRole('textbox', { name: 'Time server 1' });
	expect(first).toHaveValue('time.example.com');
	expect(document.getElementById('time-clock-ntp-servers')).toHaveTextContent(
		'Set in the environment file (NTP_SERVERS). Saving here replaces it.'
	);
	await user.click(screen.getByRole('button', { name: 'Add a time server' }));
	await user.type(screen.getByRole('textbox', { name: 'Time server 2' }), 'ntp.example.com:123');
	expect(screen.getByText(/without a port/)).toBeInTheDocument();
	await user.click(screen.getByRole('button', { name: 'Save changes' }));
	expect(server.sent('PUT', SETTINGS)).toEqual([]);
});

test('Reset puts the default servers in; Save asks about the restart and sends them separated by commas', async () => {
	const server = routes();
	const user = userEvent.setup();
	render(TimeTab);
	await screen.findByRole('textbox', { name: 'Time server 1' });
	await user.click(screen.getByRole('button', { name: 'Reset to default: settings.time.servers_label' }));
	expect(screen.getByRole('textbox', { name: 'Time server 2' })).toHaveValue('time.google.com');
	await user.click(screen.getByRole('button', { name: 'Save changes' }));
	await vi.waitFor(() => expect(server.sent('PUT', SETTINGS)).toHaveLength(1));
	expect(confirm).toHaveBeenCalledTimes(1);
	expect(server.sent('PUT', SETTINGS)[0]).toMatchObject({
		ntp_servers: 'pool.ntp.org,time.google.com',
		billing_timezone: 'Europe/Berlin'
	});
});
