/**
 * Unread counts on conversation rows: a span can't carry a name, so the
 * number is hidden and a sentence for screen readers sits next to it
 * (the bottom bar's pattern, WCAG 4.1.2).
 */
import { render, screen } from '@testing-library/svelte';
import { expect, test, vi } from 'vitest';

const conv = {
	id: 'c1',
	kind: 'support',
	state: 'open',
	last_message_id: '1',
	last_message_at: 0,
	last_message: null,
	unread: 4,
	device: {
		id: '900100001',
		name: 'demo-reception-01',
		hostname: 'demo-reception-01',
		owner: '',
		folder: '',
		platform: 'windows',
		online: true,
		chat_connected: true,
		agent_chat: false
	}
};

vi.mock('../../../src/lib/chat/api.ts', () => ({
	loadInbox: vi.fn(async () => ({
		conversations: [conv],
		counts: { open: 1, mine: 0, closed: 0, unassigned: 1 }
	})),
	loadTeam: vi.fn(async () => ({ conversations: [], people: [], old: [] })),
	searchMessages: vi.fn(async () => ({ messages: [], conversations: {} })),
	loadServerSettings: vi.fn(async () => ({ mail_after_minutes: 15, mail_preview: true, computers: true }))
}));

vi.mock('../../../src/lib/chat/client.svelte.ts', () => ({
	chat: {
		may: { support: true, team: true, assign: true, delete_any: false },
		listVersion: 0,
		presence: {},
		supportUnread: 0,
		teamUnread: 0,
		me: { id: 1, name: 'Admin', username: 'admin' }
	}
}));

const { default: ConversationList } = await import('../../../src/lib/chat/ConversationList.svelte');

test('a row says "4 new" in its name and hides the bare number', async () => {
	render(ConversationList, {
		section: 'support',
		filter: 'open',
		active: null,
		onsection: () => {},
		onfilter: () => {},
		onopen: () => {},
		onnew: () => {},
		onperson: () => {}
	});
	const row = await screen.findByRole('button', { name: /demo-reception-01/ });
	expect(row).toHaveAccessibleName(expect.stringContaining('chat.unread_count'));
	const badge = row.querySelector('.row-badge')!;
	expect(badge).not.toHaveAttribute('aria-label');
	expect(badge.querySelector('[aria-hidden="true"]')).toHaveTextContent(/^4$/);
});
