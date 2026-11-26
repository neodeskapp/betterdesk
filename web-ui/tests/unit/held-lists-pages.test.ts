/**
 * Help requests and Registrations load again when the server pushes a
 * change. While live updates are paused the push leaves the list as it is
 * and "New entries, show them" appears above it; pressing it loads the
 * list. Without the pause the push loads the list at once.
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

const pageUrl = vi.hoisted(() => ({ url: new URL('http://localhost/app/help-requests') }));
vi.mock('$app/state', () => ({ page: pageUrl }));
vi.mock('$app/paths', () => ({ resolve: (p: string) => '/app' + (p || '/'), asset: (p: string) => '/' + p }));
vi.mock('$app/navigation', () => ({ goto: vi.fn(), pushState: vi.fn(), replaceState: vi.fn() }));

type Listener = (p: { type: string }) => void;
const events = vi.hoisted(() => ({ listeners: new Set<(p: { type: string }) => void>() }));
vi.mock('#lib/shell/live-events.svelte.ts', () => ({
	onPanelEvents: (fn: Listener) => {
		events.listeners.add(fn);
		return () => events.listeners.delete(fn);
	},
	loadRegistrationCount: vi.fn(async () => {}),
	startLiveEvents: vi.fn(),
	stopLiveEvents: vi.fn()
}));

import HelpRequests from '../../src/routes/(console)/help-requests/+page.svelte';
import Registrations from '../../src/routes/(console)/registrations/+page.svelte';
import { session } from '../../src/lib/state/session.svelte.ts';
import { setLivePaused } from '../../src/lib/state/live-updates.svelte.ts';
import { dismissAll } from '../../src/lib/ui/toasts.svelte.ts';

let requests: string[] = [];

beforeEach(() => {
	requests = [];
	events.listeners.clear();
	vi.stubGlobal('matchMedia', (query: string) => ({
		matches: false,
		media: query,
		addEventListener() {},
		removeEventListener() {}
	}));
	vi.stubGlobal('fetch', async (input: string, init: RequestInit = {}) => {
		const url = new URL(input, location.origin);
		requests.push(`${init.method ?? 'GET'} ${url.pathname}`);
		const data =
			url.pathname === '/api/help/requests' ? { success: true, requests: [] } : { success: true, data: [] };
		return new Response(JSON.stringify(data), {
			status: 200,
			headers: { 'Content-Type': 'application/json' }
		});
	});
	session.isSuperAdmin = false;
	session.permissions = ['chat.support', 'enrollment.approve'];
});

afterEach(() => {
	cleanup();
	dismissAll();
	vi.unstubAllGlobals();
	session.permissions = [];
	setLivePaused(false);
});

const push = (type: string) => {
	for (const fn of events.listeners) fn({ type });
};
const pause = () => new Promise((r) => setTimeout(r, 50));

const pages = [
	{ name: 'Help requests', Page: HelpRequests, path: 'GET /api/help/requests', event: 'help_request' },
	{
		name: 'Registrations',
		Page: Registrations,
		path: 'GET /api/enrollment/pending',
		event: 'registration_pending'
	}
];

describe.each(pages)('$name', ({ Page, path, event }) => {
	const loads = () => requests.filter((r) => r === path).length;

	test('while paused a push waits for New entries, show them', async () => {
		setLivePaused(true);
		render(Page, {});
		await vi.waitFor(() => expect(loads()).toBe(1));
		push(event);
		await pause();
		expect(loads()).toBe(1);
		await userEvent.click(await screen.findByRole('button', { name: 'live.new_entries' }));
		await vi.waitFor(() => expect(loads()).toBe(2));
		await vi.waitFor(() => expect(screen.queryByRole('button', { name: 'live.new_entries' })).toBeNull());
	});

	test('without the pause a push loads the list at once', async () => {
		render(Page, {});
		await vi.waitFor(() => expect(loads()).toBe(1));
		push(event);
		await vi.waitFor(() => expect(loads()).toBe(2));
		expect(screen.queryByRole('button', { name: 'live.new_entries' })).toBeNull();
	});
});
