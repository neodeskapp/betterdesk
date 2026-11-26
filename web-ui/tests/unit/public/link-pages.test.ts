/**
 * The set-password and invite pages (WCAG 4.1.3): the form is not inside a
 * live region (every error line and the whole next step were read), only
 * the status sentence is; when the step changes, focus goes to the new
 * heading so the step is read from its start.
 */
import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import { ApiError } from '../../../src/lib/api/client.ts';

vi.mock('$app/paths', () => ({ resolve: (p: string) => '/app' + (p || '/'), asset: (p: string) => '/' + p }));
vi.setConfig({ testTimeout: 30_000 });

const links = vi.hoisted(() => ({
	lookup: vi.fn(),
	available: vi.fn(),
	request: vi.fn()
}));
vi.mock('#lib/api/passwordlinks.ts', () => ({
	linkToken: (hash: string) => hash.replace(/^#/, ''),
	lookupPasswordLink: links.lookup,
	passwordResetAvailable: links.available,
	redeemPasswordLink: vi.fn(),
	requestPasswordReset: links.request
}));
vi.mock('#lib/api/signin.ts', () => ({
	getLoginPage: vi.fn(async () => ({ csrfToken: 'tok', appName: 'NeoDesk' }))
}));
vi.mock('#lib/public/boot.svelte.ts', () => ({ dropEarlySession: () => {} }));
const invites = vi.hoisted(() => ({ lookup: vi.fn() }));
vi.mock('#lib/api/invitations.ts', () => ({ lookupInvitation: invites.lookup, redeemInvitation: vi.fn() }));

beforeEach(() => {
	sessionStorage.clear();
	history.replaceState(null, '', '/set-password');
});
afterEach(() => {
	vi.clearAllMocks();
});

test('set-password: an unusable link moves focus to the new heading; nothing around the form is live', async () => {
	history.replaceState(null, '', '/set-password#tok-1');
	links.lookup.mockRejectedValue(new ApiError('Not found', 404));
	const Page = (await import('../../../src/routes/(public)/set-password/+page.svelte')).default;
	render(Page);
	const heading = await screen.findByRole('heading', { level: 1, name: 'password_link.invalid_title' });
	await vi.waitFor(() => expect(heading).toHaveFocus());
	expect(document.querySelector('.link-body')).not.toHaveAttribute('aria-live');
});

test('set-password: after asking for a link, the heading has focus and the sentence is a status', async () => {
	links.available.mockResolvedValue(true);
	links.request.mockResolvedValue(undefined);
	const Page = (await import('../../../src/routes/(public)/set-password/+page.svelte')).default;
	render(Page);
	const field = await screen.findByRole('textbox', { name: /auth\.forgot_field/ });
	await userEvent.type(field, 'carol{Enter}');
	const sent = await screen.findByText('auth.forgot_sent');
	expect(sent).toHaveAttribute('role', 'status');
	await vi.waitFor(() => expect(screen.getByRole('heading', { level: 1 })).toHaveFocus());
});

test('invite: an unusable code moves focus to the heading; nothing around the form is live', async () => {
	history.replaceState(null, '', '/invite#' + 'ab'.repeat(32));
	invites.lookup.mockRejectedValue(new ApiError('Not found', 404));
	const Page = (await import('../../../src/routes/(public)/invite/+page.svelte')).default;
	render(Page);
	await screen.findByText('organizations.invite_invalid');
	await vi.waitFor(() =>
		expect(screen.getByRole('heading', { level: 1, name: 'organizations.invite_title' })).toHaveFocus()
	);
	expect(document.querySelector('.invite-body')).not.toHaveAttribute('aria-live');
});
