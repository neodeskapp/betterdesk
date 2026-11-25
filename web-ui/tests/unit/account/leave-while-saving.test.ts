/**
 * Leaving a page of settings (here Your account's password) while a save runs, by the setup guide's way back or any
 * other link: the page waits for the save and leaves without asking when
 * it saved everything. It asks only about changes that are still unsaved.
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import { anyDirty, mayLeave } from '../../../src/lib/settings/dirty.ts';
import { session, type User } from '../../../src/lib/state/session.svelte.ts';

vi.mock('$app/paths', () => ({ resolve: (p: string) => '/app' + (p || '/'), asset: (p: string) => '/' + p }));

const json = (body: unknown, status = 200) =>
	new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });

/** Answers the password change the test holds back */
let answerSave: ((r: Response) => void) | null;

beforeEach(() => {
	answerSave = null;
	session.user = { id: 7, username: 'hank', role: 'viewer' } as User;
	vi.stubGlobal(
		'fetch',
		vi.fn((input: string) => {
			const url = new URL(input, 'http://localhost').pathname;
			if (url === '/api/auth/password') return new Promise<Response>((resolve) => (answerSave = resolve));
			return Promise.resolve(
				json(
					url === '/api/auth/totp/status'
						? { success: true, enabled: false, disable_with: 'password' }
						: { success: true }
				)
			);
		})
	);
});

afterEach(() => {
	cleanup();
	vi.unstubAllGlobals();
	session.user = null;
});

async function fillPassword() {
	const user = userEvent.setup();
	const PasswordAndTwoFactor = (await import('../../../src/lib/account/PasswordAndTwoFactor.svelte')).default;
	render(PasswordAndTwoFactor);
	await user.type(await screen.findByLabelText('settings.current_password'), 'old-password');
	await user.type(screen.getByLabelText('settings.new_password'), 'new-password-1');
	await user.type(screen.getByLabelText('settings.confirm_password'), 'new-password-1');
	return user;
}

async function startSave(user: ReturnType<typeof userEvent.setup>) {
	await user.click(screen.getByRole('button', { name: 'settings.save_password' }));
	await vi.waitFor(() => expect(answerSave).not.toBeNull());
	expect(anyDirty()).toBe(true);
}

test('leaving while the password is saved waits for the save and does not ask', async () => {
	const user = await fillPassword();
	await startSave(user);
	const ask = vi.fn(async () => false);
	const leave = mayLeave(ask);
	answerSave!(json({ success: true }));
	expect(await leave).toBe(true);
	expect(ask).not.toHaveBeenCalled();
	expect(anyDirty()).toBe(false);
}, 30_000);

test('a save that fails keeps the changes, and leaving asks after it', async () => {
	const user = await fillPassword();
	await startSave(user);
	const ask = vi.fn(async () => false);
	const leave = mayLeave(ask);
	await Promise.resolve();
	expect(ask).not.toHaveBeenCalled();
	answerSave!(json({ success: false, error: 'Current password is wrong' }, 400));
	expect(await leave).toBe(false);
	expect(ask).toHaveBeenCalledTimes(1);
	expect(anyDirty()).toBe(true);
}, 30_000);

test('changes nobody is saving are asked about right away', async () => {
	await fillPassword();
	const ask = vi.fn(async () => true);
	expect(await mayLeave(ask)).toBe(true);
	expect(ask).toHaveBeenCalledTimes(1);
}, 30_000);
