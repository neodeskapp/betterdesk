/**
 * Organization invitations (BUGS L42): the link and its code, an
 * invitation's state, the dialog that shows the link once, and the
 * /invite page that redeems it, signed out and signed in.
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import InvitePage from '../../../src/routes/(public)/invite/+page.svelte';
import InviteDialog from '../../../src/routes/(console)/organizations/[id]/InviteDialog.svelte';
import { invitationCode, invitationLink, invitationState } from '../../../src/lib/people/orgs.ts';

const code = '0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef';

const loginPage = {
	success: true,
	csrfToken: 'csrf-1',
	appName: 'NeoDesk',
	appVersion: '1',
	desktopMode: false,
	signedIn: false,
	branding: {
		logoType: '',
		logoSvg: '',
		logoUrl: '',
		logoText: '',
		logoTextAccent: '',
		loginTitle: '',
		loginSubtitle: '',
		loginPromoMode: 'classic',
		loginSlides: [],
		footerText: '',
		showPoweredBy: false
	},
	languages: [],
	attestation: null
};

type Reply = { status: number; body: unknown };
let replies: Record<string, Reply[]> = {};
let sent: { path: string; body: unknown; csrf: string | null }[] = [];

beforeEach(() => {
	replies = {};
	sent = [];
	sessionStorage.clear();
	vi.stubGlobal('fetch', async (input: string, init: RequestInit = {}) => {
		const path = new URL(input, location.origin).pathname;
		const headers = new Headers(init.headers);
		sent.push({
			path,
			body: typeof init.body === 'string' ? JSON.parse(init.body) : undefined,
			csrf: headers.get('X-CSRF-Token')
		});
		const reply =
			path === '/api/auth/login-page'
				? { status: 200, body: loginPage }
				: (replies[path]?.shift() ?? { status: 500, body: {} });
		return new Response(JSON.stringify(reply.body), {
			status: reply.status,
			headers: { 'Content-Type': 'application/json' }
		});
	});
});

afterEach(() => {
	cleanup();
	vi.unstubAllGlobals();
	history.replaceState(null, '', '/');
});

const lookup = (account: { username: string } | null): Reply => ({
	status: 200,
	body: {
		success: true,
		organization: { name: 'Acme', slug: 'acme' },
		expires_at: '2026-12-01T00:00:00Z',
		account
	}
});

describe('invitation links', () => {
	test('the code goes after #, and comes back out of a link or on its own', () => {
		const link = invitationLink('https://console.example', code);
		expect(link).toBe(`https://console.example/invite#${code}`);
		expect(invitationCode(link)).toBe(code);
		expect(invitationCode(`  ${code.toUpperCase()} `)).toBe(code);
		for (const bad of ['', 'https://console.example/invite', `#${code}x`, 'abc', '%E0%A4'])
			expect(invitationCode(bad)).toBe('');
	});

	test('an invitation is used, expired at its time, or open', () => {
		const now = Date.parse('2026-11-22T12:00:00Z');
		expect(
			invitationState({ id: '1', email: '', expires_at: '2026-11-23T00:00:00Z', used_at: null }, now)
		).toBe('open');
		expect(
			invitationState({ id: '1', email: '', expires_at: '2026-11-22T12:00:00Z', used_at: null }, now)
		).toBe('expired');
		expect(
			invitationState(
				{ id: '1', email: '', expires_at: '2026-11-23T00:00:00Z', used_at: '2026-11-22T10:00:00Z' },
				now
			)
		).toBe('used');
	});
});

describe('the invite dialog', () => {
	test('after creating, it shows the link with the code once and closes as saved', async () => {
		const user = userEvent.setup();
		replies['/api/panel/org/o1/invite'] = [
			{ status: 201, body: { id: 'inv-1', token: code, expires_at: '2026-12-01T00:00:00Z' } }
		];
		const onsaved = vi.fn();
		render(InviteDialog, { orgId: 'o1', onclose: () => {}, onsaved });
		await user.click(screen.getByRole('button', { name: 'organizations.create_invitation' }));
		await vi.waitFor(() => expect(document.body.textContent).toContain(`${location.origin}/invite#${code}`));
		expect(sent.find((s) => s.path === '/api/panel/org/o1/invite')?.body).toEqual({
			email: '',
			expires_in_hours: 72
		});
		await user.click(screen.getByRole('button', { name: 'common.close' }));
		expect(onsaved).toHaveBeenCalled();
	});
});

describe('the invite page', () => {
	test('signed out: the code from the link leaves the address bar, the form makes the sign-in', async () => {
		const user = userEvent.setup();
		history.replaceState(null, '', `/invite#${code}`);
		replies['/api/invitations/lookup'] = [lookup(null)];
		replies['/api/invitations/redeem'] = [
			{ status: 201, body: { success: true, organization: { name: 'Acme', slug: 'acme' }, username: 'anna' } }
		];
		render(InvitePage);
		await vi.waitFor(() => expect(screen.getByText('organizations.invite_for')).toBeInTheDocument());
		expect(location.hash).toBe('');
		expect(sessionStorage.getItem('nd.invitation')).toBe(code);
		expect(sent.find((s) => s.path === '/api/invitations/lookup')).toMatchObject({
			body: { token: code },
			csrf: 'csrf-1'
		});
		expect(document.querySelector('.invite-signed-in')).toBeNull();

		await user.type(screen.getByLabelText('organizations.username'), 'anna');
		await user.type(screen.getByLabelText('organizations.password'), 'long-enough-1');
		await user.type(screen.getByLabelText('organizations.invite_password_again'), 'long-enough-2');
		await user.click(screen.getByRole('button', { name: 'organizations.invite_submit' }));
		await vi.waitFor(() => expect(screen.getByText('settings.passwords_not_match')).toBeInTheDocument());
		expect(sent.some((s) => s.path === '/api/invitations/redeem')).toBe(false);

		await user.clear(screen.getByLabelText('organizations.invite_password_again'));
		await user.type(screen.getByLabelText('organizations.invite_password_again'), 'long-enough-1');
		await user.click(screen.getByRole('button', { name: 'organizations.invite_submit' }));
		await vi.waitFor(() => expect(screen.getByText('organizations.invite_done_title')).toBeInTheDocument());
		expect(sent.find((s) => s.path === '/api/invitations/redeem')?.body).toEqual({
			token: code,
			username: 'anna',
			password: 'long-enough-1'
		});
		expect(sessionStorage.getItem('nd.invitation')).toBeNull();
	});

	test('signed in: the page says the new sign-in is separate from the console account', async () => {
		sessionStorage.setItem('nd.invitation', code);
		history.replaceState(null, '', '/invite');
		replies['/api/invitations/lookup'] = [lookup({ username: 'e2e_admin' })];
		render(InvitePage);
		await vi.waitFor(() => expect(document.querySelector('.invite-signed-in')).not.toBeNull());
		expect(document.querySelector('.invite-signed-in')?.textContent).toContain(
			'organizations.invite_signed_in'
		);
	});

	test('a code that cannot be used gets one sentence and no form', async () => {
		history.replaceState(null, '', `/invite#${code}`);
		replies['/api/invitations/lookup'] = [{ status: 404, body: { success: false, error: 'x' } }];
		render(InvitePage);
		await vi.waitFor(() => expect(document.body.textContent).toContain('organizations.invite_invalid'));
		expect(screen.queryByLabelText('organizations.username')).toBeNull();
		expect(sessionStorage.getItem('nd.invitation')).toBeNull();
	});

	test('a username the organization has is refused on its field, and the invitation stays', async () => {
		const user = userEvent.setup();
		history.replaceState(null, '', `/invite#${code}`);
		replies['/api/invitations/lookup'] = [lookup(null)];
		replies['/api/invitations/redeem'] = [{ status: 409, body: { success: false, error: 'taken' } }];
		render(InvitePage);
		await vi.waitFor(() => expect(screen.getByLabelText('organizations.username')).toBeInTheDocument());
		await user.type(screen.getByLabelText('organizations.username'), 'anna');
		await user.type(screen.getByLabelText('organizations.password'), 'long-enough-1');
		await user.type(screen.getByLabelText('organizations.invite_password_again'), 'long-enough-1');
		await user.click(screen.getByRole('button', { name: 'organizations.invite_submit' }));
		await vi.waitFor(() =>
			expect(screen.getByText('organizations.invite_username_taken')).toBeInTheDocument()
		);
		expect(sessionStorage.getItem('nd.invitation')).toBe(code);
	});

	test('without a code the page asks for the link', async () => {
		const user = userEvent.setup();
		history.replaceState(null, '', '/invite');
		replies['/api/invitations/lookup'] = [lookup(null)];
		render(InvitePage);
		await vi.waitFor(() =>
			expect(screen.getByLabelText('organizations.invite_code_label')).toBeInTheDocument()
		);
		await user.type(
			screen.getByLabelText('organizations.invite_code_label'),
			`https://x.example/invite#${code}`
		);
		await user.click(screen.getByRole('button', { name: 'organizations.invite_code_submit' }));
		await vi.waitFor(() => expect(screen.getByText('organizations.invite_for')).toBeInTheDocument());
	});
});
