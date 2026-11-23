/**
 * The font picker on Settings › Appearance with the keyboard (BUGS L326):
 * the fonts are reached with the arrow keys, not one Tab stop each, and
 * the list closes when focus leaves the picker, so it never covers the
 * control that has focus.
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, test, vi } from 'vitest';

vi.mock('#lib/api/settings.ts', async (orig) => ({
	...(await orig<typeof import('../../../src/lib/api/settings.ts')>()),
	searchFonts: vi.fn(async () =>
		Array.from({ length: 60 }, (_, i) => ({
			family: `Font ${i + 1}`,
			category: 'sans-serif',
			downloaded: false
		}))
	),
	downloadFont: vi.fn(async () => true)
}));

import FontPicker from '../../../src/lib/settings/FontPicker.svelte';

afterEach(cleanup);

describe('font picker with the keyboard', () => {
	test('Tab goes past the list, and the list closes when focus leaves', async () => {
		const user = userEvent.setup();
		const after = document.createElement('button');
		after.textContent = 'Next control';
		render(FontPicker, { slot: 'heading', value: '', source: 'google', category: '', canEdit: true });
		document.body.appendChild(after);

		await user.tab();
		const field = screen.getByRole('combobox');
		expect(field).toHaveFocus();
		const list = document.getElementById(field.getAttribute('aria-controls')!)!;
		await vi.waitFor(() => expect(list.hidden).toBe(false));
		const fonts = list.querySelectorAll('button');
		expect(fonts.length).toBe(60);
		expect([...fonts].filter((b) => b.tabIndex !== -1).length, 'fonts that are Tab stops').toBe(0);

		// Arrow down still moves into the list
		await user.keyboard('{ArrowDown}');
		expect(fonts[0]).toHaveFocus();

		await user.tab();
		expect(after).toHaveFocus();
		expect(list.hidden, 'the list closed when focus left').toBe(true);
		expect(field.getAttribute('aria-expanded')).toBe('false');
		after.remove();
	});
});
