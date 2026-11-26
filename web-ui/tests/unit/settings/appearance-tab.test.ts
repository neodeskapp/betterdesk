/**
 * Settings › Appearance on the settings pattern (PATTERN.md): one save
 * bar that names each change, Discard instead of Revert, a filter, a
 * picked background that waits for Save like any other change, examples in
 * the help instead of in the fields, and a danger zone that names what
 * Reset removes.
 */
import { cleanup, fireEvent, render, screen, within } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

const confirmed = vi.hoisted(() => ({ messages: [] as string[] }));
vi.mock('#lib/ui/dialogs.svelte.ts', async (orig) => ({
	...(await orig<typeof import('../../../src/lib/ui/dialogs.svelte.ts')>()),
	confirm: vi.fn(async (o: { message: string }) => {
		confirmed.messages.push(o.message);
		return false;
	})
}));
vi.mock('#lib/api/settings.ts', async (orig) => ({
	...(await orig<typeof import('../../../src/lib/api/settings.ts')>()),
	getBranding: vi.fn(async () => ({
		data: { appName: 'Desk', themeMode: 'system', bgType: 'none', bgImageUrl: '', customCss: '' },
		readability: null
	})),
	listBackgrounds: vi.fn(async () => []),
	listLocalFonts: vi.fn(async () => []),
	listProfiles: vi.fn(async () => []),
	listThemes: vi.fn(async () => []),
	saveBranding: vi.fn(async (body: Record<string, unknown>) => ({ data: body, readability: null })),
	uploadBackground: vi.fn(async () => ({ url: '/uploads/new.png' }))
}));

import * as settingsApi from '../../../src/lib/api/settings.ts';
import AppearanceTab from '../../../src/lib/settings/tabs/AppearanceTab.svelte';
import { session } from '../../../src/lib/state/session.svelte.ts';

beforeEach(() => {
	session.isSuperAdmin = true;
	confirmed.messages = [];
	vi.mocked(settingsApi.saveBranding).mockClear();
	vi.mocked(settingsApi.uploadBackground).mockClear();
});

afterEach(() => {
	cleanup();
	session.isSuperAdmin = false;
});

async function loaded() {
	render(AppearanceTab);
	await vi.waitFor(() => expect(document.querySelector('.branding-form[aria-busy="false"]')).not.toBeNull());
}

describe('Settings › Appearance', () => {
	test('a change shows in the save bar by name; Discard puts it back and nothing is sent', async () => {
		const user = userEvent.setup();
		await loaded();
		expect(screen.queryByRole('region', { name: 'Unsaved changes' })).toBeNull();
		await user.click(screen.getByRole('radio', { name: /theme\.dark/ }));
		const bar = screen.getByRole('region', { name: 'Unsaved changes' });
		expect(bar).toHaveTextContent('Default theme: theme.auto → theme.dark');
		await user.click(within(bar).getByRole('button', { name: 'Discard' }));
		expect(screen.getByRole('radio', { name: /theme\.auto/ })).toBeChecked();
		expect(settingsApi.saveBranding).not.toHaveBeenCalled();
	}, 20_000);

	test('Save sends the whole appearance with the change', async () => {
		const user = userEvent.setup();
		await loaded();
		await user.click(screen.getByRole('radio', { name: /theme\.dark/ }));
		await user.click(screen.getByRole('button', { name: 'Save changes' }));
		await vi.waitFor(() => expect(settingsApi.saveBranding).toHaveBeenCalledTimes(1));
		expect(vi.mocked(settingsApi.saveBranding).mock.calls[0]![0]).toMatchObject({
			themeMode: 'dark',
			appName: 'Desk'
		});
		await vi.waitFor(() => expect(screen.queryByRole('region', { name: 'Unsaved changes' })).toBeNull());
	}, 20_000);

	test('a picked background is uploaded, then waits for Save as an unsaved change', async () => {
		const user = userEvent.setup();
		await loaded();
		await user.click(screen.getByRole('radio', { name: 'branding.bg_type_image' }));
		const picker = screen.getByLabelText('branding.bg_image_upload') as HTMLInputElement;
		const file = new File(['x'], 'new.png', { type: 'image/png' });
		Object.defineProperty(picker, 'files', { value: [file], writable: true, configurable: true });
		await fireEvent.change(picker);
		await vi.waitFor(() => expect(settingsApi.uploadBackground).toHaveBeenCalled());
		// Stored, not applied: no target
		expect(vi.mocked(settingsApi.uploadBackground).mock.calls[0]![1]).toBe('');
		await vi.waitFor(() =>
			expect(screen.getByRole('region', { name: 'Unsaved changes' })).toHaveTextContent('/uploads/new.png')
		);
		expect(settingsApi.saveBranding).not.toHaveBeenCalled();
		await user.click(screen.getByRole('button', { name: 'Save changes' }));
		await vi.waitFor(() =>
			expect(vi.mocked(settingsApi.saveBranding).mock.calls[0]?.[0]).toMatchObject({
				bgType: 'image',
				bgImageUrl: '/uploads/new.png'
			})
		);
	}, 20_000);

	test('the filter narrows the rows and opens Advanced for custom CSS', async () => {
		const user = userEvent.setup();
		await loaded();
		const advanced = document.querySelector<HTMLDetailsElement>('#branding-css details')!;
		expect(advanced.open).toBe(false);
		await user.type(screen.getByRole('searchbox', { name: 'Filter appearance settings' }), 'custom css');
		await vi.waitFor(() => expect(advanced.open).toBe(true));
		expect(document.getElementById('font-heading-search')).not.toBeVisible();
	}, 20_000);

	test('fields hold no examples; sliders say their value', async () => {
		await loaded();
		const placeholders = [...document.querySelectorAll<HTMLInputElement>('.branding-form [placeholder]')]
			.filter((el) => el.getAttribute('role') !== 'combobox' && el.type !== 'search')
			.map((el) => `${el.id}: ${el.placeholder}`);
		expect(placeholders).toEqual([]);
		expect(screen.getByRole('slider', { name: 'branding.bg_blur', hidden: true })).toHaveAttribute(
			'aria-valuetext',
			'0 px'
		);
	}, 20_000);

	test('Reset is in the danger zone and its question names what goes and what stays', async () => {
		const user = userEvent.setup();
		await loaded();
		const zone = screen.getByRole('heading', { name: 'Danger zone' }).closest('.danger-zone') as HTMLElement;
		await user.click(within(zone).getByRole('button', { name: /settings\.appearance\.reset_button/ }));
		expect(confirmed.messages.at(-1)).toMatch(/custom CSS.*sign-in page.*Saved appearances.*stay/);
	}, 20_000);
});
