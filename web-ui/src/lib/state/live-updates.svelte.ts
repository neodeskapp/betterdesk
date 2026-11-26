/**
 * Pause live updates (the accessibility option, WCAG 2.2.2): while it is
 * on, lists and pages do not load again by themselves. live.ts asks
 * `liveUpdates.paused` before each timed run; a poller marked essential
 * (the session check, a dialog that waits for a computer) keeps running.
 *
 * The pollers on screen register here, so a Refresh button can run them
 * all once (refreshNow) and LiveStatus can say when they last loaded.
 */

interface Poller {
	/** Run now, whatever the pause says */
	refresh(): Promise<void>;
	/** The pause ended: run now and go back to the timer */
	resume(): void;
}

class LiveUpdates {
	paused = $state(false);
	/** Pollers on screen that the pause stops */
	active = $state(0);
	/** When one of them last loaded without an error (ms), null before */
	lastUpdated = $state<number | null>(null);
	/** LiveStatus lines on screen (the shell shows its own only without one) */
	lines = $state(0);
	/** A Refresh is running */
	refreshing = $state(false);
}

export const liveUpdates = new LiveUpdates();

// eslint-disable-next-line svelte/prefer-svelte-reactivity -- a registry; liveUpdates.active carries the count
const pollers = new Set<Poller>();

export function registerPoller(p: Poller): () => void {
	pollers.add(p);
	liveUpdates.active = pollers.size;
	return () => {
		pollers.delete(p);
		liveUpdates.active = pollers.size;
		if (pollers.size === 0) liveUpdates.lastUpdated = null;
	};
}

/** A pausable poller loaded */
export function markUpdated(at: number = Date.now()): void {
	liveUpdates.lastUpdated = at;
}

export function setLivePaused(on: boolean): void {
	if (liveUpdates.paused === on) return;
	liveUpdates.paused = on;
	if (!on) for (const p of [...pollers]) p.resume();
}

/** Load everything on screen once (the Refresh button) */
export async function refreshNow(): Promise<void> {
	liveUpdates.refreshing = true;
	try {
		await Promise.all([...pollers].map((p) => p.refresh()));
	} finally {
		liveUpdates.refreshing = false;
	}
}
