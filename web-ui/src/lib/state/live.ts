/**
 * Real-time data: one poller or one socket per kind of data, with
 * reconnect and backoff, paused while the tab is hidden.
 *
 *   const stop = poll(() => loadDevices(), { every: 15_000 });
 *   const sock = socket('/ws/device-status', { onmessage: (data) => ... });
 *
 * A hidden tab makes no requests: the poller stops and runs once when the
 * tab is shown again; a socket closes after a grace period and reopens on
 * show. Errors back off exponentially (capped) and reset on success.
 *
 * Pause live updates (accessibility settings, live-updates.svelte.ts): a
 * poller still loads once at its start, then only on Refresh, until the
 * pause ends. A poller marked essential keeps its timer: the session
 * check, a dialog waiting for a computer, a job someone started.
 */

import { liveUpdates, markUpdated, registerPoller } from './live-updates.svelte.ts';

type Visibility = { hidden: boolean; on(fn: () => void): () => void };

const visibility: Visibility = {
	get hidden() {
		return typeof document !== 'undefined' && document.visibilityState === 'hidden';
	},
	on(fn) {
		document.addEventListener('visibilitychange', fn);
		return () => document.removeEventListener('visibilitychange', fn);
	}
};

export interface PollOptions {
	/** Interval in ms while things work */
	every: number;
	/** Longest wait after repeated errors */
	maxBackoff?: number;
	/** Run once right away (default true) */
	immediate?: boolean;
	/** Keeps running while live updates are paused (not content people read) */
	essential?: boolean;
}

/** Call `fn` every `every` ms while the tab is visible. Returns stop(). */
export function poll(fn: () => Promise<unknown>, opts: PollOptions): () => void {
	const maxBackoff = opts.maxBackoff ?? Math.max(opts.every * 8, 60_000);
	let timer: ReturnType<typeof setTimeout> | null = null;
	let stopped = false;
	let running = false;
	let failures = 0;
	const pausable = !opts.essential;
	const paused = () => pausable && liveUpdates.paused;

	const schedule = (ms: number) => {
		if (timer) clearTimeout(timer);
		timer = null;
		if (stopped || visibility.hidden || paused()) return;
		timer = setTimeout(run, ms);
	};

	async function load(): Promise<void> {
		running = true;
		try {
			await fn();
			failures = 0;
			if (pausable) markUpdated();
		} catch {
			failures++;
		} finally {
			running = false;
		}
	}

	async function run() {
		timer = null;
		if (stopped || visibility.hidden || running) return;
		await load();
		schedule(failures ? Math.min(opts.every * 2 ** failures, maxBackoff) : opts.every);
	}

	const off = visibility.on(() => {
		if (visibility.hidden) {
			if (timer) clearTimeout(timer);
			timer = null;
		} else if (!timer && !running && !paused()) {
			void run();
		}
	});

	const unregister = pausable
		? registerPoller({
				refresh: async () => {
					if (stopped || running) return;
					await load();
				},
				resume: () => {
					if (!stopped && !timer && !running) void run();
				}
			})
		: () => {};

	if (opts.immediate === false) schedule(opts.every);
	else void run();

	return () => {
		stopped = true;
		if (timer) clearTimeout(timer);
		off();
		unregister();
	};
}

export interface SocketOptions {
	onmessage: (data: unknown) => void;
	onstatus?: (status: 'connecting' | 'open' | 'closed') => void;
	/** Close this long after the tab is hidden (ms, default 30 s) */
	hiddenGrace?: number;
	maxBackoff?: number;
}

export interface LiveSocket {
	send(data: unknown): boolean;
	close(): void;
}

/** A WebSocket to a same-origin path that reconnects with backoff. JSON messages are parsed. */
export function socket(path: string, opts: SocketOptions): LiveSocket {
	const maxBackoff = opts.maxBackoff ?? 30_000;
	let ws: WebSocket | null = null;
	let closed = false;
	let attempts = 0;
	let retry: ReturnType<typeof setTimeout> | null = null;
	let hideTimer: ReturnType<typeof setTimeout> | null = null;

	const url = () => {
		const u = new URL(path, location.href);
		u.protocol = location.protocol === 'https:' ? 'wss:' : 'ws:';
		return u.toString();
	};

	function connect() {
		if (closed || visibility.hidden || ws) return;
		opts.onstatus?.('connecting');
		const s = new WebSocket(url());
		ws = s;
		s.onopen = () => {
			attempts = 0;
			opts.onstatus?.('open');
		};
		s.onmessage = (e) => {
			let data: unknown = e.data;
			if (typeof e.data === 'string') {
				try {
					data = JSON.parse(e.data);
				} catch {
					data = e.data;
				}
			}
			opts.onmessage(data);
		};
		s.onclose = () => {
			if (ws === s) ws = null;
			opts.onstatus?.('closed');
			if (closed || visibility.hidden) return;
			attempts++;
			const wait = Math.min(1000 * 2 ** (attempts - 1), maxBackoff);
			retry = setTimeout(connect, wait + Math.random() * 250);
		};
		s.onerror = () => s.close();
	}

	const off = visibility.on(() => {
		if (visibility.hidden) {
			hideTimer = setTimeout(() => {
				if (retry) clearTimeout(retry);
				ws?.close();
				ws = null;
			}, opts.hiddenGrace ?? 30_000);
		} else {
			if (hideTimer) clearTimeout(hideTimer);
			hideTimer = null;
			connect();
		}
	});

	connect();

	return {
		send(data) {
			if (!ws || ws.readyState !== WebSocket.OPEN) return false;
			ws.send(typeof data === 'string' ? data : JSON.stringify(data));
			return true;
		},
		close() {
			closed = true;
			if (retry) clearTimeout(retry);
			if (hideTimer) clearTimeout(hideTimer);
			off();
			ws?.close();
			ws = null;
		}
	};
}

/** How often a list that other people change loads again while it is on screen */
export const RELOAD_EVERY = 30_000;

/**
 * Load a page's data again every 30 s while the tab is visible, and once
 * when it is shown again; the page's own first load comes before. Pages
 * whose data changes elsewhere use it, since the top bar has no Refresh.
 * Returns stop().
 */
export function reloadWhileShown(fn: () => unknown, every: number = RELOAD_EVERY): () => void {
	return poll(async () => void (await fn()), { every, immediate: false });
}
