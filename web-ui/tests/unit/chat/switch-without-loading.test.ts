/**
 * Switching between conversations (A242): a row the pointer rests on loads
 * its conversation ahead of the click and only once, so it opens with its
 * header and messages and no "Loading" line; "Loading" shows only when a
 * conversation takes a while; and the facts loaded after a change win over
 * an older answer still on its way.
 */
import { fireEvent, render, screen, within } from '@testing-library/svelte';
import { tick } from 'svelte';
import { beforeAll, beforeEach, describe, expect, test, vi } from 'vitest';
import { setStrings } from '../../../src/lib/i18n/i18n.svelte.ts';

const sent: Record<string, unknown>[] = [];
let onstatus: ((s: string) => void) | null = null;
let onmessage: ((data: unknown) => void) | null = null;

vi.mock('../../../src/lib/state/live.ts', async (orig) => ({
	...(await orig<object>()),
	socket: (_path: string, opts: { onmessage: (d: unknown) => void; onstatus: (s: string) => void }) => {
		onstatus = opts.onstatus;
		onmessage = opts.onmessage;
		return {
			send: (f: Record<string, unknown>) => {
				sent.push(f);
				return true;
			},
			close: () => {}
		};
	}
}));

const device = (id: string, name: string) => ({
	id,
	name,
	hostname: name,
	owner: '',
	folder: '',
	platform: 'windows',
	online: true,
	chat_connected: true,
	agent_chat: false
});
const conversation = (id: string, state: 'open' | 'closed' = 'open') => ({
	id,
	kind: 'support',
	state,
	last_message_id: '0',
	last_message_at: 0
});

/** loadConversation's calls per conversation, and a way to answer them later */
let detailCalls: Record<string, number> = {};
let answer: (id: string) => Promise<unknown> = async (id) => ({
	conversation: conversation(id),
	device: device(id, `PC-${id}`)
});

vi.mock('../../../src/lib/chat/api.ts', async (orig) => ({
	...(await orig<object>()),
	loadConversation: (id: string) => {
		detailCalls[id] = (detailCalls[id] ?? 0) + 1;
		return answer(id);
	},
	loadInbox: async () => ({
		conversations: [
			{ ...conversation('200'), device: device('200', 'PC-200'), last_message: null, unread: 0 }
		],
		counts: { open: 1, mine: 0, closed: 0, unassigned: 1 }
	}),
	loadTeam: async () => ({ conversations: [], people: [], old: [] }),
	searchMessages: async () => ({ messages: [], conversations: {} }),
	loadServerSettings: async () => ({ mail_after_minutes: 15, mail_preview: true, computers: true }),
	loadMySettings: async () => ({ browser: false, sound: false, mail: false }),
	loadSummary: async () => ({})
}));

const { chat } = await import('../../../src/lib/chat/client.svelte.ts');
const { default: ChatView } = await import('../../../src/lib/chat/ChatView.svelte');

const wait = (ms: number) => new Promise((r) => setTimeout(r, ms));
const histories = (conv: string) =>
	sent.filter((f) => f.type === 'history' && f.conversation_id === conv && f.before === '0').length;

beforeAll(() => {
	setStrings('en', 'ltr', {
		common: { loading: 'Loading…' },
		chat: { connecting: 'Connecting…', no_messages: 'No messages yet.' }
	} as never);
});

beforeEach(() => {
	chat.stop();
	sent.length = 0;
	detailCalls = {};
	answer = async (id) => ({ conversation: conversation(id), device: device(id, `PC-${id}`) });
	chat.start();
	onstatus?.('open');
	onmessage?.({
		type: 'welcome',
		me: { id: 1, name: 'Admin', username: 'admin' },
		may: { support: true, team: true, assign: true, delete_any: false }
	});
});

function show(conv: string | null) {
	const view = render(ChatView, {
		section: 'support',
		filter: 'open',
		conv,
		onchange: (w: { conv?: string | null }) => {
			if (w.conv !== undefined) void view.rerender({ conv: w.conv });
		}
	});
	return view;
}

describe('switching conversations', () => {
	test('a row pointed at loads its conversation once, and it opens without a Loading line', async () => {
		const view = show(null);
		const row = await screen.findByRole('button', { name: /PC-200/ });

		fireEvent.pointerEnter(row);
		await wait(150);
		expect(histories('200')).toBe(1);
		expect(detailCalls['200']).toBe(1);

		onmessage?.({
			type: 'history',
			conversation_id: '200',
			before: '0',
			more: false,
			reads: [],
			messages: [
				{
					id: '1',
					conversation_id: '200',
					from: { kind: 'device', id: '200', name: 'PC-200' },
					text: 'The printer is gone',
					reply_to: null,
					attachments: [],
					created_at: Date.now(),
					edited_at: null,
					deleted: false,
					reactions: {}
				}
			]
		});
		await tick();

		const { container } = view;
		fireEvent.click(row);
		await tick();
		await tick();
		expect(
			within(container.querySelector<HTMLElement>('.chat-pane-thread')!).queryByText('Loading…')
		).toBeNull();
		expect(screen.getByRole('heading', { name: 'PC-200' })).toBeInTheDocument();
		expect(screen.getByText('The printer is gone')).toBeInTheDocument();
		// Opening it didn't ask for the same page again
		expect(histories('200')).toBe(1);
	});

	test('a conversation that takes a while says Loading only after a moment', async () => {
		answer = () => new Promise(() => {});
		const { container } = show('300');
		const pane = within(container.querySelector<HTMLElement>('.chat-pane-thread')!);
		await tick();
		expect(pane.queryByText('Loading…')).toBeNull();
		await wait(450);
		// The header and the messages
		expect(pane.getAllByText('Loading…')).toHaveLength(2);
	});

	test('the facts loaded after a change win over an older answer still on its way', async () => {
		const pending: ((d: unknown) => void)[] = [];
		answer = () => new Promise((ok) => pending.push(ok));
		const older = chat.loadDetail('400');
		const fresh = chat.loadDetail('400', true);
		pending[1]?.({ conversation: conversation('400', 'closed'), device: device('400', 'PC-400') });
		await fresh;
		pending[0]?.({ conversation: conversation('400', 'open'), device: device('400', 'PC-400') });
		await older;
		expect(chat.details['400']?.conversation.state).toBe('closed');
	});
});
