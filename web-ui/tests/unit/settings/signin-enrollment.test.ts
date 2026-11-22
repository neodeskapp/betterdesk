/**
 * The enrollment mode has one place, the Device tokens page, under
 * enrollment.manage (BUGS L100): Settings › Sign-in shows the mode it is in
 * and links there for accounts that may change it, and saving its
 * enrollment form sends the two approval options without a mode.
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

vi.mock('#lib/api/settings.ts', async (orig) => ({
	...(await orig<typeof import('../../../src/lib/api/settings.ts')>()),
	getEnrollment: vi.fn(async () => ({ mode: 'managed', rich_approve: true, tag_picker: true, pending_count: 0 })),
	saveEnrollment: vi.fn(async () => ({ mode: 'managed', rich_approve: false, tag_picker: true })),
	getClientSessions: vi.fn(async () => ({ expiry_days: 7, sliding: true, max_days: 30 })),
	getLdap: vi.fn(async () => ({})),
	getOidc: vi.fn(async () => ({})),
	getTotpEnabled: vi.fn(async () => false)
}));

import * as api from '../../../src/lib/api/settings.ts';
import SignInTab from '../../../src/lib/settings/tabs/SignInTab.svelte';
import { session } from '../../../src/lib/state/session.svelte.ts';

beforeEach(() => {
	session.isSuperAdmin = false;
	vi.mocked(api.saveEnrollment).mockClear();
});

afterEach(() => {
	cleanup();
	session.permissions = [];
});

describe('enrollment on Settings › Sign-in', () => {
	test('no mode to pick here; the link to Device tokens for enrollment.manage; the save sends no mode', async () => {
		session.permissions = ['server.config', 'enrollment.manage'];
		const user = userEvent.setup();
		render(SignInTab);
		const link = await screen.findByRole('link', { name: 'settings.enrollment_mode_link' });
		expect(link.getAttribute('href')).toBe('/tokens');
		expect(screen.queryByRole('radio', { name: 'tokens.mode_open' })).toBeNull();
		expect(screen.getByText('tokens.mode_managed')).toBeTruthy();

		await user.click(screen.getByRole('checkbox', { name: 'settings.enrollment_rich_approve' }));
		await user.click(screen.getByRole('button', { name: 'settings.signin.enrollment_save' }));
		await vi.waitFor(() => expect(api.saveEnrollment).toHaveBeenCalledTimes(1));
		expect(vi.mocked(api.saveEnrollment).mock.calls[0]![0]).toEqual({ rich_approve: false, tag_picker: true });
	});

	test('without enrollment.manage the mode shows without a link', async () => {
		session.permissions = ['server.config'];
		render(SignInTab);
		await screen.findByText('tokens.mode_managed');
		expect(screen.queryByRole('link', { name: 'settings.enrollment_mode_link' })).toBeNull();
	});
});
