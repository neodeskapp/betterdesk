/**
 * The data loss prevention page asks for one page of events at a time and
 * takes its counts from /stats: the event table grows only when Older
 * events is pressed, and the Policies tab loads no events at all.
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

const pageState = vi.hoisted(() => ({ url: new URL('http://localhost/dataguard'), state: {} }));
vi.mock('$app/state', () => ({ page: pageState }));
vi.mock('$app/paths', () => ({ resolve: (p: string) => p || '/', asset: (p: string) => '/' + p }));
vi.mock('$app/navigation', () => ({ goto: vi.fn(), pushState: vi.fn(), replaceState: vi.fn() }));

import Page from '../../../src/routes/(console)/dataguard/+page.svelte';
import { session } from '../../../src/lib/state/session.svelte.ts';
import { dismissAll } from '../../../src/lib/ui/toasts.svelte.ts';

/** Events newest first, one a minute */
const all = Array.from({ length: 150 }, (_, i) => ({
	id: 1000 - i,
	device_id: `dev${i}`,
	policy_id: i % 2 ? 1 : 2,
	policy_name: 'USB',
	action: 'block',
	created_at: new Date(Date.UTC(2026, 10, 20, 12, 0) - i * 60_000).toISOString()
}));
let eventQueries: URLSearchParams[] = [];
let requests: string[] = [];

beforeEach(() => {
	eventQueries = [];
	requests = [];
	vi.stubGlobal('fetch', async (input: string) => {
		const url = new URL(input, location.origin);
		requests.push(url.pathname);
		let data: unknown = {};
		if (url.pathname === '/api/dataguard/policies') data = [{ id: 1, name: 'USB', enabled: 1 }];
		if (url.pathname === '/api/dataguard/stats')
			data = { total: 150, blocked: 150, logged: 0, usb_events: 150, file_events: 0, by_policy: { '1': 75 } };
		if (url.pathname === '/api/dataguard/events') {
			eventQueries.push(url.searchParams);
			const limit = Number(url.searchParams.get('limit')) || all.length;
			const to = url.searchParams.get('to');
			data = all.filter((e) => !to || e.created_at <= to).slice(0, limit);
		}
		return {
			ok: true,
			status: 200,
			redirected: false,
			url: url.href,
			headers: new Headers({ 'content-type': 'application/json' }),
			json: async () => data,
			text: async () => JSON.stringify(data)
		} as unknown as Response;
	});
});

afterEach(() => {
	cleanup();
	dismissAll();
	vi.unstubAllGlobals();
	session.isSuperAdmin = false;
	session.permissions = [];
	session.user = null;
	pageState.url = new URL('http://localhost/dataguard');
});

function signIn() {
	session.isSuperAdmin = false;
	session.permissions = ['server.config', 'audit.view', 'device.view'];
	session.user = {
		id: 1,
		username: 'demo',
		role: 'server_admin',
		preferredLanguage: null,
		avatarVersion: null
	};
}

/** Rows of the event table (role queries are slow over a hundred rows) */
const eventRows = () => document.querySelectorAll('tbody tr[data-index]');
const olderButton = () =>
	[...document.querySelectorAll('button')].find((b) => b.textContent?.trim() === 'Older events') ?? null;

describe('data loss prevention page', () => {
	test('events come one page at a time with a limit, older pages end where the last one stopped', async () => {
		pageState.url = new URL('http://localhost/dataguard?tab=events');
		signIn();
		render(Page);
		await vi.waitFor(() => expect(eventRows().length).toBe(100), { timeout: 5000 });
		expect(eventQueries[0]?.get('limit')).toBe('100');
		expect(eventQueries[0]?.has('to')).toBe(false);

		await userEvent.click(olderButton()!);
		await vi.waitFor(() => expect(eventRows().length).toBe(150), { timeout: 5000 });
		expect(eventQueries[1]?.get('limit')).toBe('100');
		expect(eventQueries[1]?.get('to')).toBe(all[99]?.created_at);
		expect(olderButton()).toBeNull();
	}, 20_000); // a hundred rows take a while in jsdom

	test('the Policies tab counts per policy from /stats and loads no events', async () => {
		signIn();
		render(Page);
		await vi.waitFor(() => expect(requests).toContain('/api/dataguard/stats'));
		const table = await screen.findByRole('table', { name: 'dataguard.tab_policies' });
		await vi.waitFor(() =>
			expect(table.querySelector('tbody tr[data-index] td.is-num')).toHaveTextContent('75')
		);
		expect(requests).not.toContain('/api/dataguard/events');
	});
});
