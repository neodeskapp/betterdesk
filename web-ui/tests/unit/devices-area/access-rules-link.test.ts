/**
 * The Policies page sends people to the access rules for turning sound off
 * in sessions (a first-time admin found only the plain device list):
 * /devices#access-rules opens the folded section and brings it into view.
 */
import { cleanup, render } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

const pageState = vi.hoisted(() => ({ url: new URL('http://localhost/app/devices'), state: {} }));
vi.mock('$app/state', () => ({ page: pageState }));
vi.mock('$app/paths', () => ({ resolve: (p: string) => p || '/', asset: (p: string) => '/' + p }));
vi.mock('$app/navigation', () => ({ goto: vi.fn(), replaceState: vi.fn() }));

import AccessRules from '../../../src/routes/(console)/devices/AccessRules.svelte';

beforeEach(() => {
	vi.stubGlobal(
		'fetch',
		async () =>
			new Response(JSON.stringify({ success: true, data: [] }), {
				headers: { 'Content-Type': 'application/json' }
			})
	);
	Element.prototype.scrollIntoView = vi.fn();
});

afterEach(() => {
	cleanup();
	vi.unstubAllGlobals();
});

describe('a link to the access rules', () => {
	test('#access-rules opens the section and scrolls to it', async () => {
		pageState.url = new URL('http://localhost/app/devices#access-rules');
		render(AccessRules, { groups: [], devices: [] });
		const section = () => document.querySelector<HTMLDetailsElement>('details.access-rules');
		await vi.waitFor(() => expect(section()?.open).toBe(true));
		expect(document.getElementById('access-rules')?.contains(section() ?? null)).toBe(true);
		await vi.waitFor(() => expect(Element.prototype.scrollIntoView).toHaveBeenCalled());
	});

	test('without it the section stays folded', async () => {
		pageState.url = new URL('http://localhost/app/devices');
		render(AccessRules, { groups: [], devices: [] });
		await vi.waitFor(() => expect(document.querySelector('details.access-rules')).not.toBeNull());
		expect(document.querySelector<HTMLDetailsElement>('details.access-rules')?.open).toBe(false);
	});

	test('with no rule the section says so in three words, not the note again', async () => {
		vi.stubGlobal(
			'fetch',
			async () =>
				new Response(JSON.stringify({ success: true, data: { strategies: [] } }), {
					headers: { 'Content-Type': 'application/json' }
				})
		);
		pageState.url = new URL('http://localhost/app/devices#access-rules');
		render(AccessRules, { groups: [], devices: [] });
		await vi.waitFor(() =>
			expect(document.querySelector('.access-rules .state')?.textContent).toBe('No rules yet.')
		);
	});
});
