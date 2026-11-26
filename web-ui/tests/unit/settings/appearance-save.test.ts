/**
 * Appearance: Save pressed while a background picture uploads waits for
 * the upload and sends the picture it stored, not the address the form
 * had before.
 */
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

const upload = vi.hoisted(() => ({ finish: (_: unknown) => {} }));
vi.mock('#lib/api/settings.ts', async (orig) => ({
	...(await orig<typeof import('../../../src/lib/api/settings.ts')>()),
	getBranding: vi.fn(async () => ({
		data: { appName: 'Desk', bgType: 'none', bgImageUrl: '' },
		readability: null
	})),
	listBackgrounds: vi.fn(async () => []),
	listLocalFonts: vi.fn(async () => []),
	listProfiles: vi.fn(async () => []),
	listThemes: vi.fn(async () => []),
	saveBranding: vi.fn(async (body: Record<string, unknown>) => ({ data: body, readability: null })),
	uploadBackground: vi.fn(
		() =>
			new Promise((resolve) => {
				upload.finish = resolve;
			})
	)
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

describe('appearance', () => {
	test('a save while a background uploads waits and carries the uploaded background', async () => {
		const user = userEvent.setup();
		render(AppearanceTab);
		await vi.waitFor(() =>
			expect(document.querySelector('.branding-form[aria-busy="false"]')).not.toBeNull()
		);
		await user.click(screen.getByRole('radio', { name: 'branding.bg_type_image' }));
		const file = new File(['x'], 'new.png', { type: 'image/png' });
		const picker = document.getElementById('bg-image-file-control') as HTMLInputElement;
		// A writable files list: the picker sets it back after the upload, which jsdom's getter refuses
		Object.defineProperty(picker, 'files', { value: [file], writable: true, configurable: true });
		await fireEvent.change(picker);
		await vi.waitFor(() => expect(settingsApi.uploadBackground).toHaveBeenCalled());

		await user.click(screen.getByRole('button', { name: 'Save changes' }));
		expect(settingsApi.saveBranding).not.toHaveBeenCalled();
		upload.finish({ url: '/uploads/new.png' });
		await vi.waitFor(() => expect(settingsApi.saveBranding).toHaveBeenCalledTimes(1));
		expect(vi.mocked(settingsApi.saveBranding).mock.calls[0]![0]).toMatchObject({
			bgType: 'image',
			bgImageUrl: '/uploads/new.png'
		});
	}, 20_000);
});
