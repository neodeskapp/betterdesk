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

describe('a new password at sign-in, as an admin asked', () => {
	test('after the password the person sets a new one, then the sign-in goes on', async () => {
		reply('/api/auth/oidc/status', { body: { enabled: false } });
		reply('/api/auth/login', { body: { success: true, passwordChangeRequired: true } });
		reply('/api/auth/password/required', { body: { success: true, redirect: '/tickets' } });
		const go = vi.fn();
		render(LoginView, { page: page(), query: readLoginQuery('?return=%2Ftickets'), go });
		await userEvent.type(screen.getByLabelText('auth.username'), 'demo');
		await userEvent.type(screen.getByLabelText('auth.password'), 'Temp-Pass-1{Enter}');

		expect(await screen.findByRole('heading', { name: 'auth.change_required_title' })).toBeInTheDocument();
		await userEvent.type(screen.getByLabelText('users.new_password'), 'Chosen-Pass-1!');
		await userEvent.type(screen.getByLabelText('password_link.repeat_password'), 'Chosen-Pass-2!');
		await userEvent.click(screen.getByRole('button', { name: 'auth.change_required_save' }));
		expect(await screen.findByText('password_link.passwords_differ')).toBeInTheDocument();
		expect(posted('/api/auth/password/required')).toEqual([]);

		await userEvent.clear(screen.getByLabelText('password_link.repeat_password'));
		await userEvent.type(screen.getByLabelText('password_link.repeat_password'), 'Chosen-Pass-1!');
		await userEvent.click(screen.getByRole('button', { name: 'auth.change_required_save' }));
		await vi.waitFor(() => expect(go).toHaveBeenCalledWith('/tickets'));
		expect(posted('/api/auth/password/required')).toEqual([
			{ newPassword: 'Chosen-Pass-1!', confirmPassword: 'Chosen-Pass-1!', return: '/tickets' }
		]);
	}, 30_000);

	test('with a second factor the code comes first; a mandatory set-up comes after', async () => {
		reply('/api/auth/oidc/status', { body: { enabled: false } });
		reply('/api/auth/login', { body: { success: true, totpRequired: true } });
		reply('/api/auth/totp/verify', { body: { success: true, passwordChangeRequired: true } });
		reply('/api/auth/password/required', { body: { success: true, totpSetupRequired: true } });
		reply('/api/auth/totp/required/setup', { body: { success: true, qrCode: '', secret: 'GEZDGNBV' } });
		const go = vi.fn();
		render(LoginView, { page: page(), query: readLoginQuery(''), go });
		await userEvent.type(screen.getByLabelText('auth.username'), 'demo');
		await userEvent.type(screen.getByLabelText('auth.password'), 'Temp-Pass-1{Enter}');
		await userEvent.type(await screen.findByLabelText('auth.totp_code'), '123456');
		expect(await screen.findByRole('heading', { name: 'auth.change_required_title' })).toBeInTheDocument();
		await userEvent.type(screen.getByLabelText('users.new_password'), 'Chosen-Pass-1!');
		await userEvent.type(screen.getByLabelText('password_link.repeat_password'), 'Chosen-Pass-1!{Enter}');
		expect(
			await screen.findByRole('heading', { name: 'auth.totp_setup_required_title' })
		).toBeInTheDocument();
		expect(go).not.toHaveBeenCalled();
	}, 30_000);
});
