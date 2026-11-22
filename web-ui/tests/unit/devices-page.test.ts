/**
 * The device list's page head: the list keeps itself current (status
 * pushes and a reload every 60 s), so the head offers no Sync status
 * button, and opening the page asks nothing of POST /api/sync-status.
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

const pageUrl = vi.hoisted(() => ({ url: new URL('http://localhost/app/devices') }));
vi.mock('$app/state', () => ({ page: pageUrl }));
vi.mock('$app/paths', () => ({ resolve: (p: string) => '/app' + (p || '/'), asset: (p: string) => '/' + p }));
vi.mock('$app/navigation', () => ({ goto: vi.fn(), pushState: vi.fn(), replaceState: vi.fn() }));

import Page from '../../src/routes/(console)/devices/+page.svelte';
import { session } from '../../src/lib/state/session.svelte.ts';
import { dismissAll } from '../../src/lib/ui/toasts.svelte.ts';

let requests: string[] = [];

class QuietSocket extends EventTarget {
	readyState = 0;
	close() {}
	send() {}
}

beforeEach(() => {
	requests = [];
	vi.stubGlobal('WebSocket', QuietSocket);
	vi.stubGlobal('fetch', async (input: string, init: RequestInit = {}) => {
		const url = new URL(input, location.origin);
		requests.push(`${init.method ?? 'GET'} ${url.pathname}`);
		const data = url.pathname === '/api/devices' ? { success: true, devices: [] } : { success: true };
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
