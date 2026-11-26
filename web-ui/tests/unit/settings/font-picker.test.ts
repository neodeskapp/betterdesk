/**
 * The font picker on Settings › Appearance with the keyboard (BUGS L326):
 * the fonts are reached with the arrow keys, not one Tab stop each, and
 * the list closes when focus leaves the picker, so it never covers the
 * control that has focus. It is an APG combobox: the field keeps focus,
 * the fonts are options of a listbox, aria-activedescendant points at the
 * one the arrows reached, and Enter picks it.
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
		expect(list).toHaveAttribute('role', 'listbox');
		await vi.waitFor(() => expect(list.closest('[hidden]')).toBeNull());
		const fonts = screen.getAllByRole('option');
		expect(fonts.length).toBe(60);
		expect([...fonts].filter((b) => b.tabIndex >= 0).length, 'fonts that are Tab stops').toBe(0);

		// Arrow down moves the active option; focus stays in the field
		await user.keyboard('{ArrowDown}');
		expect(field).toHaveFocus();
		expect(field).toHaveAttribute('aria-activedescendant', fonts[0]!.id);
		expect(fonts[0]).toHaveAttribute('aria-selected', 'true');
		await user.keyboard('{ArrowDown}{ArrowDown}{ArrowUp}');
		expect(field).toHaveAttribute('aria-activedescendant', fonts[1]!.id);

		await user.tab();
		expect(after).toHaveFocus();
		expect(list.closest('[hidden]'), 'the list closed when focus left').not.toBeNull();
		expect(field.getAttribute('aria-expanded')).toBe('false');
		after.remove();
	});

	test('Enter picks the active font; Escape closes the list', async () => {
		const user = userEvent.setup();
		render(FontPicker, { slot: 'body', value: '', source: 'google', category: '', canEdit: true });
		await user.tab();
		const field = screen.getByRole('combobox');
		await vi.waitFor(() => expect(screen.getAllByRole('option')).toHaveLength(60));
		await user.keyboard('{ArrowDown}{ArrowDown}{Enter}');
		await vi.waitFor(() => expect(document.getElementById('font-body-current')?.textContent).toBe('Font 2'));
		expect(field).toHaveFocus();
		expect(field).toHaveAttribute('aria-expanded', 'false');
		expect(field).not.toHaveAttribute('aria-activedescendant');
		await user.keyboard('{ArrowDown}');
		await vi.waitFor(() => expect(field).toHaveAttribute('aria-expanded', 'true'));
		await user.keyboard('{Escape}');
		expect(field).toHaveAttribute('aria-expanded', 'false');
	});
});

describe('font picker in a setting row', () => {
	test('bare: the row names and describes it; the picker draws no label of its own', () => {
		const help = document.createElement('p');
		help.id = 'row-help';
		help.textContent = 'Used for headings.';
		document.body.appendChild(help);
		render(FontPicker, {
			slot: 'heading',
			value: '',
			source: 'google',
			category: '',
			canEdit: true,
			bare: true,
			inputId: 'row-control',
			describedby: 'row-help'
		});
		const field = screen.getByRole('combobox');
		expect(field.id).toBe('row-control');
		expect(field).toHaveAccessibleDescription('Used for headings.');
		expect(document.querySelector('label')).toBeNull();
		expect(screen.queryByText('branding.font_heading_hint')).toBeNull();
		help.remove();
	});
});
