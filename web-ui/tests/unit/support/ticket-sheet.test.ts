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
