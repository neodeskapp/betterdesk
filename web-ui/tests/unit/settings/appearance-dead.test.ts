/**
 * Appearance: no control for a setting nothing reads (BUGS L360, L371).
 * Glass surfaces wrote style variables no console style reads, and the
 * application description showed only in the tab's own preview. The
 * form neither offers nor sends them.
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

vi.mock('#lib/api/settings.ts', async (orig) => ({
	...(await orig<typeof import('../../../src/lib/api/settings.ts')>()),
	getBranding: vi.fn(async () => ({
		data: {
			appName: 'Desk',
			appDescription: 'Old text',
			glassEnabled: 'true',
			glassColor: '#102030',
			bgType: 'none'
		},
		readability: null
	})),
	listBackgrounds: vi.fn(async () => []),
	listLocalFonts: vi.fn(async () => []),
	listProfiles: vi.fn(async () => []),
	listThemes: vi.fn(async () => []),
	saveBranding: vi.fn(async (body: Record<string, unknown>) => ({ data: body, readability: null }))
}));

import * as settingsApi from '../../../src/lib/api/settings.ts';
import AppearanceTab from '../../../src/lib/settings/tabs/AppearanceTab.svelte';
import { session } from '../../../src/lib/state/session.svelte.ts';

beforeEach(() => {
	session.isSuperAdmin = true;
	vi.mocked(settingsApi.saveBranding).mockClear();
});

afterEach(() => {
	cleanup();
	session.isSuperAdmin = false;
});

describe('appearance controls without an effect', () => {
	test('no glass surfaces and no application description, in the form or in the save', async () => {
		const user = userEvent.setup();
		render(AppearanceTab);
		const name = await screen.findByLabelText('branding.app_name');
		expect(document.getElementById('glass-enabled')).toBeNull();
		expect(document.getElementById('glass-color')).toBeNull();
		expect(document.getElementById('brand-description')).toBeNull();
		expect(screen.queryByText('Old text')).toBeNull();

		await user.clear(name);
		await user.type(name, 'Help desk');
		await user.click(screen.getByRole('button', { name: 'settings.appearance.save' }));
		await vi.waitFor(() => expect(settingsApi.saveBranding).toHaveBeenCalledTimes(1));
		const sent = vi.mocked(settingsApi.saveBranding).mock.calls[0]![0] as Record<string, unknown>;
		expect(sent.appName).toBe('Help desk');
		for (const key of ['glassEnabled', 'glassColor', 'glassBlur', 'glassOpacity', 'appDescription']) {
			expect(sent).not.toHaveProperty(key);
		}
	}, 20_000);
});
