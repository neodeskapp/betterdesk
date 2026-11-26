import { cleanup, render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, expect, test, vi } from 'vitest';
import { poll } from '../../../src/lib/state/live.ts';
import { liveUpdates, setLivePaused } from '../../../src/lib/state/live-updates.svelte.ts';
import LiveStatus from '../../../src/lib/ui/LiveStatus.svelte';
import { announcer } from '../../../src/lib/ui/announce.svelte.ts';

afterEach(() => {
	cleanup();
	setLivePaused(false);
});

test('nothing before a load; then the time of the last load', async () => {
	render(LiveStatus);
	expect(document.querySelector('.live-status')).toBeNull();
	const stop = poll(async () => {}, { every: 60_000 });
	await vi.waitFor(() => expect(screen.getByText('live.last_updated')).toBeInTheDocument());
	expect(screen.queryByRole('button')).toBeNull();
	stop();
});

test('paused: says so, and Refresh loads the page once and reads the new time', async () => {
	setLivePaused(true);
	const fn = vi.fn(async () => {});
	const stop = poll(fn, { every: 60_000 });
	render(LiveStatus);
	expect(screen.getByText('live.paused')).toBeInTheDocument();
	await vi.waitFor(() => expect(fn).toHaveBeenCalledTimes(1));
	await userEvent.click(screen.getByRole('button', { name: 'common.refresh' }));
	expect(fn).toHaveBeenCalledTimes(2);
	await vi.waitFor(() => expect(announcer.polite).toBe('live.last_updated'));
	stop();
});

test("a page's line counts, the shell's own does not", () => {
	const page = render(LiveStatus);
	expect(liveUpdates.lines).toBe(1);
	page.unmount();
	expect(liveUpdates.lines).toBe(0);
	render(LiveStatus, { fallback: true });
	expect(liveUpdates.lines).toBe(0);
});
