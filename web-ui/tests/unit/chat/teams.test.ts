/**
 * Teams in Team chat: a team's id opens in the Team section, and the Team
 * list shows the teams one is in under their own heading, before the
 * direct and group conversations.
 */
import { render, screen, within } from '@testing-library/svelte';
import { beforeAll, describe, expect, test, vi } from 'vitest';
import { setStrings } from '../../../src/lib/i18n/i18n.svelte.ts';
import type { TeamItem } from '../../../src/lib/chat/types.ts';

const person = { id: 2, name: 'Ben', username: 'ben', role: 'operator', status: 'online' as const };

function item(id: string, kind: TeamItem['kind'], title = ''): TeamItem {
	return {
		id,
		kind,
		state: 'open',
		title,
		last_message_id: '0',
		last_message_at: 0,
		people: [person],
		last_message: null,
		unread: 0
	};
}

vi.mock('../../../src/lib/chat/api.ts', () => ({
	loadInbox: vi.fn(),
	loadTeam: vi.fn(async () => ({
		conversations: [item('dm:1:2', 'dm'), item('team:1a2b3c4d', 'team', 'Service desk')],
		people: [],
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
			member_count: '{count} members'
		}
	} as never);
});

describe('teams', () => {
	test('a team opens in the Team section', () => {
		expect(sectionOf('team:1a2b3c4d')).toBe('team');
		expect(sectionOf('group:1a2b3c4d')).toBe('team');
		expect(sectionOf('900100001')).toBe('support');
	});

	test('the Team list shows teams first, under their own heading', async () => {
		render(ConversationList, {
			section: 'team',
			filter: 'open',
			active: null,
			onsection: () => {},
			onfilter: () => {},
			onopen: () => {},
			onnew: () => {},
			onperson: () => {}
		});
		const teams = await screen.findByRole('list', { name: 'Teams' });
		expect(within(teams).getByText('Service desk')).toBeTruthy();
		expect(within(teams).queryByText('Ben')).toBeNull();
		const rest = screen.getByRole('list', { name: 'Conversations' });
		expect(within(rest).getByText('Ben')).toBeTruthy();
		expect(teams.compareDocumentPosition(rest) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
	});
});
