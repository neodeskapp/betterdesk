/**
 * Accessible authentication (WCAG 3.3.8): a two-factor code can be pasted
 * as authenticator apps and password managers show it, with a space or a
 * dash in the middle, and it is not cut short.
 */
import { cleanup, render, screen, waitFor } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import type { LoginPage } from '../../../src/lib/api/signin.ts';
import { readLoginQuery } from '../../../src/lib/public/login.ts';

vi.mock('$app/paths', () => ({ resolve: (p: string) => '/app' + (p || '/'), asset: (p: string) => '/' + p }));

const LoginView = (await import('../../../src/lib/public/LoginView.svelte')).default;

function page(over: Partial<LoginPage> = {}, branding: Partial<LoginPage['branding']> = {}): LoginPage {
	return {
		success: true,
		csrfToken: 'tok',
		appName: 'NeoDesk',
		appVersion: '3.5.0',
		desktopMode: false,
		signedIn: false,
		branding: {
			logoType: 'default',
			logoSvg: '',
			logoUrl: '',
			logoText: '',
			logoTextAccent: '',
			logoIcon: '',
			loginTitle: '',
			loginSubtitle: '',
			loginPromoMode: 'classic',
			loginSlides: [],
			footerText: '',
			showPoweredBy: true,
			...branding
		},
		languages: [
			{ code: 'en', name: 'English', native: 'English', flag: '', rtl: false },
			{ code: 'de', name: 'German', native: 'Deutsch', flag: '', rtl: false }
		],
		attestation: null,
		...over
	};
}

type Reply = { status?: number; body: unknown };
let replies: Record<string, Reply[]>;
let sent: { url: string; body: Record<string, unknown> | null }[];

function reply(path: string, ...r: Reply[]) {
	replies[path] = [...(replies[path] ?? []), ...r];
}

beforeEach(() => {
	replies = {};
	sent = [];
	vi.stubGlobal(
		'fetch',
		vi.fn(async (input: string, init?: RequestInit) => {
			const url = new URL(input, 'http://localhost');
			sent.push({ url: url.pathname + url.search, body: init?.body ? JSON.parse(String(init.body)) : null });
			const next = replies[url.pathname]?.shift() ?? { status: 404, body: { success: false } };
			return new Response(JSON.stringify(next.body), {
				status: next.status ?? 200,
				headers: { 'Content-Type': 'application/json' }
			});
		})
	);
});

afterEach(() => {
	cleanup();
	vi.unstubAllGlobals();
	document.body.className = '';
});

const posted = (path: string) => sent.filter((s) => s.url.startsWith(path)).map((s) => s.body);

describe('pasting a two-factor code', () => {
	for (const pasted of ['123 456', '123-456', ' 123456 ']) {
		test(`"${pasted}" sends all six digits`, async () => {
			reply('/api/auth/oidc/status', { body: { enabled: false } });
			reply('/api/auth/login', { body: { success: true, totpRequired: true } });
			reply('/api/auth/totp/verify', { body: { success: true, redirect: '/devices' } });
			const go = vi.fn();
			render(LoginView, { page: page(), query: readLoginQuery(''), go });
			const user = userEvent.setup();
			await user.type(screen.getByLabelText('auth.username'), 'demo');
			await user.type(screen.getByLabelText('auth.password'), 'secret{Enter}');
			const code = await screen.findByLabelText('auth.totp_code');
			await waitFor(() => expect(code).toHaveFocus());
			await user.paste(pasted);
			await waitFor(() => expect(posted('/api/auth/totp/verify')[0]).toMatchObject({ code: '123456' }));
		});
	}
});
