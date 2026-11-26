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

let requests: string[] = [];
let devices: object[] = [];

class QuietSocket extends EventTarget {
	readyState = 0;
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
