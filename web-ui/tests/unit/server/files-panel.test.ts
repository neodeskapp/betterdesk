/**
 * Server management's Files are read-only (A194): an admin can browse the
 * folders and open a file, but nothing offers to create, rename, delete or
 * save, and the panel sends no write.
 */
import { cleanup, render, screen, within } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

vi.mock('$app/state', () => ({ page: { url: new URL('http://localhost/app/server-management?tab=files') } }));
vi.mock('$app/paths', () => ({ resolve: (p: string) => '/app' + (p || '/'), asset: (p: string) => '/' + p }));
vi.mock('$app/navigation', () => ({ goto: vi.fn(), pushState: vi.fn(), replaceState: vi.fn() }));

import FilesPanel from '../../../src/routes/(console)/server-management/FilesPanel.svelte';
import { session } from '../../../src/lib/state/session.svelte.ts';
import { dismissAll } from '../../../src/lib/ui/toasts.svelte.ts';

let writes: string[] = [];

const ANSWERS: Record<string, unknown> = {
	'/api/server-management/info': {
		success: true,
		platform: 'linux',
		fileRoots: ['/opt/rustdesk'],
		mutableFileRoots: ['/opt/rustdesk']
	},
	'/api/server-management/files': {
		success: true,
		path: '/opt/rustdesk',
		cwd: '/opt/rustdesk',
		items: [
			{ name: 'logs', path: '/opt/rustdesk/logs', type: 'dir', size: 0, mode: 0o40755 },
			{ name: 'note.txt', path: '/opt/rustdesk/note.txt', type: 'file', size: 6, mode: 0o100644 }
		]
	},
	'/api/server-management/files/read': {
		success: true,
		path: '/opt/rustdesk/note.txt',
		size: 6,
		isBinary: false,
		truncated: false,
		content: 'hello\n'
	}
};

beforeEach(() => {
	writes = [];
	session.isSuperAdmin = true;
	session.permissions = ['server.config'];
	vi.stubGlobal('fetch', async (input: string, init: RequestInit = {}) => {
		const url = new URL(input, location.origin);
		const method = init.method ?? 'GET';
		if (method !== 'GET') writes.push(`${method} ${url.pathname}`);
		const data = ANSWERS[url.pathname] ?? { success: false, error: 'not found' };
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
});

describe('server management files', () => {
	test('an admin browses and opens files, but nothing changes them', async () => {
		const user = userEvent.setup();
		render(FilesPanel);
		await screen.findByText('note.txt');

		expect(screen.queryByRole('button', { name: 'server_mgmt.files_mkdir' })).toBeNull();
		expect(screen.queryByText('server_mgmt.root_writable')).toBeNull();
		for (const menu of screen.queryAllByRole('button', { name: /server_mgmt\.files_actions_for/ })) {
			await user.click(menu);
			expect(screen.queryByRole('menuitem', { name: /server_mgmt\.files_rename/ })).toBeNull();
			expect(screen.queryByRole('menuitem', { name: /actions\.delete/ })).toBeNull();
			await user.keyboard('{Escape}');
		}

		await user.click(screen.getByText('note.txt'));
		const sheet = await screen.findByRole('dialog');
		const content = within(sheet).getByRole('textbox');
		expect(content).toHaveValue('hello\n');
		expect(content).toHaveAttribute('readonly');
		expect(within(sheet).queryByRole('button', { name: 'server_mgmt.file_save' })).toBeNull();

		expect(writes).toEqual([]);
	});
});
