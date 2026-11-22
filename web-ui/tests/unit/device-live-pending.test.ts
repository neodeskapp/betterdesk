/**
 * A live view the device answers later (BUGS L97): while the request
 * waits for an offline device, the view loads again by itself once the
 * device can have answered: it came online (status push or the panel's
 * poll), or it sent a new snapshot of that view. Until then a reload of
 * the device asks the agent nothing.
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import { tick } from 'svelte';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import DevicePanelHost from '../../src/lib/devices/DevicePanelHost.svelte';
import '../../src/lib/devices/DevicePanel.svelte';
import { openDeviceAt } from '../../src/lib/devices/panel.svelte.ts';
import { closeDevice, refreshDevice } from '../../src/lib/state/devicePanel.svelte.ts';
import { session } from '../../src/lib/state/session.svelte.ts';

const ID = '900100002';

let device: Record<string, unknown> = {};
let services: unknown = {};
let calls: string[] = [];

beforeEach(() => {
	calls = [];
	session.isSuperAdmin = true;
	session.permissions = [];
	device = { id: ID, hostname: 'kiosk-1', online: false, banned: false, soft_deleted: false, device_type: 'rustdesk' };
	services = { success: true, pending: true, data: { pending: true, command: 'collect.services' } };
	vi.stubGlobal('fetch', async (input: string, init: RequestInit = {}) => {
		const url = new URL(input, location.origin);
		const key = `${init.method ?? 'GET'} ${url.pathname}`;
		calls.push(key);
		const routes: Record<string, () => unknown> = {
			[`GET /api/devices/${ID}`]: () => ({ success: true, data: device }),
			[`GET /api/devices/${ID}/services`]: () => services,
			'GET /api/folders': () => ({ success: true, data: { folders: [] } })
		};
		const handler = routes[key];
		const data = handler ? handler() : { success: false, error: 'not found' };
		const status = handler ? 200 : 404;
		return {
			ok: status < 400,
			status,
			redirected: false,
			url: url.href,
			headers: new Headers({ 'content-type': 'application/json' }),
			json: async () => data,
			text: async () => JSON.stringify(data)
		} as unknown as Response;
	});
});

afterEach(async () => {
	closeDevice();
	await tick();
	cleanup();
	vi.unstubAllGlobals();
});

const serviceLoads = () => calls.filter((c) => c === `GET /api/devices/${ID}/services`).length;
const deviceLoads = () => calls.filter((c) => c === `GET /api/devices/${ID}`).length;

async function openLive() {
	render(DevicePanelHost);
	openDeviceAt(ID, 'live', { liveView: 'services' });
	await screen.findByText('device_detail.agent_pending');
}

async function reloadDevice() {
	const before = deviceLoads();
	refreshDevice(ID);
	await vi.waitFor(() => expect(deviceLoads()).toBeGreaterThan(before));
	await tick();
}

describe('a live view that waits for the device', () => {
	test('loads again by itself when the device comes online', async () => {
		await openLive();
		expect(serviceLoads()).toBe(1);

		// Still offline: reloading the device asks the agent nothing
		await reloadDevice();
		await reloadDevice();
		expect(serviceLoads()).toBe(1);

		device = { ...device, online: true };
		services = { success: true, data: { services: [{ name: 'Spooler', status: 'running' }] } };
		await reloadDevice();
		expect((await screen.findAllByText('Spooler')).length).toBeGreaterThan(0);
		expect(serviceLoads()).toBe(2);
		expect(screen.queryByText('device_detail.agent_pending')).toBeNull();
	});

	test('loads again by itself when the device sends a new snapshot of the view', async () => {
		await openLive();
		device = {
			...device,
			telemetry: { snapshots: { services: { collected_at: '2026-11-22 08:00:00', data: { services: [] } } } }
		};
		services = {
			success: true,
			cached: true,
			collected_at: '2026-11-22 08:00:00',
			data: { services: [{ name: 'Audiosrv', status: 'stopped' }] }
		};
		await reloadDevice();
		expect((await screen.findAllByText('Audiosrv')).length).toBeGreaterThan(0);
		expect(serviceLoads()).toBe(2);
	});
});
