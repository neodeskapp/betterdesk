/**
 * An action on several accounts: the dialog asks the server first with a
 * dry run and shows what would happen to each account, runs the action
 * only after Confirm, and then says what happened.
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import * as v from 'valibot';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import BulkDialog from '../../../src/routes/(console)/users/BulkDialog.svelte';
import { ConsoleUser } from '../../../src/lib/api/users.ts';

let sent: { path: string; body: Record<string, unknown> }[] = [];

const results = [
	{ id: 5, username: 'carol', outcome: 'changed' },
	{ id: 6, username: 'dave', outcome: 'unchanged' },
	{ id: 1, username: 'me', outcome: 'refused', error: 'Your own account is left out.' }
];

beforeEach(() => {
	sent = [];
	vi.stubGlobal('fetch', async (input: string, init: RequestInit = {}) => {
		const url = new URL(input, location.origin);
		const body = JSON.parse(String(init.body ?? '{}')) as Record<string, unknown>;
		sent.push({ path: url.pathname, body });
		return new Response(JSON.stringify({ success: true, data: { results, dry_run: !!body.dry_run } }), {
			headers: { 'Content-Type': 'application/json' }
		});
	});
});

afterEach(() => {
	cleanup();
	vi.unstubAllGlobals();
});

const users = [5, 6, 1].map((id) => v.parse(ConsoleUser, { id, username: `u${id}`, role: 'viewer' }));
const props = {
	users,
	roles: ['viewer', 'operator'],
	roleLabel: (r: string) => r,
	orgs: [],
	onclose: () => {},
	ondone: () => {}
};

describe('actions on several accounts', () => {
	test('a dry run first: what changes, what is so already, what is left out and why; nothing runs before Confirm', async () => {
		const ondone = vi.fn();
		render(BulkDialog, { ...props, kind: 'disable', ondone });
		await screen.findByText(/users\.bulk_changes_one/);
		expect(sent).toEqual([{ path: '/api/users/bulk/disable', body: { ids: [5, 6, 1], dry_run: true } }]);
		expect(screen.getByText('users.bulk_already_one')).toBeTruthy();
		expect(screen.getByRole('listitem').textContent).toBe('me: Your own account is left out.');

		await userEvent.click(screen.getByRole('button', { name: 'users.bulk_disable_confirm_one' }));
		await screen.findByText('users.bulk_done_one');
		expect(sent[1]).toEqual({ path: '/api/users/bulk/disable', body: { ids: [5, 6, 1] } });
		await userEvent.click(screen.getByRole('button', { name: 'actions.close' }));
		expect(ondone).toHaveBeenCalledOnce();
	});

	test('a role is asked for before the server is; the older route takes the password change', async () => {
		render(BulkDialog, { ...props, kind: 'role' });
		expect(sent).toEqual([]);
		expect(
			(screen.getByRole('button', { name: 'users.bulk_role_confirm_many' }) as HTMLButtonElement).disabled
		).toBe(true);
		await userEvent.selectOptions(screen.getByRole('combobox'), 'operator');
		await screen.findByText(/users\.bulk_changes_one/);
		expect(sent).toEqual([
			{ path: '/api/users/bulk/role', body: { role: 'operator', ids: [5, 6, 1], dry_run: true } }
		]);
		cleanup();

		sent = [];
		render(BulkDialog, { ...props, kind: 'require-password-change' });
		await screen.findByText(/users\.bulk_changes_one/);
		expect(sent).toEqual([
			{ path: '/api/users/require-password-change', body: { required: true, ids: [5, 6, 1], dry_run: true } }
		]);
	});
});
