/**
 * Appearance fonts (BUGS L362, L363): a Google font is used only once it is
 * on the server, since the console loads fonts from itself alone; a failed
 * download says so at the field and keeps the font in use. Downloaded and
 * Upload custom list the fonts on the server, uploaded ones among them.
 */
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, test, vi } from 'vitest';

vi.mock('#lib/api/settings.ts', async (orig) => ({
	...(await orig<typeof import('../../../src/lib/api/settings.ts')>()),
	searchFonts: vi.fn(async () => [{ family: 'Lora', category: 'serif', downloaded: false }]),
	listLocalFonts: vi.fn(async () => [{ family: 'Audit Sans' }, { family: 'Inter' }]),
	downloadFont: vi.fn(async () => false)
}));

import * as settingsApi from '../../../src/lib/api/settings.ts';
import FontPicker from '../../../src/lib/settings/FontPicker.svelte';

afterEach(() => {
	cleanup();
	vi.mocked(settingsApi.downloadFont).mockClear();
	vi.mocked(settingsApi.searchFonts).mockClear();
});

describe('font picker', () => {
	test('a Google font that could not be downloaded is not used, and the field says why', async () => {
		render(FontPicker, { slot: 'heading', value: 'Inter', source: 'google', category: '', canEdit: true });
		await fireEvent.focus(screen.getByRole('combobox'));
		await fireEvent.click(await screen.findByRole('button', { name: /Lora/ }));
		await vi.waitFor(() => expect(settingsApi.downloadFont).toHaveBeenCalledWith('Lora'));
		await vi.waitFor(() =>
			expect(document.getElementById('font-heading-problem')?.textContent).toContain(
				'settings.appearance.font_download_failed'
			)
		);
		expect(document.getElementById('font-heading-current')?.textContent).toBe('Inter');
		expect(screen.getByRole('combobox').getAttribute('aria-invalid')).toBe('true');
	});

	test('Downloaded lists the fonts on the server, an uploaded one too', async () => {
		render(FontPicker, { slot: 'body', value: '', source: 'local', category: '', canEdit: true });
		const input = screen.getByRole('combobox');
		await fireEvent.input(input, { target: { value: 'audit' } });
		await fireEvent.focus(input);
		await fireEvent.click(await screen.findByRole('button', { name: /Audit Sans/ }));
		await vi.waitFor(() =>
			expect(document.getElementById('font-body-current')?.textContent).toBe('Audit Sans')
		);
		expect(settingsApi.searchFonts).not.toHaveBeenCalled();
		expect(settingsApi.downloadFont).not.toHaveBeenCalled();
	});
});
