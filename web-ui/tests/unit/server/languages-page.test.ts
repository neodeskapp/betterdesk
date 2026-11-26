/**
 * Settings › Translations in the one page template (PATTERN.md rules 5
 * and 10): it says that it only shows the files, where to change the
 * language you see (Your account › Preferences, with a link), and what
 * the statuses mean.
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';

const pageState = vi.hoisted(() => ({ url: new URL('http://localhost/languages'), state: {} }));
vi.mock('$app/state', () => ({ page: pageState }));
vi.mock('$app/paths', () => ({ resolve: (p: string) => p || '/', asset: (p: string) => '/' + p, base: '' }));
vi.mock('$app/navigation', () => ({
	goto: vi.fn(),
	beforeNavigate: vi.fn(),
	pushState: vi.fn(),
	replaceState: vi.fn()
}));

import Page from '../../../src/routes/(console)/languages/+page.svelte';
import { session } from '../../../src/lib/state/session.svelte.ts';

beforeEach(() => {
	session.isSuperAdmin = true;
	vi.stubGlobal('fetch', async (input: string) => {
		const url = new URL(input, location.origin);
		const data = {
			success: true,
			languages: [
				{ code: 'en', name: 'English', native: 'English', coverage: 100, is_reference: true },
				{ code: 'de', name: 'German', native: 'Deutsch', coverage: 100, needs_review: true, missing_keys: 0 }
			],
			refKeyCount: 10
		};
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
	session.isSuperAdmin = false;
});

test('the page uses the settings template at full width', () => {
	render(Page);
	expect(document.querySelector('[data-page="languages"]')!.classList).toContain('is-wide');
});

test('it says it only shows the files and where to change your own language', () => {
	render(Page);
	expect(
		screen.getByText(
			/This page only shows the language files; texts are changed in the files on the server\./
		)
	).toBeTruthy();
	expect(screen.getByRole('link', { name: 'Your account › Preferences' }).getAttribute('href')).toBe(
		'/account#preferences-language'
	);
});

test('it says what the statuses mean', () => {
	render(Page);
	expect(
		screen.getByText(
			'Reference language: the files the others are compared with. Needs review: the file is marked as not yet checked by a person.'
		)
	).toBeTruthy();
});
