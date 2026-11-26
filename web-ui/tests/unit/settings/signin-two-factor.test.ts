/**
 * Settings › Sign-in, For everyone: "Require two-factor sign-in" saves
 * with its own Save button (BUGS L325: the button pointed at a form id no
 * element had, so Save sent nothing). Security notices: the four switches
 * show what the server has and save together.
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, test, vi } from 'vitest';

vi.mock('#lib/api/settings.ts', async (orig) => ({
	...(await orig<typeof import('../../../src/lib/api/settings.ts')>()),
	getEnrollment: vi.fn(async () => ({
		mode: 'managed',
		rich_approve: true,
		tag_picker: true,
		pending_count: 0
	})),
	getClientSessions: vi.fn(async () => ({ expiry_days: 7, sliding: true, max_days: 30 })),
	getTwoFactorPolicy: vi.fn(async () => false),
	saveTwoFactorPolicy: vi.fn(async () => ({ success: true })),
	getLdap: vi.fn(async () => ({})),
	getOidc: vi.fn(async () => ({})),
	getTotpEnabled: vi.fn(async () => false),
	getSecurityNotices: vi.fn(async () => ({
		new_signin: true,
		password_changed: false,
		two_factor_changed: true,
		email_changed: true
	})),
	saveSecurityNotices: vi.fn(async () => ({ success: true }))
}));

import * as api from '../../../src/lib/api/settings.ts';
import SignInTab from '../../../src/lib/settings/tabs/SignInTab.svelte';
import { session } from '../../../src/lib/state/session.svelte.ts';

afterEach(() => {
	cleanup();
	session.permissions = [];
});

describe('mandatory two-factor sign-in on Settings › Sign-in', () => {
	test('ticking the box and pressing its Save sends the requirement', async () => {
		session.permissions = ['server.config'];
		const user = userEvent.setup();
		render(SignInTab);
		const box = await screen.findByRole('checkbox', { name: /settings\.signin\.require_2fa/ });
		await vi.waitFor(() => expect(api.getTwoFactorPolicy).toHaveBeenCalled());
		await user.click(box);
		const save = box.closest('form')!.querySelector<HTMLButtonElement>('button[type="submit"]')!;
		expect(save.form, 'the Save button belongs to the two-factor form').toBe(box.closest('form'));
		await user.click(save);
		await vi.waitFor(() => expect(api.saveTwoFactorPolicy).toHaveBeenCalledWith(true));
	});
});

describe('security notices on Settings › Sign-in', () => {
	test('the switches show the saved values and Save sends all four', async () => {
		session.permissions = ['server.config'];
		const user = userEvent.setup();
		render(SignInTab);
		const changed = await screen.findByRole('checkbox', { name: /settings\.notices\.password_changed/ });
		await vi.waitFor(() => expect(changed).not.toBeChecked());
		const signin = screen.getByRole('checkbox', { name: /settings\.notices\.new_signin/ });
		expect(signin).toBeChecked();
		await user.click(signin);
		const save = signin.closest('form')!.querySelector<HTMLButtonElement>('button[type="submit"]')!;
		await user.click(save);
		await vi.waitFor(() =>
			expect(api.saveSecurityNotices).toHaveBeenCalledWith({
				new_signin: false,
				password_changed: false,
				two_factor_changed: true,
				email_changed: true
			})
		);
	});
});
