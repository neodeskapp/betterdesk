/**
 * Edit user with a new address the server refuses (A274): the other
 * changes are saved, so the status line says that and why, by the
 * refusal's code and not the server's English; without email settings
 * the way to them shows for who may open them; closing the sheet then
 * loads the list again.
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import * as v from 'valibot';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import UserSheet from '../../../src/routes/(console)/users/UserSheet.svelte';
import { ConsoleUser } from '../../../src/lib/api/users.ts';
import { setStrings } from '../../../src/lib/i18n/i18n.svelte.ts';
import { session } from '../../../src/lib/state/session.svelte.ts';

const STRINGS = {
	users: {
		email_change_needs_mail: 'Needs the email settings.',
		email_confirm_mail_failed: 'The mail server refused the mail.',
		email_not_changed_rest_saved: 'Other changes saved, the address not. {reason}'
	}
};

let refusal: { status: number; body: Record<string, unknown> } | null = null;

beforeEach(() => {
	refusal = null;
	setStrings('en', 'ltr', STRINGS);
	vi.stubGlobal('fetch', async (_input: string, init: RequestInit = {}) => {
		if ((init.method ?? 'GET') === 'PATCH' && refusal)
			return new Response(JSON.stringify(refusal.body), {
				status: refusal.status,
				headers: { 'Content-Type': 'application/json' }
			});
		return new Response('{"success":true,"data":{}}', { headers: { 'Content-Type': 'application/json' } });
	});
	session.isSuperAdmin = true;
	session.user = {
		id: 1,
		username: 'root',
		role: 'super_admin',
		preferredLanguage: null,
		avatarVersion: null
	};
});

afterEach(() => {
	cleanup();
	vi.unstubAllGlobals();
	setStrings('en', 'ltr', {});
	session.isSuperAdmin = false;
	session.permissions = [];
	session.user = null;
});

function sheet(user: Record<string, unknown>, mailReady: boolean) {
	const onsaved = vi.fn();
	const onclose = vi.fn();
	render(UserSheet, {
		user: v.parse(ConsoleUser, { role: 'super_admin', ...user }),
		groups: [],
		roleLabel: (r: string) => r,
		providerLabel: (p: string) => p,
		mailReady,
		onclose,
		onsaved
	});
	return { onsaved, onclose };
}

async function saveWithAddress(address: string) {
	const field = screen.getByLabelText('users.email');
	await userEvent.clear(field);
	await userEvent.type(field, address);
	await userEvent.click(screen.getByRole('button', { name: 'actions.save' }));
}

describe('edit user, a refused address', () => {
	test('your own row without email settings: the rest is saved, why, and the way to the settings', async () => {
		refusal = {
			status: 409,
			body: {
				success: false,
				error: 'English from the server',
				code: 'email_change_needs_mail',
				saved: ['groupGuids']
			}
		};
		const { onsaved, onclose } = sheet({ id: 1, username: 'root', email: 'root@example.com' }, false);
		await saveWithAddress('new@example.com');
		expect(
			await screen.findByText('Other changes saved, the address not. Needs the email settings.')
		).toBeTruthy();
		expect(screen.queryByText('English from the server')).toBeNull();
		const settings = screen.getByRole('link', { name: 'account.email_settings_link' });
		expect(settings.getAttribute('href')).toMatch(/\/settings\?tab=email$/);
		expect(onsaved).not.toHaveBeenCalled();
		// Something was saved: closing loads the list again
		await userEvent.click(screen.getByRole('button', { name: 'actions.cancel' }));
		expect(onsaved).toHaveBeenCalledTimes(1);
		expect(onclose).not.toHaveBeenCalled();
	});

	test('the way to the settings only for who may open them', async () => {
		session.isSuperAdmin = false;
		session.permissions = ['user.view', 'user.edit'];
		refusal = {
			status: 409,
			body: { success: false, error: 'x', code: 'email_change_needs_mail', saved: ['groupGuids'] }
		};
		sheet({ id: 1, username: 'root', email: 'root@example.com' }, false);
		await saveWithAddress('new@example.com');
		await screen.findByText('Other changes saved, the address not. Needs the email settings.');
		expect(screen.queryByRole('link', { name: 'account.email_settings_link' })).toBeNull();
	});

	test("another account's row when the mail server refuses the confirmation", async () => {
		refusal = {
			status: 502,
			body: {
				success: false,
				error: 'English from the server',
				code: 'email_confirmation_not_sent',
				saved: ['groupGuids']
			}
		};
		sheet({ id: 7, username: 'carol', role: 'viewer', email: 'c@example.com' }, true);
		await saveWithAddress('c2@example.com');
		expect(
			await screen.findByText('Other changes saved, the address not. The mail server refused the mail.')
		).toBeTruthy();
		expect(screen.queryByRole('link', { name: 'account.email_settings_link' })).toBeNull();
	});

	test('nothing saved beside the address: only why, and closing just closes', async () => {
		refusal = {
			status: 502,
			body: { success: false, error: 'English from the server', code: 'email_confirmation_not_sent' }
		};
		const { onsaved, onclose } = sheet(
			{ id: 7, username: 'carol', role: 'viewer', email: 'c@example.com' },
			true
		);
		await saveWithAddress('c2@example.com');
		expect(await screen.findByText('The mail server refused the mail.')).toBeTruthy();
		await userEvent.click(screen.getByRole('button', { name: 'actions.cancel' }));
		expect(onclose).toHaveBeenCalledTimes(1);
		expect(onsaved).not.toHaveBeenCalled();
	});
});
