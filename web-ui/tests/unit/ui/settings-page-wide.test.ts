/**
 * SettingsPage can use the whole width (the configuration file editor),
 * as SettingsPageFrame's `wide` does.
 */
import { cleanup, render } from '@testing-library/svelte';
import { createRawSnippet } from 'svelte';
import { afterEach, expect, test, vi } from 'vitest';

vi.mock('$app/state', () => ({ page: { url: new URL('http://localhost/x'), state: {} } }));
vi.mock('$app/navigation', () => ({ goto: vi.fn(), beforeNavigate: vi.fn() }));

import SettingsPage from '../../../src/lib/ui/settings/SettingsPage.svelte';

afterEach(cleanup);

const children = createRawSnippet(() => ({ render: () => '<p>Body</p>' }));

test('wide passes on to the frame', () => {
	render(SettingsPage, { id: 'w', title: 'T', description: 'D', allowed: true, wide: true, children });
	expect(document.querySelector('[data-page="w"]')!.classList).toContain('is-wide');
});

test('without wide the page keeps the settings measure', () => {
	render(SettingsPage, { id: 'n', title: 'T', description: 'D', allowed: true, children });
	expect(document.querySelector('[data-page="n"]')!.classList).not.toContain('is-wide');
});
