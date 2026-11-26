import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { poll, reloadWhileShown, socket } from '../../src/lib/state/live.ts';
import { liveUpdates, refreshNow, setLivePaused } from '../../src/lib/state/live-updates.svelte.ts';

let hidden = false;
beforeEach(() => {
	hidden = false;
	Object.defineProperty(document, 'visibilityState', {
		configurable: true,
		get: () => (hidden ? 'hidden' : 'visible')
	});
	vi.useFakeTimers();
});
afterEach(() => {
	setLivePaused(false);
	vi.useRealTimers();
});

function setHidden(h: boolean) {
	hidden = h;
	document.dispatchEvent(new Event('visibilitychange'));
}

describe('poll', () => {
	test('runs now and then every interval', async () => {
		const fn = vi.fn().mockResolvedValue(undefined);
		const stop = poll(fn, { every: 1000 });
		await vi.advanceTimersByTimeAsync(0);
		expect(fn).toHaveBeenCalledTimes(1);
		await vi.advanceTimersByTimeAsync(3000);
		expect(fn).toHaveBeenCalledTimes(4);
		stop();
		await vi.advanceTimersByTimeAsync(5000);
		expect(fn).toHaveBeenCalledTimes(4);
	});

	test('makes no requests while the tab is hidden and catches up on show', async () => {
		const fn = vi.fn().mockResolvedValue(undefined);
		const stop = poll(fn, { every: 1000 });
		await vi.advanceTimersByTimeAsync(0);
		setHidden(true);
		await vi.advanceTimersByTimeAsync(10_000);
		expect(fn).toHaveBeenCalledTimes(1);
		setHidden(false);
		await vi.advanceTimersByTimeAsync(0);
		expect(fn).toHaveBeenCalledTimes(2);
		stop();
	});

	test('a poller started in a hidden tab waits until it is shown', async () => {
		setHidden(true);
		const fn = vi.fn().mockResolvedValue(undefined);
		const stop = poll(fn, { every: 1000 });
		await vi.advanceTimersByTimeAsync(5000);
		expect(fn).not.toHaveBeenCalled();
		setHidden(false);
		await vi.advanceTimersByTimeAsync(0);
		expect(fn).toHaveBeenCalledTimes(1);
		stop();
	});

	test('backs off after errors and resets after success', async () => {
		const fn = vi
			.fn()
			.mockRejectedValueOnce(new Error('x'))
			.mockRejectedValueOnce(new Error('x'))
			.mockResolvedValue(undefined);
		const stop = poll(fn, { every: 1000 });
		await vi.advanceTimersByTimeAsync(0); // fail 1 → wait 2000
		await vi.advanceTimersByTimeAsync(1999);
		expect(fn).toHaveBeenCalledTimes(1);
		await vi.advanceTimersByTimeAsync(1); // fail 2 → wait 4000
		expect(fn).toHaveBeenCalledTimes(2);
		await vi.advanceTimersByTimeAsync(4000); // success → back to 1000
		expect(fn).toHaveBeenCalledTimes(3);
		await vi.advanceTimersByTimeAsync(1000);
		expect(fn).toHaveBeenCalledTimes(4);
		stop();
	});
});

