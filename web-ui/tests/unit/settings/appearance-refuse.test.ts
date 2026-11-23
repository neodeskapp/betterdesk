/**
 * Appearance: a value the server would clean or drop is refused at its
 * field with the reason, and nothing is saved (BUGS L366, L367, L368,
 * L370). The same refusal from the server lands on its field too.
 */
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { ApiError } from '../../../src/lib/api/client.ts';
import {
	LOGO_ICONS,
	appearanceProblems,
	brandingFromServer,
	type BrandingValues
} from '../../../src/lib/settings/appearance.ts';

const stored = vi.hoisted(() => ({
	data: {
		appName: 'Desk',
		themeMode: 'custom',
		bgType: 'color',
		bgColor: '#102030',
		loginPromoMode: 'slides',
		loginSlides: [
			{ id: 'a', imageUrl: '/img/login-slides/free.jpg', title: 'One' },
			{ id: 'b', imageUrl: '/img/login-slides/remote.jpg', title: 'Two' }
		]
	} as Record<string, unknown>
}));

vi.mock('#lib/api/settings.ts', async (orig) => ({
	...(await orig<typeof import('../../../src/lib/api/settings.ts')>()),
	getBranding: vi.fn(async () => ({ data: stored.data, readability: null })),
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

const base = (over: Partial<BrandingValues> = {}): BrandingValues => ({
	...brandingFromServer(stored.data),
	...over
});

describe('appearanceProblems', () => {
	test('colours, gradients, addresses, slides and logos', () => {
		const saved = base();
		const v = base({
			bgColor: 'red;}',
			bgGradient: 'url(javascript:alert(1))',
			loginPromoMode: 'classic',
			loginBgType: 'image',
			loginBgImageUrl: 'https://example.com/a.jpg',
			agentBgType: 'image',
			agentBgImageUrl: 'javascript:alert(1)',
			colors: { ...saved.colors, accentBlue: '#12345g', bgPrimary: '' },
			logoType: 'icon',
			logoIcon: 'rocket_launch'
		});
		v.loginSlides = [{ ...v.loginSlides[0]!, imageUrl: 'javascript:alert(1)' }];
		expect(appearanceProblems(v, saved, { pendingLogo: false })).toEqual({
			bgColor: 'settings.appearance.refused_color',
			loginBgImageUrl: 'settings.appearance.refused_outside_address',
			agentBgImageUrl: 'settings.appearance.refused_address',
			'colors.accentBlue': 'settings.appearance.refused_color',
			logoIcon: 'settings.appearance.refused_icon'
		});
		// the slides count only in the slideshow, and the sign-in background only outside it
		v.loginPromoMode = 'slides';
		v.loginSlides = [
			{ ...v.loginSlides[0]!, imageUrl: '/ok.png' },
			{ ...v.loginSlides[0]!, id: 'b', imageUrl: '//host/a.png' }
		];
		expect(appearanceProblems(v, saved, { pendingLogo: false })).toMatchObject({
			'loginSlides.1.imageUrl': 'settings.appearance.refused_address'
		});
		expect(appearanceProblems(v, saved, { pendingLogo: false })).not.toHaveProperty('loginBgImageUrl');
	});

	test('hidden kinds, the custom colours outside Custom, and stored values pass', () => {
		const saved = base({ bgGradient: 'javascriptalert(1))' });
		const v = base({
			themeMode: 'dark',
			colors: { ...saved.colors, accentBlue: '#12345g' },
			bgGradient: 'javascriptalert(1))',
			loginBgColor: 'nope'
		});
		expect(appearanceProblems(v, saved, { pendingLogo: false })).toEqual({});
	});

	test('a logo kind without its content', () => {
		const saved = base();
		expect(
			appearanceProblems(base({ logoType: 'text', logoText: ' ' }), saved, { pendingLogo: false })
		).toEqual({
			logoText: 'settings.appearance.refused_logo_text'
		});
		expect(appearanceProblems(base({ logoType: 'svg', logoSvg: '' }), saved, { pendingLogo: false })).toEqual(
			{
				logoSvg: 'settings.appearance.refused_logo_svg'
			}
		);
		expect(
			appearanceProblems(base({ logoType: 'image', logoUrl: '' }), saved, { pendingLogo: false })
		).toEqual({
			logoUrl: 'settings.appearance.refused_logo_image'
		});
		expect(
			appearanceProblems(base({ logoType: 'image', logoUrl: '' }), saved, { pendingLogo: true })
		).toEqual({});
		expect(LOGO_ICONS).toContain('dns');
		expect(
			appearanceProblems(base({ logoType: 'icon', logoIcon: 'cloud' }), saved, { pendingLogo: false })
		).toEqual({});
	});
});

describe('the Appearance form', () => {
	test('a colour the server would clean is refused at its field, nothing is saved', async () => {
		const user = userEvent.setup();
		render(AppearanceTab);
		await screen.findByLabelText('branding.app_name');
		const color = document.getElementById('bg-color') as HTMLInputElement;
		await fireEvent.input(color, { target: { value: 'red;}' } });
		await user.click(screen.getByRole('button', { name: 'settings.appearance.save' }));
		await vi.waitFor(() =>
			expect(document.getElementById('bg-color-error')?.textContent).toBe('settings.appearance.refused_color')
		);
		expect(color.getAttribute('aria-invalid')).toBe('true');
		expect(settingsApi.saveBranding).not.toHaveBeenCalled();
		expect(screen.getByText('settings.appearance.refused_summary')).toBeTruthy();
	}, 20_000);

	test('a slide with a refused address is marked, not dropped', async () => {
		const user = userEvent.setup();
		render(AppearanceTab);
		await screen.findByLabelText('branding.app_name');
		const url = document.getElementById('login-slide-1-url') as HTMLInputElement;
		await fireEvent.input(url, { target: { value: 'javascript:alert(1)' } });
		await user.click(screen.getByRole('button', { name: 'settings.appearance.save' }));
		await vi.waitFor(() =>
			expect(document.getElementById('login-slide-1-url-error')?.textContent).toBe(
				'settings.appearance.refused_address'
			)
		);
		expect(settingsApi.saveBranding).not.toHaveBeenCalled();
		expect(document.getElementById('login-slide-1-url')).toBeTruthy();
	}, 20_000);

	test("the server's refusal lands on its field", async () => {
		vi.mocked(settingsApi.saveBranding).mockRejectedValueOnce(
			new ApiError('Nope', 400, {
				data: {
					success: false,
					error: 'Nope',
					field: 'customCss',
					fields: [{ field: 'customCss', message: 'Nope' }]
				}
			})
		);
		const user = userEvent.setup();
		render(AppearanceTab);
		const name = await screen.findByLabelText('branding.app_name');
		await user.type(name, 'x');
		await user.click(screen.getByRole('button', { name: 'settings.appearance.save' }));
		await vi.waitFor(() => expect(document.getElementById('custom-css-error')?.textContent).toBe('Nope'));
		expect(screen.getByText('settings.appearance.refused_summary')).toBeTruthy();
	}, 20_000);
});
