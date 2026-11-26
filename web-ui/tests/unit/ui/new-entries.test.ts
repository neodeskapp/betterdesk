/**
 * Pause live updates and lists that load again on a server push
 * (state/held-pushes.svelte.ts, ui/NewEntries.svelte): while paused a push
 * leaves the list as it is, says once through the polite live region that
 * new entries wait, and "New entries, show them" loads them and puts focus
 * on the list. The end of the pause loads them too.
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, test, vi } from 'vitest';

import NewEntries from '../../../src/lib/ui/NewEntries.svelte';
import { HeldPushes } from '../../../src/lib/state/held-pushes.svelte.ts';
import { setLivePaused } from '../../../src/lib/state/live-updates.svelte.ts';
import { announcer, resetAnnouncer } from '../../../src/lib/ui/announce.svelte.ts';

afterEach(() => {
	cleanup();
	setLivePaused(false);
	resetAnnouncer();
	document.body.innerHTML = '';
});

describe('HeldPushes', () => {
	test('without the pause a push loads at once and nothing waits', async () => {
		const load = vi.fn(async () => {});
		const held = new HeldPushes(load);
		held.push();
		expect(load).toHaveBeenCalledTimes(1);
		expect(held.waiting).toBe(false);
	});

	test('while paused a push loads nothing and is announced once', async () => {
		setLivePaused(true);
		const load = vi.fn(async () => {});
		const held = new HeldPushes(load);
		held.push();
		held.push();
		expect(load).not.toHaveBeenCalled();
		expect(held.waiting).toBe(true);
		await vi.waitFor(() => expect(announcer.polite).toBe('live.new_entries_waiting'));
	});
	test('a push during a load still waits after that load', () => {
		setLivePaused(true);
		const held = new HeldPushes(async () => {});
		const from = held.begin();
		held.push();
		held.loaded(from);
		expect(held.waiting).toBe(true);
		held.loaded(held.begin());
		expect(held.waiting).toBe(false);
	});
});

describe('NewEntries', () => {
	function setup() {
		setLivePaused(true);
		const list = document.createElement('div');
		list.className = 'the-list';
		document.body.append(list);
		const load = vi.fn(async () => {});
		const held = new HeldPushes(load);
		render(NewEntries, { props: { held, focus: () => list } });
		return { list, load, held };
	}

	test('shows nothing until a push waits', () => {
		setup();
		expect(screen.queryByRole('button', { name: 'live.new_entries' })).toBeNull();
	});

	test('the button loads the entries, goes away and leaves focus on the list', async () => {
		const { list, load, held } = setup();
		held.push();
		const button = await screen.findByRole('button', { name: 'live.new_entries' });
		await userEvent.click(button);
		expect(load).toHaveBeenCalledTimes(1);
		await vi.waitFor(() => expect(screen.queryByRole('button', { name: 'live.new_entries' })).toBeNull());
		expect(document.activeElement).toBe(list);
	});

	test('the end of the pause loads what waits', async () => {
		const { load, held } = setup();
		held.push();
		await screen.findByRole('button', { name: 'live.new_entries' });
		setLivePaused(false);
		await vi.waitFor(() => expect(load).toHaveBeenCalledTimes(1));
		await vi.waitFor(() => expect(held.waiting).toBe(false));
	});
});
