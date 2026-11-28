/**
 * Teams in Team chat: a team's id opens in the Team section, and the Team
 * list shows the teams one is in under their own heading, before the
 * direct and group conversations. A direct conversation without a message
 * is no conversation yet: its colleague stays under Colleagues, unless it
 * is the open one.
 */
import { render, screen, within } from '@testing-library/svelte';
import { beforeAll, describe, expect, test, vi } from 'vitest';
import { setStrings } from '../../../src/lib/i18n/i18n.svelte.ts';
import type { Person, TeamItem } from '../../../src/lib/chat/types.ts';

const person = { id: 2, name: 'Ben', username: 'ben', role: 'operator', status: 'online' as const };
const cleo: Person = { id: 3, name: 'Cleo', username: 'cleo', role: 'operator', status: 'away' };

function item(
	id: string,
	kind: TeamItem['kind'],
	title = '',
	people: Person[] = [person],
	said = ''
): TeamItem {
	return {
		id,
		kind,
		state: 'open',
		title,
		last_message_id: said ? '1' : '0',
		last_message_at: said ? 1 : 0,
		people,
		last_message: said ? { text: said, from_kind: 'user', from_name: people[0]!.name, at: 1 } : null,
		unread: 0
	};
}

vi.mock('../../../src/lib/chat/api.ts', () => ({
	loadInbox: vi.fn(),
	loadTeam: vi.fn(async () => ({
		conversations: [
			item('dm:1:2', 'dm', '', [person], 'hello'),
			item('dm:1:3', 'dm', '', [cleo]),
			item('team:1a2b3c4d', 'team', 'Service desk')
		],
		people: [person, cleo],
		old: []
	})),
	searchMessages: vi.fn(async () => ({ messages: [], conversations: {} })),
	loadServerSettings: vi.fn()
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

const { sectionOf } = await import('../../../src/lib/chat/panel.svelte.ts');
const { default: ConversationList } = await import('../../../src/lib/chat/ConversationList.svelte');

beforeAll(() => {
	setStrings('en', 'ltr', {
		chat: {
			section_support: 'Support',
			section_team: 'Team',
			teams: 'Teams',
			conversations: 'Conversations',
			colleagues: 'Colleagues',
			member_count: '{count} members'
		}
	} as never);
});

function show(active: string | null = null) {
	render(ConversationList, {
		section: 'team',
		filter: 'open',
		active,
		onsection: () => {},
		onfilter: () => {},
		onopen: () => {},
		onnew: () => {},
		onperson: () => {}
	});
}

describe('teams', () => {
	test('a team opens in the Team section', () => {
		expect(sectionOf('team:1a2b3c4d')).toBe('team');
		expect(sectionOf('group:1a2b3c4d')).toBe('team');
		expect(sectionOf('900100001')).toBe('support');
	});

	test('the Team list shows teams first, under their own heading', async () => {
		show();
		const teams = await screen.findByRole('list', { name: 'Teams' });
		expect(within(teams).getByText('Service desk')).toBeTruthy();
		expect(within(teams).queryByText('Ben')).toBeNull();
		const rest = screen.getByRole('list', { name: 'Conversations' });
		expect(within(rest).getByText('Ben')).toBeTruthy();
		expect(teams.compareDocumentPosition(rest) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
	});

	test('a direct conversation without a message leaves its colleague under Colleagues', async () => {
		show();
		const colleagues = await screen.findByRole('list', { name: 'Colleagues' });
		expect(within(colleagues).getByText('Cleo')).toBeTruthy();
		expect(within(colleagues).queryByText('Ben')).toBeNull();
		expect(within(screen.getByRole('list', { name: 'Conversations' })).queryByText('Cleo')).toBeNull();
	});

	test('the open direct conversation shows even without a message', async () => {
		show('dm:1:3');
		const rest = await screen.findByRole('list', { name: 'Conversations' });
		expect(within(rest).getByText('Cleo')).toBeTruthy();
		expect(screen.queryByRole('list', { name: 'Colleagues' })).toBeNull();
	});
});
