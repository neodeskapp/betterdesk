/**
 * Your account uses the one settings template (PATTERN.md rule 10): links
 * to its parts under the title, and no page-wide save line, because the
 * page holds actions (password, two-factor), records (devices, tokens)
 * and the one group that applies at once (Preferences), which says so
 * itself.
 */
import { cleanup, render, screen, within } from '@testing-library/svelte';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';

const pageState = vi.hoisted(() => ({ url: new URL('http://localhost/account'), state: {} }));
vi.mock('$app/state', () => ({ page: pageState }));
vi.mock('$app/paths', () => ({ resolve: (p: string) => p || '/', asset: (p: string) => '/' + p }));
vi.mock('$app/navigation', () => ({
	goto: vi.fn(),
	beforeNavigate: vi.fn(),
	pushState: vi.fn(),
	replaceState: vi.fn()
}));

import Page from '../../../src/routes/(console)/account/+page.svelte';
import { session, type User } from '../../../src/lib/state/session.svelte.ts';

beforeEach(() => {
	session.user = { id: 7, username: 'hank', role: 'viewer' } as User;
	vi.stubGlobal('fetch', async (input: string) => {
		const url = new URL(input, location.origin);
		const data =
			url.pathname === '/api/auth/totp/status'
				? { success: true, enabled: false, disable_with: 'password' }
				: { success: true, data: [] };
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
	vi.unstubAllGlobals();
	session.user = null;
});

test('the page links to each of its parts', async () => {
	render(Page);
	const nav = screen.getByRole('navigation', { name: 'Sections on this page' });
	const hrefs = within(nav)
		.getAllByRole('link')
		.map((a) => a.getAttribute('href'));
	expect(hrefs).toEqual(['#profile', '#password', '#two-factor', '#sessions', '#preferences']);
	for (const h of hrefs) expect(document.getElementById(h!.slice(1)), h!).not.toBeNull();
});

test('no save line for the whole page: only Preferences applies at once and says so', async () => {
	render(Page);
	expect(document.querySelector('.save-model')).toBeNull();
	expect(screen.queryByText(/Changes are saved when you press Save/)).toBeNull();
	expect(document.getElementById('preferences')!.textContent).toContain('Changes here apply at once');
});

test('an account that may make tokens gets the API tokens link too', async () => {
	session.user = { id: 1, username: 'root', role: 'super_admin' } as User;
	render(Page);
	const nav = screen.getByRole('navigation', { name: 'Sections on this page' });
	expect(within(nav).getByRole('link', { name: 'api_tokens.title' }).getAttribute('href')).toBe(
		'#api-tokens'
	);
});
