/**
 * The ticket sheet offers the comment box only to accounts that may
 * comment (device.edit, as the server asks since TK5).
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import TicketSheet from '../../../src/routes/(console)/tickets/TicketSheet.svelte';
import { session } from '../../../src/lib/state/session.svelte.ts';

beforeEach(() => {
	vi.stubGlobal('fetch', async () => {
		const ticket = { id: 3, title: 'Kiosk', status: 'open', priority: 'high', comments: [], attachments: [] };
		return new Response(JSON.stringify(ticket), {
			status: 200,
			headers: { 'Content-Type': 'application/json' }
		});
	});
});

afterEach(() => {
	cleanup();
	vi.unstubAllGlobals();
	session.permissions = [];
	session.user = null;
});

function signIn(role: string, permissions: string[]) {
	session.isSuperAdmin = false;
	session.permissions = permissions;
	session.user = { id: 1, username: 'demo', role, preferredLanguage: null, avatarVersion: null };
}

const props = {
	id: 3,
	onclose: () => {},
	onchanged: () => {},
	onedit: () => {},
	ondelete: async () => false
};

describe('ticket sheet', () => {
	test.each([
		['viewer', ['device.view'], false],
		['operator', ['device.view', 'device.edit'], true]
	])('%s: the comment box only with device.edit', async (role, permissions, shown) => {
		signIn(role, permissions);
		render(TicketSheet, props);
		await vi.waitFor(() => expect(screen.queryByText('tickets.no_comments')).not.toBeNull());
		expect(!!screen.queryByRole('button', { name: 'tickets.add_comment' })).toBe(shown);
		expect(!!screen.queryByLabelText('tickets.comment_label')).toBe(shown);
	});
});

describe('a ticket from a computer', () => {
	test('the creator and the replies from the app read as the person at the computer', async () => {
		vi.stubGlobal('fetch', async () => {
			const ticket = {
				id: 4,
				title: 'Printer',
				status: 'open',
				priority: 'medium',
				source: 'device',
				reporter: 'Erika Muster',
				created_by: 'agent:900100001',
				device_id: '900100001',
				comments: [
					{
						id: 1,
						author: 'agent:900100001',
						body: 'It stopped again',
						from: 'device',
						created_at: '2026-11-27 10:00:00'
					},
					{ id: 2, author: 'demo', body: 'On my way', from: 'staff', created_at: '2026-11-27 10:05:00' }
				],
				attachments: [
					{ id: 7, filename: 'computer-info.json', size_bytes: 900, uploaded_by: 'agent:900100001' }
				]
			};
			return new Response(JSON.stringify(ticket), {
				status: 200,
				headers: { 'Content-Type': 'application/json' }
			});
		});
		signIn('operator', ['device.view', 'device.edit']);
		render(TicketSheet, { ...props, id: 4 });
		await vi.waitFor(() => expect(screen.queryByText('It stopped again')).not.toBeNull());
		// Created by, the reply and the file: the person at the computer, never agent:<id>
		expect(screen.getAllByText('tickets.at_computer_named').length).toBe(2);
		expect(screen.queryByText(/agent:900100001/)).toBeNull();
	});
});
