import { cleanup, render, screen, within } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import A11yPanel from '../../../src/lib/shell/A11yPanel.svelte';
import {
	DEFAULTS,
	STORAGE_KEY,
	a11y,
	applyAccountA11y,
	leaveAccountA11y
} from '../../../src/lib/shell/a11y.svelte.ts';
import { writePref } from '../../../src/lib/util/storage.ts';

beforeEach(() => {
	localStorage.clear();
	writePref(STORAGE_KEY, null);
	leaveAccountA11y();
	a11y.settings = { ...DEFAULTS };
});
afterEach(() => {
	cleanup();
	vi.unstubAllGlobals();
});

test('a sheet titled Accessibility with two radio groups and four checkboxes', () => {
	render(A11yPanel, { open: true });
	const sheet = screen.getByRole('dialog', { name: 'accessibility.title' });
	expect(within(sheet).getByText('accessibility.subtitle_browser')).toBeInTheDocument();
	for (const name of ['accessibility.reduce_motion', 'accessibility.more_contrast']) {
		const group = within(sheet).getByRole('group', { name });
		const radios = within(group).getAllByRole('radio');
		expect(radios.map((r) => r.closest('label')?.textContent?.trim())).toEqual([
			'accessibility.follow_system',
			'accessibility.choice_on',
			'accessibility.choice_off'
		]);
		expect(within(group).getByRole('radio', { name: 'accessibility.follow_system' })).toBeChecked();
	}
	const boxes = within(sheet).getAllByRole('checkbox');
	expect(boxes.map((b) => b.closest('label')?.querySelector('.check-label')?.textContent)).toEqual([
		'accessibility.underline_links',
		'accessibility.readable_font',
		'accessibility.single_key_shortcuts',
		'accessibility.pause_live_updates'
	]);
	for (const b of boxes) expect(b).not.toBeChecked();
	// Nothing of the old panel is left
	expect(within(sheet).queryByRole('slider')).toBeNull();
	expect(within(sheet).queryByRole('combobox')).toBeNull();
	expect(within(sheet).queryByText(/profile/i)).toBeNull();
});

test('a choice applies and is kept at once', async () => {
	render(A11yPanel, { open: true });
	const motion = screen.getByRole('group', { name: 'accessibility.reduce_motion' });
	await userEvent.click(within(motion).getByRole('radio', { name: 'accessibility.choice_on' }));
	expect(a11y.settings.reduceMotion).toBe('on');
	expect(document.documentElement.dataset.a11yReduceMotion).toBe('true');
	await userEvent.click(screen.getByRole('checkbox', { name: 'accessibility.underline_links' }));
	await vi.waitFor(() =>
		expect(JSON.parse(localStorage.getItem(STORAGE_KEY)!)).toMatchObject({
			reduceMotion: 'on',
			underlineLinks: true
		})
	);
});

test('signed in, the sheet says so; a refused save puts the control back', async () => {
	applyAccountA11y(null);
	vi.stubGlobal(
		'fetch',
		vi.fn(async () => Response.json({ success: false, error: 'no' }, { status: 500 }))
	);
	render(A11yPanel, { open: true });
	expect(screen.getByText('accessibility.subtitle_account')).toBeInTheDocument();
	const contrast = screen.getByRole('group', { name: 'accessibility.more_contrast' });
	await userEvent.click(within(contrast).getByRole('radio', { name: 'accessibility.choice_off' }));
	await vi.waitFor(() =>
		expect(within(contrast).getByRole('radio', { name: 'accessibility.follow_system' })).toBeChecked()
	);
	expect(a11y.settings.moreContrast).toBe('system');
	const box = screen.getByRole('checkbox', { name: 'accessibility.pause_live_updates' });
	await userEvent.click(box);
	await vi.waitFor(() => expect(box).not.toBeChecked());
});
