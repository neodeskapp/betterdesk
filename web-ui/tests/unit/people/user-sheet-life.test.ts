/**
 * Edit user: the End date goes out as the start of the chosen day, the
 * own account has no End date, a pending address shows with Cancel, and a
 * directory account's address is not sent.
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import * as v from 'valibot';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import UserSheet from '../../../src/routes/(console)/users/UserSheet.svelte';
import { ConsoleUser } from '../../../src/lib/api/users.ts';
import { session } from '../../../src/lib/state/session.svelte.ts';

let writes: { method: string; path: string; body: Record<string, unknown> | null }[] = [];

beforeEach(() => {
	writes = [];
	vi.stubGlobal('fetch', async (input: string, init: RequestInit = {}) => {
		const method = init.method ?? 'GET';
		if (method !== 'GET')
			writes.push({
				method,
				path: new URL(input, location.origin).pathname,
				body: init.body ? JSON.parse(String(init.body)) : null
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
	session.isSuperAdmin = false;
	session.user = null;
});

const sheet = (user: Record<string, unknown>, onsaved = () => {}) =>
	render(UserSheet, {
		user: v.parse(ConsoleUser, { role: 'viewer', ...user }),
		groups: [],
		roleLabel: (r: string) => r,
		providerLabel: (p: string) => p,
		mailReady: true,
		onclose: () => {},
		onsaved
	});

describe('edit user, life cycle', () => {
	test('an end date goes out as the start of the day', async () => {
		const saved = vi.fn();
		sheet({ id: 7, username: 'carol', email: 'c@example.com' }, saved);
		const field = screen.getByLabelText('users.expiry_label');
		await userEvent.type(field, '2030-06-30');
		await userEvent.click(screen.getByRole('button', { name: 'actions.save' }));
		await vi.waitFor(() => expect(saved).toHaveBeenCalled());
		expect(writes[0]?.path).toBe('/api/users/7');
		expect(writes[0]?.body?.expires_at).toBe(new Date('2030-06-30T00:00:00').toISOString());
	});

	test('the own account has no end date', () => {
		sheet({ id: 1, username: 'root', role: 'super_admin' });
		expect(screen.queryByLabelText('users.expiry_label')).toBeNull();
	});

	test('a pending address shows and can be cancelled', async () => {
		sheet({
			id: 7,
			username: 'carol',
			email: 'c@example.com',
			email_pending: { email: 'new@example.com', expires_at: '2030-01-01T00:00:00Z', expired: false }
		});
		expect(screen.getByText('users.email_pending')).toBeTruthy();
		await userEvent.click(screen.getByRole('button', { name: 'users.email_change_cancel' }));
		await vi.waitFor(() =>
			expect(writes).toEqual([{ method: 'DELETE', path: '/api/users/7/email-change', body: null }])
		);
		await vi.waitFor(() => expect(screen.queryByText('users.email_pending')).toBeNull());
	});

	test('a directory account keeps its address', async () => {
		const saved = vi.fn();
		sheet({ id: 9, username: 'dir', email: 'dir@example.com', auth_provider: 'ldap' }, saved);
		await userEvent.click(screen.getByRole('button', { name: 'actions.save' }));
		await vi.waitFor(() => expect(saved).toHaveBeenCalled());
		expect(writes[0]?.body).not.toHaveProperty('email');
	});
});
