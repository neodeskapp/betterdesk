/**
 * Server › Configuration files in the one settings template (PATTERN.md
 * rules 7, 8 and 10): the page keeps the whole width, its group does not
 * repeat the page title, what Save file does is said once for the group,
 * and the restart is a separate action whose button has the name the
 * hint under the editor uses.
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';

const pageState = vi.hoisted(() => ({ url: new URL('http://localhost/server/config-files'), state: {} }));
vi.mock('$app/state', () => ({ page: pageState }));
vi.mock('$app/paths', () => ({ resolve: (p: string) => p || '/', asset: (p: string) => '/' + p }));
vi.mock('$app/navigation', () => ({
	goto: vi.fn(),
	beforeNavigate: vi.fn(),
	pushState: vi.fn(),
	replaceState: vi.fn()
}));
vi.mock('#lib/api/settings-system.ts', async (orig) => ({
	...(await orig<typeof import('../../../src/lib/api/settings-system.ts')>()),
	listConfigFiles: vi.fn(async () => [
		{
			id: 'console-env-local',
			path: '/opt/neodesk/.env.local',
			exists: true,
			writable: true,
			canCreate: true,
			requiresRestart: 'console',
			size: 12,
			mtime: '2026-11-20T10:00:00Z'
		}
	]),
	readConfigFile: vi.fn(async () => ({
		path: '/opt/neodesk/.env.local',
		content: 'A=1\n',
		size: 4,
		mtime: '2026-11-20T10:00:00Z',
		requiresRestart: 'console'
	}))
}));
vi.mock('#lib/api/restart.ts', () => ({ getPendingRestart: vi.fn(async () => null) }));

import Page from '../../../src/routes/(console)/server/config-files/+page.svelte';
import { session } from '../../../src/lib/state/session.svelte.ts';

beforeEach(() => {
	session.isSuperAdmin = true;
});

afterEach(() => {
	cleanup();
	session.isSuperAdmin = false;
});

test('the page uses the settings template and keeps the whole width', async () => {
	render(Page);
	expect(document.querySelector('[data-page="server-config-files"]')!.classList).toContain('is-wide');
	expect(screen.getByRole('heading', { level: 1 }).textContent).toBe('nav.server_config_files');
	expect(screen.queryByRole('heading', { level: 2, name: 'settings.advanced_title' })).toBeNull();
});

test('what Save file does and when it takes effect is said once for the group', async () => {
	render(Page);
	const group = await screen.findByRole('region', { name: 'Files on this server' });
	expect(group.textContent).toContain(
		'Save file writes the file at once and keeps a copy of the previous version. When the change takes effect depends on the file: the line under the editor says it.'
	); // "Pick a file" is said once, where the editor is still empty, not again on the group
	expect(group.textContent).not.toContain('Pick a file');
	expect(screen.getByText('settings.advanced_select_file')).toBeTruthy();
});

test('the restart is a separate action named as the hint under the editor names it', async () => {
	const user = userEvent.setup();
	render(Page);
	await user.click(await screen.findByText('console-env-local'));
	const restart = await screen.findByRole('button', { name: 'Restart services…' });
	expect(restart.closest('#advanced-restart-zone')).not.toBeNull();
	expect(
		screen.getByRole('button', { name: 'settings.advanced_save' }).closest('#advanced-restart-zone')
	).toBeNull();
});
