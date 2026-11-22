import * as v from 'valibot';
import { afterEach, describe, expect, test, vi } from 'vitest';
import {
	createFolder,
	Device,
	DeviceList,
	issueDeviceToken,
	listDevices,
	listFolders
} from '../../src/lib/api/devices.ts';
import {
	countDevices,
	deviceState,
	filterDevices,
	inFolder,
	NO_FILTERS,
	stateLabelKey
} from '../../src/lib/devices/model.ts';
import { footerSlots, panelActions, panelStatus } from '../../src/lib/devices/panel-logic.ts';
import fixture from '../fixtures/devices.json';

describe('device list schema', () => {
	test('accepts what the dev server sends and normalises it', () => {
		const out = v.parse(DeviceList, fixture);
		expect(out.devices.length).toBe(fixture.data.devices.length);
		const d = out.devices[0]!;
		expect(typeof d.id).toBe('string');
		expect(typeof d.online).toBe('boolean');
		expect(Array.isArray(d.tags)).toBe(true);
		expect(Array.isArray(d.groups)).toBe(true);
	});

	test('SQLite booleans, null text and comma tags become clean values', () => {
		const d = v.parse(Device, {
			id: 9001,
			online: 1,
			banned: '0',
			note: null,
			tags: 'office, finance',
			folder_id: '3'
		});
		expect(d).toMatchObject({
			id: '9001',
			online: true,
			banned: false,
			note: '',
			tags: ['office', 'finance'],
			folder_id: 3
		});
	});

	test('a device without an id is refused', () => {
		expect(v.safeParse(Device, { hostname: 'x' }).success).toBe(false);
	});

	test('validation time grows with the list, not faster (5,000 devices)', () => {
		// Absolute times depend on the machine and on the other test files running
		// next to this one; the ratio between two sizes much less. Linear work makes
		// 5,000 devices about 10 times 500; quadratic work would make it about 100.
		const base = fixture.data.devices;
		const make = (n: number) => ({
			success: true,
			data: {
				devices: Array.from({ length: n }, (_, i) => ({ ...base[i % base.length], id: String(100000 + i) })),
				total: n
			}
		});
		const small = make(500);
		const large = make(5000);
		const time = (input: unknown) => {
			let best = Infinity;
			for (let i = 0; i < 5; i++) {
				const t0 = performance.now();
				v.parse(DeviceList, input);
				best = Math.min(best, performance.now() - t0);
			}
			return best;
		};
		time(small); // warm up
		const tSmall = time(small);
		const tLarge = time(large);
		expect(v.parse(DeviceList, large).devices).toHaveLength(5000);
		console.warn(`validated 500 devices in ${tSmall.toFixed(1)} ms, 5000 in ${tLarge.toFixed(1)} ms`);
		expect(tLarge / tSmall).toBeLessThan(40);
	});
});

describe('folders on a PostgreSQL database the Go server created (ids as text)', () => {
	const answer = (data: unknown) =>
		new Response(JSON.stringify({ success: true, data }), {
			status: 200,
			headers: { 'Content-Type': 'application/json' }
		});
	afterEach(() => vi.unstubAllGlobals());

	test('the list takes ids, order and counts as numbers or numeric text', async () => {
		vi.stubGlobal('fetch', async () =>
			answer({
				folders: [
					{ id: '4', name: 'Office', sort_order: '0', device_count: '2' },
					{ id: 5, name: 'Lab', sort_order: 1, device_count: 0 }
				]
			})
		);
		const folders = await listFolders();
		expect(folders.map((f) => [f.id, f.sort_order, f.device_count])).toEqual([
			[4, 0, 2],
			[5, 1, 0]
		]);
		const devices = [{ id: 'a', folder_id: 4 }, { id: 'b', folder_id: '5' }, { id: 'c' }].map((d) =>
			v.parse(Device, d)
		);
		expect(countDevices(devices, folders, []).folders).toEqual(
			new Map([
				[4, 1],
				[5, 1]
			])
		);
		expect(devices.filter((d) => inFolder(d, folders[0]!.id)).map((d) => d.id)).toEqual(['a']);
	});

	test('the create answer with a text id is a folder, not a failure', async () => {
		vi.stubGlobal('fetch', async () => answer({ id: '6', name: 'New' }));
		await expect(createFolder({ name: 'New' })).resolves.toMatchObject({ id: 6, name: 'New' });
	});

	test('text that is no number is still refused', async () => {
		vi.stubGlobal('fetch', async () => answer({ folders: [{ id: 'x', name: 'Bad' }] }));
		await expect(listFolders()).rejects.toThrow();
	});
});

describe('a disabled RDP or VNC target (the server marks it soft_deleted)', () => {
	afterEach(() => vi.unstubAllGlobals());

	test('stays in the list as Disabled, keeps Edit, and offers no connect', async () => {
		vi.stubGlobal(
			'fetch',
			async () =>
				new Response(
					JSON.stringify({
						success: true,
						data: {
							devices: [
								{ id: 'rt_1', remote_target: true, soft_deleted: true, device_type: 'rdp' },
								{ id: '900', soft_deleted: true }
							]
						}
					}),
					{ status: 200, headers: { 'Content-Type': 'application/json' } }
				)
		);
		const [target, peer] = await listDevices();
		expect(target).toMatchObject({ soft_deleted: false, disabled: true });
		expect(peer).toMatchObject({ soft_deleted: true, disabled: false });
		expect(deviceState(target!)).toBe('disabled');
		expect(stateLabelKey('disabled')).toBe('devices.remote_disabled_badge');
		expect(filterDevices([target!, peer!], NO_FILTERS, []).map((d) => d.id)).toEqual(['rt_1']);
		const all = () => true;
		expect(footerSlots(target!, all)).toEqual({ edit: false, editTarget: true, connect: false });
		expect(panelActions(target!, all)).toMatchObject({
			connect: [],
			manage: ['edit-target', 'groups'],
			danger: ['delete-target']
		});
		expect(panelStatus(target!)).toEqual({ tone: 'neutral', key: 'devices.remote_disabled_badge' });
	});
});

describe('a new device token', () => {
	afterEach(() => vi.unstubAllGlobals());

	const answer = (status: number, body: unknown) =>
		new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });

	test('posts to the device-token route of the encoded ID', async () => {
		const seen: [string, string | undefined][] = [];
		vi.stubGlobal('fetch', async (url: string, init: RequestInit = {}) => {
			seen.push([url, init.method]);
			return answer(200, { success: true });
		});
		await expect(issueDeviceToken('a/b c')).resolves.toMatchObject({ success: true });
		expect(seen).toEqual([['/api/devices/a%2Fb%20c/device-token', 'POST']]);
	});

	test("a refusal rejects with the server's message", async () => {
		vi.stubGlobal('fetch', async () => answer(404, { success: false, error: 'Device not found' }));
		await expect(issueDeviceToken('900100001')).rejects.toThrow('Device not found');
	});
});
