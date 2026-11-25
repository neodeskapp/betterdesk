/**
 * "Chat with computers" (A259): while an admin has it off, the Support
 * list says so in a plain line and where to turn it on; with it on there
 * is no line. Team chat is not touched.
 */
import { render, screen } from '@testing-library/svelte';
import { beforeAll, describe, expect, test, vi } from 'vitest';
import { setStrings } from '../../../src/lib/i18n/i18n.svelte.ts';

let computers = true;

vi.mock('../../../src/lib/chat/api.ts', () => ({
	loadInbox: vi.fn(async () => ({
		conversations: [],
		counts: { open: 0, mine: 0, closed: 0, unassigned: 0 }
	})),
	loadTeam: vi.fn(async () => ({ conversations: [], people: [], old: [] })),
	searchMessages: vi.fn(async () => ({ messages: [], conversations: {} })),
	loadServerSettings: vi.fn(async () => ({ mail_after_minutes: 15, mail_preview: true, computers }))
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

beforeAll(() => {
	setStrings('en', 'ltr', {
		chat: {
			computers_off: 'Chat with computers is off.',
			empty_support: 'No conversations yet.',
			section_support: 'Support',
			section_team: 'Team'
		}
	} as never);
});

function show(section: 'support' | 'team') {
	render(ConversationList, {
		section,
		filter: 'open',
		active: null,
		onsection: () => {},
		onfilter: () => {},
		onopen: () => {},
		onnew: () => {},
		onperson: () => {}
	});
}

describe('chat with computers off', () => {
	test('the Support list says that chat with computers is off', async () => {
		computers = false;
		show('support');
		expect(await screen.findByText('Chat with computers is off.')).toBeTruthy();
	});

	test('with it on there is no line', async () => {
		computers = true;
		show('support');
		await screen.findByText('No conversations yet.');
		expect(screen.queryByText('Chat with computers is off.')).toBeNull();
	});
});
