/**
 * The logo and the attribution on the sign-in page and in the sidebar
 * draw what the Appearance settings say (BUGS L369, L370, L372): the
 * second line of a text logo under the first, the icon logo with its
 * icon, and "Powered by NeoDesk" with the version where the console's own
 * name was shown.
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import type { LoginPage } from '../../../src/lib/api/signin.ts';
import { readLoginQuery } from '../../../src/lib/public/login.ts';

vi.mock('$app/paths', () => ({ resolve: (p: string) => '/app' + (p || '/'), asset: (p: string) => '/' + p }));

const LoginView = (await import('../../../src/lib/public/LoginView.svelte')).default;
const LoginBrand = (await import('../../../src/lib/public/LoginBrand.svelte')).default;
const Sidebar = (await import('../../../src/lib/shell/Sidebar.svelte')).default;

type Branding = LoginPage['branding'];

function branding(over: Partial<Branding> = {}): Branding {
	return {
		logoType: 'default',
		logoIcon: '',
		logoSvg: '',
		logoUrl: '',
		logoText: '',
		logoTextAccent: '',
		loginTitle: '',
		loginSubtitle: '',
		loginPromoMode: 'classic',
		loginSlides: [],
		footerText: '',
		showPoweredBy: true,
		...over
	};
}

function page(over: Partial<Branding> = {}): LoginPage {
	return {
		success: true,
		csrfToken: 'tok',
		appName: 'Acme Remote',
		appVersion: '3.6.3',
		desktopMode: false,
		signedIn: false,
		branding: branding(over),
		languages: [{ code: 'en', name: 'English', native: 'English', flag: '', rtl: false }],
		attestation: null
	};
}

beforeEach(() => {
	vi.stubGlobal(
		'fetch',
		vi.fn(
			async () =>
				new Response(JSON.stringify({ enabled: false }), { headers: { 'Content-Type': 'application/json' } })
		)
	);
});

afterEach(() => {
	cleanup();
	vi.unstubAllGlobals();
	document.body.className = '';
});

describe('logo', () => {
	test('the sign-in page shows the icon logo with its icon', () => {
		render(LoginBrand, {
			branding: branding({ logoType: 'icon', logoIcon: 'cloud' }),
			appName: 'Acme Remote'
		});
		const brand = document.querySelector('.login-brand')!;
		expect(brand.querySelector('.material-icons')?.textContent).toBe('cloud');
		expect(brand.textContent).toContain('Acme Remote');
	});

	test('the second line of a text logo is a line of its own, on the sign-in page and in the sidebar', () => {
		render(LoginBrand, {
			branding: branding({ logoType: 'text', logoText: 'Example', logoTextAccent: 'Core Server' }),
			appName: 'x'
		});
		const login = document.querySelector('.brand-text-logo')!;
		expect([...login.children].map((c) => c.textContent)).toEqual(['Example', 'Core Server']);
		expect(login.classList.contains('has-second-line')).toBe(true);
		cleanup();

		render(Sidebar, {
			id: 'sb',
			model: { sections: [], activeSection: null, activePage: null },
			drawer: false,
			navigation: 0,
			branding: {
				logoType: 'text',
				logoText: 'Example',
				logoTextAccent: 'Core Server',
				logoIcon: '',
				logoSvg: '',
				logoUrl: '',
				footerText: '',
				showPoweredBy: false
			}
		});
		const side = document.querySelector('.logo-text')!;
		expect([...side.children].map((c) => c.textContent)).toEqual(['Example', 'Core Server']);
		expect(side.classList.contains('has-second-line')).toBe(true);
	});
});

describe('attribution on the sign-in page', () => {
	test('names NeoDesk and its version, not the console', () => {
		render(LoginView, { page: page(), query: readLoginQuery(''), go: vi.fn() });
		const footer = document.querySelector('.login-version')!;
		expect(footer.textContent).toContain('NeoDesk');
		expect(footer.textContent).toContain('3.6.3');
		expect(footer.textContent).not.toContain('Acme Remote');
	});

	test('switched off, the footer shows no attribution', () => {
		render(LoginView, { page: page({ showPoweredBy: false }), query: readLoginQuery(''), go: vi.fn() });
		expect(document.querySelector('.login-version')).toBeNull();
		expect(screen.queryByText(/NeoDesk/)).toBeNull();
	});
});
