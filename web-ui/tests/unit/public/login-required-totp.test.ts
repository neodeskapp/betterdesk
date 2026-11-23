import { cleanup, render, screen } from '@testing-library/svelte';
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

describe('mandatory two-factor sign-in (A153)', () => {
	test('an account without it sets it up after the password, sees its recovery codes once, then goes on', async () => {
		reply('/api/auth/oidc/status', { body: { enabled: false } });
		reply('/api/auth/login', { body: { success: true, totpSetupRequired: true } });
		reply('/api/auth/totp/required/setup', {
			body: {
				success: true,
				qrCode: 'data:image/png;base64,AAAA',
				secret: 'GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ'
			}
		});
		reply('/api/auth/totp/required/enable', {
			status: 401,
			body: { success: false, error: 'Invalid code.' }
		});
		reply('/api/auth/totp/required/enable', {
			body: { success: true, redirect: '/tickets', recoveryCodes: ['AAAA-BBBB', 'CCCC-DDDD'] }
		});
		const go = vi.fn();
		render(LoginView, { page: page(), query: readLoginQuery('?return=%2Ftickets'), go });
		await userEvent.type(screen.getByLabelText('auth.username'), 'demo');
		await userEvent.type(screen.getByLabelText('auth.password'), 'secret{Enter}');

		expect(
			await screen.findByRole('heading', { name: 'auth.totp_setup_required_title' })
		).toBeInTheDocument();
		expect(await screen.findByRole('img', { name: 'settings.signin.totp_qr_alt' })).toHaveAttribute(
			'src',
			'data:image/png;base64,AAAA'
		);
		expect(screen.getByText('GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ')).toBeInTheDocument();
		await userEvent.type(screen.getByLabelText('auth.totp_code'), '111111');
		expect(await screen.findByRole('alert')).toHaveTextContent('Invalid code.');
		await userEvent.type(screen.getByLabelText('auth.totp_code'), '222222');
		expect(posted('/api/auth/totp/required/enable')).toEqual([
			{ code: '111111', return: '/tickets' },
			{ code: '222222', return: '/tickets' }
		]);

		expect(await screen.findByText(/AAAA-BBBB/)).toBeInTheDocument();
		expect(screen.getByText(/CCCC-DDDD/)).toBeInTheDocument();
		expect(go).not.toHaveBeenCalled();
		await userEvent.click(screen.getByRole('button', { name: 'settings.signin.recovery_saved' }));
		expect(go).toHaveBeenCalledWith('/tickets');
	}, 30_000);
});
