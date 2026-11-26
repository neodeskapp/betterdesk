/**
 * The device list's page head: the list keeps itself current (status
 * pushes and a reload every 60 s), so the head offers no Sync status
 * button, and opening the page asks nothing of POST /api/sync-status.
 */
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

const pageUrl = vi.hoisted(() => ({ url: new URL('http://localhost/app/devices') }));
vi.mock('$app/state', () => ({ page: pageUrl }));
vi.mock('$app/paths', () => ({ resolve: (p: string) => '/app' + (p || '/'), asset: (p: string) => '/' + p }));
vi.mock('$app/navigation', () => ({ goto: vi.fn(), pushState: vi.fn(), replaceState: vi.fn() }));

import Page from '../../src/routes/(console)/devices/+page.svelte';
import { session } from '../../src/lib/state/session.svelte.ts';
import { dismissAll } from '../../src/lib/ui/toasts.svelte.ts';
import { setLivePaused } from '../../src/lib/state/live-updates.svelte.ts';

let requests: string[] = [];
let devices: object[] = [];

class QuietSocket extends EventTarget {
	static last: QuietSocket | null = null;
	readyState = 0;
	onmessage: ((e: { data: string }) => void) | null = null;
	constructor() {
		super();
		QuietSocket.last = this;
	}
	close() {}
	send() {}
}

beforeEach(() => {
	requests = [];
	devices = [];
	vi.stubGlobal('WebSocket', QuietSocket);
	vi.stubGlobal('fetch', async (input: string, init: RequestInit = {}) => {
		const url = new URL(input, location.origin);
		requests.push(`${init.method ?? 'GET'} ${url.pathname}`);
		const data = url.pathname === '/api/devices' ? { success: true, data: { devices } } : { success: true };
		return new Response(JSON.stringify(data), {
			status: 200,
			headers: { 'Content-Type': 'application/json' }
		});
	});
	session.isSuperAdmin = false;
	session.permissions = ['device.view', 'device.edit'];
});

afterEach(() => {
	cleanup();
	dismissAll();
	vi.unstubAllGlobals();
	session.permissions = [];
	setLivePaused(false);
});

const early = () => ({
	data: {
		early: {
			devices: Promise.resolve(null),
			folders: Promise.resolve(null),
			groups: Promise.resolve(null),
			tags: Promise.resolve(null)
		}
	}
});

describe('device list page head', () => {
	test('has no Sync status button and never asks to sync', async () => {
		render(Page, early());
		await vi.waitFor(() => expect(requests).toContain('GET /api/devices'));
		expect(screen.getByRole('heading', { level: 1, name: 'devices.title' })).toBeInTheDocument();
		expect(screen.queryByRole('button', { name: 'devices.sync_status' })).toBeNull();
		expect(requests).not.toContain('POST /api/sync-status');
	});
});

describe('Pause live updates', () => {
	const listLoads = () => requests.filter((r) => r === 'GET /api/devices').length;
	const push = (data: unknown) => QuietSocket.last?.onmessage?.({ data: JSON.stringify(data) });

	test('the list keeps what it shows until Refresh; the page head says so', async () => {
		setLivePaused(true);
		render(Page, early());
		await vi.waitFor(() => expect(listLoads()).toBe(1));
		expect(screen.getByText('live.paused')).toBeInTheDocument();
		push({ type: 'device_id_changed', old_id: 'a', new_id: 'z' });
		await new Promise((r) => setTimeout(r, 50));
		expect(listLoads()).toBe(1);
		await userEvent.click(screen.getByRole('button', { name: 'common.refresh' }));
		await vi.waitFor(() => expect(listLoads()).toBe(2));
	});

	test('a push while paused offers New entries, show them, which loads the list', async () => {
		setLivePaused(true);
		render(Page, early());
		await vi.waitFor(() => expect(listLoads()).toBe(1));
		expect(screen.queryByRole('button', { name: 'live.new_entries' })).toBeNull();
		push({ type: 'device_id_changed', old_id: 'a', new_id: 'z' });
		await userEvent.click(await screen.findByRole('button', { name: 'live.new_entries' }));
		await vi.waitFor(() => expect(listLoads()).toBe(2));
		await vi.waitFor(() => expect(document.activeElement?.classList.contains('devices-table')).toBe(true));
	});

	test('without the pause a push loads the list again and there is no Refresh', async () => {
		render(Page, early());
		await vi.waitFor(() => expect(listLoads()).toBe(1));
		expect(screen.queryByRole('button', { name: 'common.refresh' })).toBeNull();
		push({ type: 'device_id_changed', old_id: 'a', new_id: 'z' });
		await vi.waitFor(() => expect(listLoads()).toBe(2));
	});
});

describe('device row menu', () => {
	beforeEach(() => {
		// jsdom lays nothing out: the menu's box is where its style puts it,
		// so the correction for transformed ancestors finds nothing to correct
		const real = HTMLElement.prototype.getBoundingClientRect;
		vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
			if (!this.matches('[role="menu"]')) return real.call(this);
			const x = parseFloat(this.style.left) || 0;
			const y = parseFloat(this.style.top) || 0;
			return new DOMRect(x, y, 0, 0);
		});
	});
	afterEach(() => vi.restoreAllMocks());

	async function row() {
		devices = [{ id: '987654321', hostname: 'desk-1', online: true, tags: [] }];
		render(Page, early());
		const cell = await screen.findByText('987654321');
		const tr = cell.closest('tr');
		if (!tr) throw new Error('no row');
		return tr;
	}

	test('a right-click opens the menu where the pointer is', async () => {
		const tr = await row();
		await fireEvent.contextMenu(tr, { clientX: 300, clientY: 200 });
		const menu = await screen.findByRole('menu');
		expect(menu.style.left).toBe('300px');
		expect(menu.style.top).toBe('200px');
		// A right-click elsewhere in the row moves it
		await fireEvent.pointerDown(document.body);
		await fireEvent.contextMenu(tr, { clientX: 420, clientY: 260 });
		const moved = await screen.findByRole('menu');
		expect(moved.style.left).toBe('420px');
		expect(moved.style.top).toBe('260px');
	});

	test('the menu button still opens it below the button', async () => {
		const tr = await row();
		await fireEvent.contextMenu(tr, { clientX: 300, clientY: 200 });
		await fireEvent.pointerDown(document.body);
		expect(screen.queryByRole('menu')).toBeNull();
		await userEvent.click(screen.getByRole('button', { name: /devices\.actions/ }));
		const menu = await screen.findByRole('menu');
		expect(menu.style.left).not.toBe('300px');
		expect(menu.style.top).not.toBe('200px');
	});
});
