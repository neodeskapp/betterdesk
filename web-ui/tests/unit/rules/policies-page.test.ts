/**
 * The policies page offers Save only to accounts whose save the server
 * takes: every admin role may read an organization's policies, writing
 * them needs org.edit (A83). Without it the groups show read-only, the
 * page says so, and no write goes out.
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

const pageState = vi.hoisted(() => ({
	url: new URL('http://localhost/app/policies/org-1'),
	params: { orgId: 'org-1' }
}));
vi.mock('$app/state', () => ({ page: pageState }));
vi.mock('$app/paths', () => ({ resolve: (p: string) => '/app' + (p || '/'), asset: (p: string) => '/' + p }));
vi.mock('$app/navigation', () => ({ goto: vi.fn(), pushState: vi.fn(), replaceState: vi.fn() }));

import Page from '../../../src/routes/(console)/policies/[[orgId]]/+page.svelte';
import { session } from '../../../src/lib/state/session.svelte.ts';
import { dismissAll } from '../../../src/lib/ui/toasts.svelte.ts';

let writes: string[] = [];

const ANSWERS: Record<string, unknown> = {
	'/api/panel/org': { organizations: [{ id: 'org-1', name: 'Front office' }] },
	'/api/panel/policies/org-1': { connection: { mode: 'unattended' } },
	'/api/panel/policies/org-1/audit': { entries: [] },
	'/api/panel/org/org-1/devices': { devices: [] }
};

beforeEach(() => {
	writes = [];
	vi.stubGlobal('fetch', async (input: string, init: RequestInit = {}) => {
		const url = new URL(input, location.origin);
		const method = init.method ?? 'GET';
		if (method !== 'GET') writes.push(`${method} ${url.pathname}`);
		return new Response(JSON.stringify(ANSWERS[url.pathname] ?? { success: true }), {
			status: 200,
			headers: { 'Content-Type': 'application/json' }
		});
	});
});

afterEach(() => {
	cleanup();
	dismissAll();
	vi.unstubAllGlobals();
	session.permissions = [];
	session.user = null;
});

function signIn(role: string, permissions: string[]) {
	session.isSuperAdmin = false;
	session.permissions = permissions;
	session.user = { id: 1, username: 'demo', role, preferredLanguage: null, avatarVersion: null };
}

async function openPage() {
	render(Page, { params: { orgId: 'org-1' } } as never);
	await vi.waitFor(() =>
		expect(screen.getByRole('heading', { name: 'policies.connection_title' })).toBeInTheDocument()
	);
}

describe('policies page', () => {
	test('server_admin (no org.edit) reads the groups but gets no Save and no write', async () => {
		signIn('server_admin', ['server.config', 'user.view', 'device.view']);
		await openPage();
		expect(screen.queryByRole('button', { name: 'policies.save_button' })).toBeNull();
		expect(screen.getByText('You can view this page but not change it.')).toBeInTheDocument();
		const mode = screen.getByLabelText('policies.connection_mode');
		expect(mode).toBeDisabled();
		expect(screen.getByLabelText('policies.allow_file_transfer')).toBeDisabled();
		expect(writes).toEqual([]);
	});

	test('global_admin (org.edit) gets the form and Save', async () => {
		signIn('global_admin', ['org.edit', 'org.manage_users', 'user.view', 'device.view']);
		await openPage();
		expect(screen.getByRole('button', { name: 'policies.save_button' })).toBeInTheDocument();
		expect(screen.queryByText('You can view this page but not change it.')).toBeNull();
		expect(screen.getByLabelText('policies.connection_mode')).toBeEnabled();
	});
});
