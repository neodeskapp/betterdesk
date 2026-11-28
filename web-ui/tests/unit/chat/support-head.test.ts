/**
 * The head of a support conversation and what sits around it (A242): Take
 * when nobody has it and Take over when a colleague does, no tag for a
 * waiting conversation, Connect as a button only while the computer is
 * online, the help request option in the close dialog only when there is
 * one, the warning above the composer only when the person can't be
 * reached, and the first answer to a conversation nobody has takes it.
 */
import { cleanup, fireEvent, render, screen, within } from '@testing-library/svelte';
import { tick } from 'svelte';
import { afterEach, beforeAll, beforeEach, describe, expect, test, vi } from 'vitest';
import { setStrings } from '../../../src/lib/i18n/i18n.svelte.ts';
import type { ConversationDetail } from '../../../src/lib/chat/api.ts';
import type { Conversation, Device } from '../../../src/lib/chat/types.ts';

const apiMock = vi.hoisted(() => ({
	assign: vi.fn(),
	closeConversation: vi.fn(),
	reopenConversation: vi.fn()
}));
vi.mock('../../../src/lib/chat/api.ts', async (orig) => ({ ...(await orig<object>()), ...apiMock }));

const chatMock = vi.hoisted(() => ({
	may: { support: true, team: true, assign: true, delete_any: false },
	me: { id: 1, name: 'Admin', username: 'admin' } as { id: number; name: string; username: string } | null,
	status: 'open',
	listVersion: 0,
	presence: {},
	deviceChat: {} as Record<string, boolean>,
	details: {} as Record<string, unknown>,
	onRemoved: () => () => {},
	onConversation: () => () => {},
	loadDetail: async () => {},
	setDetail(id: string, d: unknown) {
		this.details[id] = d;
	}
}));
vi.mock('../../../src/lib/chat/client.svelte.ts', () => ({ chat: chatMock }));

const perms = vi.hoisted(() => new Set<string>());
vi.mock('../../../src/lib/state/session.svelte.ts', async (orig) => ({
	...(await orig<object>()),
	can: (p: string) => perms.has(p),
	canWrite: (p: string) => perms.has(p)
}));

const connectMock = vi.hoisted(() => vi.fn());
vi.mock('../../../src/lib/devices/actions.svelte.ts', async (orig) => ({
	...(await orig<object>()),
	connectInBrowser: connectMock
}));

vi.mock('../../../src/lib/chat/Thread.svelte', async () => ({
	default: (await import('./ThreadStub.svelte')).default
}));
vi.mock('../../../src/lib/chat/ConversationList.svelte', async () => ({
	default: (await import('./ThreadStub.svelte')).default
}));

const { default: ChatView } = await import('../../../src/lib/chat/ChatView.svelte');

const CONV = '900100001';

function device(over: Partial<Device> = {}): Device {
	return {
		id: CONV,
		name: 'PC-Lisa',
		hostname: 'lisa-pc',
		owner: '',
		folder: '',
		platform: 'windows',
		online: true,
		chat_connected: true,
		agent_chat: false,
		...over
	};
}

function conversation(over: Partial<Conversation> = {}): Conversation {
	return {
		id: CONV,
		kind: 'support',
		state: 'open',
		last_message_id: '0',
		last_message_at: 0,
		...over
	} as Conversation;
}

function show(c: Partial<Conversation> = {}, d: Partial<Device> = {}) {
	const detail = {
		conversation: conversation(c),
		device: device(d),
		assignable: []
	} as unknown as ConversationDetail;
	chatMock.details[CONV] = detail;
	return render(ChatView, { section: 'support', filter: 'open', conv: CONV, onchange: () => {} });
}

beforeAll(() => {
	setStrings('en', 'ltr', {
		chat: {
			take: 'Take',
			take_over: 'Take over',
			connect: 'Connect',
			close: 'Close',
			online: 'Online',
			offline: 'Offline',
			unassigned: 'Waiting for someone',
			assigned_you: 'You have it',
			assigned_to: '{name} has it',
			close_note: 'Note (optional)',
			close_resolve_help: "Also mark the computer's open help request as resolved",
			reach_none: 'Their chat window is closed.',
			reach_app: 'The person sees your messages in the NeoDesk app now.'
		}
	} as never);
});