describe('Pause live updates (WCAG 2.2.2)', () => {
	test('a paused poller loads once at the start and then not by itself', async () => {
		setLivePaused(true);
		const fn = vi.fn().mockResolvedValue(undefined);
		const stop = poll(fn, { every: 1000 });
		await vi.advanceTimersByTimeAsync(10_000);
		expect(fn).toHaveBeenCalledTimes(1);
		// Not when the tab is shown again either
		setHidden(true);
		setHidden(false);
		await vi.advanceTimersByTimeAsync(5000);
		expect(fn).toHaveBeenCalledTimes(1);
		stop();
	});

	test('Refresh loads every poller on screen once; the end of the pause loads and goes back to the timer', async () => {
		setLivePaused(true);
		const a = vi.fn().mockResolvedValue(undefined);
		const b = vi.fn().mockResolvedValue(undefined);
		const stopA = poll(a, { every: 1000 });
		const stopB = reloadWhileShown(b, 1000);
		await vi.advanceTimersByTimeAsync(5000);
		expect([a.mock.calls.length, b.mock.calls.length]).toEqual([1, 0]);
		expect(liveUpdates.active).toBe(2);
		await refreshNow();
		expect([a.mock.calls.length, b.mock.calls.length]).toEqual([2, 1]);
		await vi.advanceTimersByTimeAsync(5000);
		expect([a.mock.calls.length, b.mock.calls.length]).toEqual([2, 1]);
		setLivePaused(false);
		await vi.advanceTimersByTimeAsync(0);
		expect([a.mock.calls.length, b.mock.calls.length]).toEqual([3, 2]);
		await vi.advanceTimersByTimeAsync(1000);
		expect([a.mock.calls.length, b.mock.calls.length]).toEqual([4, 3]);
		stopA();
		stopB();
		expect(liveUpdates.active).toBe(0);
	});

	test('an essential poller keeps running, and is not one Refresh counts', async () => {
		setLivePaused(true);
		const fn = vi.fn().mockResolvedValue(undefined);
		const stop = poll(fn, { every: 1000, essential: true });
		await vi.advanceTimersByTimeAsync(3000);
		expect(fn).toHaveBeenCalledTimes(4);
		expect(liveUpdates.active).toBe(0);
		stop();
	});

	test('the time of the last load that worked', async () => {
		vi.setSystemTime(new Date('2026-11-26T10:00:00Z'));
		const fn = vi.fn().mockRejectedValueOnce(new Error('x')).mockResolvedValue(undefined);
		const stop = poll(fn, { every: 1000 });
		await vi.advanceTimersByTimeAsync(0);
		expect(liveUpdates.lastUpdated).toBeNull();
		await vi.advanceTimersByTimeAsync(2000);
		expect(liveUpdates.lastUpdated).toBe(Date.parse('2026-11-26T10:00:02Z'));
		stop();
		expect(liveUpdates.lastUpdated).toBeNull();
	});
});

class FakeWS {
	static OPEN = 1;
	static all: FakeWS[] = [];
	readyState = 0;
	url: string;
	sent: string[] = [];
	onopen: (() => void) | null = null;
	onmessage: ((e: { data: unknown }) => void) | null = null;
	onclose: (() => void) | null = null;
	onerror: (() => void) | null = null;
	constructor(url: string) {
		this.url = url;
		FakeWS.all.push(this);
	}
	open() {
		this.readyState = 1;
		this.onopen?.();
	}
	send(d: string) {
		this.sent.push(d);
	}
	close() {
		this.readyState = 3;
		this.onclose?.();
	}
}

describe('socket', () => {
	beforeEach(() => {
		FakeWS.all = [];
		vi.stubGlobal('WebSocket', FakeWS);
	});
	afterEach(() => vi.unstubAllGlobals());

	test('connects same-origin over ws, parses JSON, reconnects with backoff', async () => {
		const got: unknown[] = [];
		const s = socket('/ws/device-status', { onmessage: (d) => got.push(d) });
		expect(FakeWS.all[0]!.url).toBe(
			'ws://localhost:3000/ws/device-status'.replace('localhost:3000', location.host)
		);
		FakeWS.all[0]!.open();
		FakeWS.all[0]!.onmessage?.({ data: '{"id":"1","online":true}' });
		expect(got).toEqual([{ id: '1', online: true }]);
		expect(s.send({ a: 1 })).toBe(true);
		FakeWS.all[0]!.close();
		await vi.advanceTimersByTimeAsync(1300);
		expect(FakeWS.all).toHaveLength(2);
		s.close();
		await vi.advanceTimersByTimeAsync(60_000);
		expect(FakeWS.all).toHaveLength(2);
	});

	test('closes after the tab stays hidden and reopens on show', async () => {
		const s = socket('/ws/x', { onmessage: () => {}, hiddenGrace: 1000 });
		FakeWS.all[0]!.open();
		setHidden(true);
		await vi.advanceTimersByTimeAsync(1000);
		expect(FakeWS.all[0]!.readyState).toBe(3);
		await vi.advanceTimersByTimeAsync(60_000);
		expect(FakeWS.all).toHaveLength(1);
		setHidden(false);
		expect(FakeWS.all).toHaveLength(2);
		s.close();
	});
});
