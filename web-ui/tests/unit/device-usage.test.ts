import { describe, expect, test } from 'vitest';
import {
	currentLoad,
	usageChart,
	usageSamples,
	usageSource,
	type PanelDevice
} from '../../src/lib/devices/panel-logic.ts';

/* L480: the usage a NeoDesk client reports, and what the page says without it */

const metricsSnapshot = (data: Record<string, unknown>, status = 'ok', at = '2026-11-24T22:47:38Z') => ({
	telemetry: { snapshots: { metrics: { status, collected_at: at, data } } }
});

const neodesk = (extra: Partial<PanelDevice> = {}): PanelDevice =>
	({
		id: '525210691',
		client_type: 'neodesk',
		device_type: 'neodesk',
		online: true,
		...extra
	}) as PanelDevice;

describe('current load', () => {
	test('a client that measures over a window: its CPU, memory, disk and network rates', () => {
		const load = currentLoad(
			neodesk(
				metricsSnapshot({
					cpu_percent: 12.5,
					cpu_window_seconds: 75,
					memory_percent: 29.2,
					disk_percent: 22.7,
					network: { received_per_second: 2048, transmitted_per_second: 512, window_seconds: 75 }
				})
			)
		);
		expect(load).toMatchObject({
			cpu: 12.5,
			memory: 29.2,
			disk: 22.7,
			netRx: 2048,
			netTx: 512,
			cpuUnmeasured: false
		});
	});

	test('a client up to 1.5.0-rc.7 sends the CPU value of one refresh: not shown as 100 %', () => {
		const load = currentLoad(
			neodesk(
				metricsSnapshot({
					cpu_percent: 100,
					memory_percent: 29.2,
					disk_percent: 22.7,
					network: { received_bytes: 0, transmitted_bytes: 0 }
				})
			)
		);
		expect(load?.cpu).toBeNull();
		expect(load?.cpuUnmeasured).toBe(true);
		expect(load?.memory).toBe(29.2);
		expect(load?.netRx).toBeNull();
	});

	test('the newer of the console heartbeat and the telemetry snapshot wins', () => {
		const older = { cpu_usage: 50, memory_usage: 50, disk_usage: 50, updated_at: '2026-11-24T20:00:00Z' };
		const snap = metricsSnapshot({
			cpu_percent: 5,
			cpu_window_seconds: 60,
			memory_percent: 6,
			disk_percent: 7
		});
		expect(currentLoad(neodesk({ metrics: older, ...snap }))?.cpu).toBe(5);
		const newer = { ...older, updated_at: '2026-11-24T23:00:00Z' };
		expect(currentLoad(neodesk({ metrics: newer, ...snap }))?.cpu).toBe(50);
	});
});

describe('usage history', () => {
	test('samples carry network rates, and a CPU the device did not measure stays empty', () => {
		const samples = usageSamples([
			{ cpu: null, memory: 30, disk: 22.5, time: '2026-11-24T22:01:30Z' },
			{ cpu: 37.5, memory: 41.25, disk: 22.5, time: '2026-11-24T22:00:00Z', net_rx: 1100, net_tx: 30 }
		]);
		expect(samples).toEqual([
			{ t: Date.parse('2026-11-24T22:00:00Z'), cpu: 37.5, memory: 41.25, disk: 22.5, netRx: 1100, netTx: 30 },
			{ t: Date.parse('2026-11-24T22:01:30Z'), cpu: null, memory: 30, disk: 22.5, netRx: null, netTx: null }
		]);
		const chart = usageChart(samples);
		expect(chart.netRx).toEqual([1100, null]);
		expect(chart.netTx).toEqual([30, null]);
		expect(chart.hasNetwork).toBe(true);
	});

	test('no network column without rates', () => {
		const chart = usageChart(usageSamples([{ cpu: 1, memory: 2, disk: 3, time: '2026-11-24T22:00:00Z' }]));
		expect(chart.hasNetwork).toBe(false);
	});
});

describe('where usage comes from', () => {
	test('a NeoDesk client that sends metrics', () => {
		expect(usageSource(neodesk(metricsSnapshot({ memory_percent: 3 })))).toBe('reports');
	});
	test('a NeoDesk client that has sent nothing yet: no data yet', () => {
		expect(usageSource(neodesk())).toBe('waiting');
	});
	test('a NeoDesk client that cannot or may not collect them', () => {
		expect(usageSource(neodesk(metricsSnapshot({}, 'permission_denied')))).toBe('off');
		expect(usageSource(neodesk(metricsSnapshot({}, 'unsupported')))).toBe('off');
	});
	test('a stock RustDesk client sends no telemetry', () => {
		expect(usageSource({ id: '1', client_type: 'rustdesk', device_type: 'rustdesk' } as PanelDevice)).toBe(
			'none'
		);
	});
	test('a RustDesk client whose heartbeats to the console carry usage', () => {
		const d = {
			id: '1',
			client_type: 'rustdesk',
			metrics_history: [{ cpu: 1, memory: 2, disk: 3, time: '2026-11-24T22:00:00Z' }]
		} as PanelDevice;
		expect(usageSource(d)).toBe('reports');
	});
});
