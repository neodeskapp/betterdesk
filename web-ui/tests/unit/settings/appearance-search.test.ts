/**
 * The settings search finds the Appearance fields it lists: every id its
 * index names for the tab is on the page (the colour fields were listed as
 * "<prefix>-color-picker", which no element has).
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import { SEARCH_INDEX } from '../../../src/lib/settings/search-index.ts';

vi.mock('#lib/api/settings.ts', async (orig) => ({
	...(await orig<typeof import('../../../src/lib/api/settings.ts')>()),
	getBranding: vi.fn(async () => ({ data: { appName: 'Desk', customCss: '.a {}' }, readability: null })),
	listBackgrounds: vi.fn(async () => []),
	listLocalFonts: vi.fn(async () => []),
	listProfiles: vi.fn(async () => []),
	listThemes: vi.fn(async () => [])
}));

import AppearanceTab from '../../../src/lib/settings/tabs/AppearanceTab.svelte';
import { session } from '../../../src/lib/state/session.svelte.ts';

beforeEach(() => {
	session.isSuperAdmin = true;
});

afterEach(() => {
	cleanup();
	session.isSuperAdmin = false;
});

test('every Appearance entry of the settings search is on the page', async () => {
	render(AppearanceTab);
	await vi.waitFor(() => expect(document.querySelector('.branding-form[aria-busy="false"]')).not.toBeNull());
	const missing = SEARCH_INDEX.filter((row) => row[0] === 'branding')
		.map((row) => row[1])
		// the upload fields show once "Upload custom" is picked as the font source
		.filter((id) => id !== 'font-upload-name' && id !== 'font-upload-file')
		.filter((id) => !document.getElementById(id));
	expect(missing).toEqual([]);
	// Name and logo and the pages others see are on Branding, not here
	expect(document.getElementById('brand-name')).toBeNull();
	expect(document.getElementById('branding-login')).toBeNull();
}, 20_000);

test('every Branding entry of the settings search is on the Branding part', async () => {
	render(AppearanceTab, { part: 'brand' });
	await screen.findByLabelText('branding.app_name');
	const missing = SEARCH_INDEX.filter((row) => row[0] === 'brand')
		.map((row) => row[1])
		.filter((id) => !document.getElementById(id));
	expect(missing).toEqual([]);
	// Colours, fonts and saved appearances are on Appearance
	for (const id of ['branding-theme', 'branding-fonts', 'branding-css', 'branding-profiles'])
		expect(document.getElementById(id), id).toBeNull();
}, 20_000);
