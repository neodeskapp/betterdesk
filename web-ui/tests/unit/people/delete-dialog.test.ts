/**
 * Delete user: the dialog shows what goes and what stays with the
 * server's numbers, and a hand-over sends the account it goes to.
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import * as v from 'valibot';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import DeleteUserDialog from '../../../src/routes/(console)/users/DeleteUserDialog.svelte';
import { ConsoleUser } from '../../../src/lib/api/users.ts';

let sent: string[] = [];

const preview = {
	id: 5,
	username: 'carol',
	counts: {
		sessions: 2,
		client_sign_ins: 0,
		api_tokens: 1,
		organizations: 0,
		user_groups: 1,
		device_grants: 0,
		saved_reports: 3,
		open_tickets: 1,
		audit_entries: 40,
		tickets_opened: 0,
		ticket_comments: 0,
		chat_messages: 7
	},
	candidates: [{ id: 6, username: 'dave', role: 'viewer' }],
	organizations_move: true
};

beforeEach(() => {
	sent = [];
	vi.stubGlobal('fetch', async (input: string, init: RequestInit = {}) => {
		const url = new URL(input, location.origin);
		if ((init.method ?? 'GET') === 'GET')
			return new Response(JSON.stringify({ success: true, data: preview }), {
				headers: { 'Content-Type': 'application/json' }
			});
		sent.push(`${init.method} ${url.pathname}${url.search}`);
		return new Response('{"success":true}', { headers: { 'Content-Type': 'application/json' } });
	});
});

afterEach(() => {
	cleanup();
	vi.unstubAllGlobals();
});

const carol = v.parse(ConsoleUser, { id: 5, username: 'carol', role: 'operator' });

describe('delete user dialog', () => {
	test('counts what goes and what stays, and leaves out what is not there', async () => {
		render(DeleteUserDialog, {
			user: carol,
			roleLabel: (r: string) => r,
			onclose: () => {},
			ondeleted: () => {}
		});
		await screen.findByText('users.delete_goes_title');
		const goes = screen.getByRole('region', { name: 'users.delete_goes_title' });
		expect(goes.textContent).toContain('users.delete_item_sessions2');
		expect(goes.textContent).not.toContain('users.delete_item_client_sign_ins');
		const stays = screen.getByRole('region', { name: 'users.delete_stays_title' });
		expect(stays.textContent).toContain('users.delete_item_audit40');
		expect(stays.textContent).toContain('users.delete_item_chat7');
		expect(screen.getByText('users.delete_without_handover')).toBeTruthy();
	});

	test('a hand-over goes with the delete', async () => {
		const deleted = vi.fn();
		render(DeleteUserDialog, {
			user: carol,
			roleLabel: (r: string) => r,
			onclose: () => {},
			ondeleted: deleted
		});
		await userEvent.selectOptions(await screen.findByLabelText('users.delete_handover_label'), '6');
		expect(screen.getAllByText('users.delete_moves').length).toBeGreaterThan(0);
		await userEvent.click(screen.getByRole('button', { name: 'users.delete_action' }));
		await vi.waitFor(() => expect(deleted).toHaveBeenCalled());
		expect(sent).toEqual(['DELETE /api/users/5?handover_to=6']);
	});
});