beforeEach(() => {
	perms.clear();
	perms.add('chat.support');
	chatMock.details = {};
	chatMock.me = { id: 1, name: 'Admin', username: 'admin' };
	chatMock.may.assign = true;
	connectMock.mockReset();
	for (const fn of Object.values(apiMock)) fn.mockReset();
	apiMock.assign.mockImplementation(async (id: string, who: number) =>
		conversation({ id, assignee_id: who, assignee_name: 'admin' })
	);
	apiMock.closeConversation.mockImplementation(async () => ({
		conversation: conversation({ state: 'closed' }),
		resolved: 0
	}));
});

afterEach(cleanup);

describe('who has it', () => {
	test('nobody: Take, and no tag next to the name', () => {
		show();
		expect(screen.getByRole('button', { name: 'Take' })).toBeInTheDocument();
		expect(screen.queryByRole('button', { name: 'Take over' })).toBeNull();
		expect(screen.queryByText('Waiting for someone')).toBeNull();
	});

	test('a colleague: Take over, and their name as plain text', () => {
		show({ assignee_id: 2, assignee_name: 'Ben' });
		expect(screen.getByRole('button', { name: 'Take over' })).toBeInTheDocument();
		expect(screen.queryByRole('button', { name: 'Take' })).toBeNull();
		expect(screen.getByText('Ben has it')).toBeInTheDocument();
	});
});

describe('Connect', () => {
	test('a button while the computer is online', async () => {
		perms.add('device.connect');
		show();
		await fireEvent.click(screen.getByRole('button', { name: 'Connect' }));
		expect(connectMock).toHaveBeenCalledWith({ id: CONV, hostname: 'lisa-pc' });
	});

	test('gone while it is offline', () => {
		perms.add('device.connect');
		show({}, { online: false });
		expect(screen.queryByRole('button', { name: 'Connect' })).toBeNull();
		expect(screen.getByText('Offline')).toBeInTheDocument();
	});

	test('gone without the permission', () => {
		show();
		expect(screen.queryByRole('button', { name: 'Connect' })).toBeNull();
	});
});

describe('closing', () => {
	test('without a help request: no option for it, and nothing to resolve', async () => {
		show({ assignee_id: 1, assignee_name: 'admin' });
		await fireEvent.click(screen.getByRole('button', { name: 'Close' }));
		const dialog = screen.getByRole('dialog');
		expect(within(dialog).queryByRole('checkbox')).toBeNull();
		await fireEvent.input(within(dialog).getByRole('textbox', { name: 'Note (optional)' }), {
			target: { value: 'driver reinstalled' }
		});
		await fireEvent.click(within(dialog).getAllByRole('button', { name: 'Close' }).at(-1)!);
		expect(apiMock.closeConversation).toHaveBeenCalledWith(CONV, 'driver reinstalled', false);
	});

	test('with a help request: the option shows and is sent', async () => {
		show({ assignee_id: 1, assignee_name: 'admin', help_request_id: 7 });
		await fireEvent.click(screen.getByRole('button', { name: 'Close' }));
		const dialog = screen.getByRole('dialog');
		expect(within(dialog).getByRole('checkbox', { name: /help request/ })).toBeChecked();
		await fireEvent.click(within(dialog).getAllByRole('button', { name: 'Close' }).at(-1)!);
		expect(apiMock.closeConversation).toHaveBeenCalledWith(CONV, '', true);
	});
});

describe('whether the message reaches them', () => {
	test('nothing while their chat window is open', () => {
		show();
		expect(screen.queryByText('Their chat window is closed.')).toBeNull();
		expect(screen.queryByText('The person sees your messages in the NeoDesk app now.')).toBeNull();
	});

	test('a warning while nothing takes it', () => {
		show({}, { chat_connected: false, agent_chat: false });
		expect(screen.getByRole('status')).toHaveTextContent('Their chat window is closed.');
	});
});

describe('the first answer', () => {
	test('takes a conversation nobody has', async () => {
		show();
		await fireEvent.click(screen.getByRole('button', { name: 'Send' }));
		await tick();
		expect(apiMock.assign).toHaveBeenCalledWith(CONV, 1);
	});

	test("leaves one a colleague has, a closed one, and one when the account can't assign", async () => {
		const { unmount } = show({ assignee_id: 2, assignee_name: 'Ben' });
		await fireEvent.click(screen.getByRole('button', { name: 'Send' }));
		unmount();
		const closed = show({ state: 'closed' });
		await fireEvent.click(screen.getByRole('button', { name: 'Send' }));
		closed.unmount();
		chatMock.may.assign = false;
		show();
		await fireEvent.click(screen.getByRole('button', { name: 'Send' }));
		await tick();
		expect(apiMock.assign).not.toHaveBeenCalled();
	});
});
