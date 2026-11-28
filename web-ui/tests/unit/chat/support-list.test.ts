/**
 * The Support list: the filters name their count only when there is one,
 * "Waiting for someone" shows only while the computer wrote last, and a
 * search lists the computers without a conversation ("Write to a
 * computer") and, when Open has nothing, the closed conversations.
 */
import { fireEvent, render, screen, within } from '@testing-library/svelte';
import { beforeAll, beforeEach, describe, expect, test, vi } from 'vitest';
import { setStrings } from '../../../src/lib/i18n/i18n.svelte.ts';
import type { Device, InboxFilter, InboxItem } from '../../../src/lib/chat/types.ts';

function device(id: string, name: string): Device {
	return {
		id,
		name,
		hostname: name,
		owner: '',
		folder: '',
		platform: 'windows',
		online: true,
		chat_connected: true,
		agent_chat: false
	};
}

function conv(d: Device, more: Partial<InboxItem> = {}): InboxItem {
	return {
		id: d.id,
		kind: 'support',
		state: 'open',
		last_message_id: '1',
		last_message_at: 1,
		last_message: null,
		unread: 0,
		device: d,
		...more
	};
}

const reception = device('900100001', 'reception-01');
const lobby = device('900100002', 'reception-02');
const archive = device('900100003', 'reception-03');

let open: InboxItem[] = [];
let closed: InboxItem[] = [];

vi.mock('../../../src/lib/chat/api.ts', () => ({
	loadInbox: vi.fn(async (filter: InboxFilter) => ({
		conversations: filter === 'closed' ? closed : open,
		counts: { open: open.length, mine: 0, closed: closed.length, unassigned: 0 }
	})),
	findDevices: vi.fn(async () => [reception, lobby, archive]),
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
		me: { id: 1, name: 'Admin', username: 'admin' },
		preload: () => {}
	}
}));

const { default: ConversationList } = await import('../../../src/lib/chat/ConversationList.svelte');

beforeAll(() => {
	setStrings('en', 'ltr', {
		chat: {
			section_support: 'Support',
			section_team: 'Team',
			filter_open: 'Open',
			filter_mine: 'Mine',
			filter_closed: 'Closed',
			inbox_filters: 'Inbox filters',
			unassigned: 'Waiting for someone',
			new_support: 'Write to a computer',
			search_support: 'Find a computer'
		}
	} as never);
});

beforeEach(() => {
	open = [];
	closed = [];
});

function show(onopen: (conv: string) => void = () => {}) {
	render(ConversationList, {
		section: 'support',
		filter: 'open',
		active: null,
		onsection: () => {},
		onfilter: () => {},
		onopen,
		onnew: () => {},
		onperson: () => {}
	});
}

describe('the Support list', () => {
	test('a filter names its count only when it has one', async () => {
		open = [conv(reception)];
		show();
		const filters = screen.getByRole('group', { name: 'Inbox filters' });
		expect(await within(filters).findByRole('button', { name: 'Open (1)' })).toBeTruthy();
		expect(within(filters).getByRole('button', { name: 'Mine' })).toBeTruthy();
	});

	test('"Waiting for someone" shows only while the computer waits for an answer', async () => {
		open = [conv(reception, { waiting_since: 1 }), conv(lobby)];
		show();
		const waiting = await screen.findByRole('button', { name: /reception-01/ });
		expect(waiting).toHaveTextContent('Waiting for someone');
		expect(screen.getByRole('button', { name: /reception-02/ })).not.toHaveTextContent('Waiting for someone');
	});

	test('a search lists the computers without a conversation, and opens one', async () => {
		open = [conv(reception)];
		const opened: string[] = [];
		show((c) => opened.push(c));
		await fireEvent.input(screen.getByRole('searchbox', { name: 'Find a computer' }), {
			target: { value: 'reception' }
		});
		const fresh = await screen.findByRole('list', { name: 'Write to a computer' });
		expect(within(fresh).queryByText('reception-01')).toBeNull();
		expect(within(fresh).getByText('reception-02')).toBeTruthy();
		await fireEvent.click(within(fresh).getByRole('button', { name: /reception-03/ }));
		expect(opened).toEqual(['900100003']);
	});

	test('a search with nothing open lists the closed conversations that match', async () => {
		closed = [conv(archive, { state: 'closed' })];
		show();
		await fireEvent.input(screen.getByRole('searchbox', { name: 'Find a computer' }), {
			target: { value: 'reception' }
		});
		const old = await screen.findByRole('list', { name: 'Closed' });
		expect(within(old).getByText('reception-03')).toBeTruthy();
		const fresh = screen.getByRole('list', { name: 'Write to a computer' });
		expect(within(fresh).queryByText('reception-03')).toBeNull();
	});
});
