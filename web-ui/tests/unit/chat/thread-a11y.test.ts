/**
 * The chat thread for screen readers (WCAG 4.1.3, 1.3.1): the log holds
 * only messages, a new message from someone else is announced once as
 * "<sender>: <text>" without the names of its buttons, loading older
 * messages marks the log busy and says nothing, and the typing line is
 * not a live region.
 */
import { render, screen, within } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { flushSync } from 'svelte';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import Announcer from '../../../src/lib/ui/Announcer.svelte';
import { resetAnnouncer } from '../../../src/lib/ui/announce.svelte.ts';
import { chat } from '../../../src/lib/chat/client.svelte.ts';
import type { Message } from '../../../src/lib/chat/types.ts';

vi.mock('$app/paths', () => ({ resolve: (p: string) => '/app' + (p || '/'), asset: (p: string) => '/' + p }));
vi.setConfig({ testTimeout: 30_000 });

const CONV = 'c1';
let seq = 0;
function msg(from: { id: string; name: string; kind?: 'user' | 'device' }, text: string): Message {
	seq++;
	return {
		id: String(1000 + seq),
		conversation_id: CONV,
		from: { kind: from.kind ?? 'user', id: from.id, name: from.name },
		text,
		reply_to: null,
		attachments: [],
		created_at: Date.UTC(2026, 10, 26, 9, seq),
		edited_at: null,
		deleted: false,
		reactions: {}
	};
}
const ANA = { id: '9', name: 'Ana' };
const ME = { id: '1', name: 'Admin' };

function setThread(messages: Message[], more = false, loaded = true) {
	chat.threads = { ...chat.threads, [CONV]: { messages, loaded, more, reads: {} } };
	flushSync();
}

async function show() {
	const Thread = (await import('../../../src/lib/chat/Thread.svelte')).default;
	render(Announcer);
	render(Thread, { props: { conv: CONV, target: 'Ana', writable: false, onmanagecanned: () => {} } });
}

const polite = () => document.querySelector('[data-announcer="polite"]')!;
const log = () => screen.getByRole('log');

beforeEach(() => {
	vi.useFakeTimers({ shouldAdvanceTime: true });
	chat.me = { id: 1, name: 'Admin', username: 'admin' };
	chat.status = 'open';
	Element.prototype.scrollIntoView = vi.fn();
});
afterEach(() => {
	resetAnnouncer();
	chat.threads = {};
	chat.typing = {};
	chat.me = null;
	chat.status = 'idle';
	vi.useRealTimers();
});

describe('chat thread for screen readers', () => {
	test('the log holds the messages only: Earlier messages and the state lines are outside it', async () => {
		setThread([msg(ANA, 'Hello')], true);
		await show();
		const earlier = screen.getByRole('button', { name: 'chat.earlier' });
		expect(log()).not.toContainElement(earlier);
		expect(within(log()).getByText('Hello')).toBeInTheDocument();
		// The scroll box is a named group, not a nameless div with a label
		const box = document.querySelector('.thread-scroll')!;
		expect(box).toHaveAttribute('role', 'group');
		expect(box).toHaveAccessibleName('chat.messages');
	});

	test('while the conversation loads, the loading line is not in the log and the log is busy', async () => {
		setThread([], false, false);
		await show();
		const loading = screen.getByText('common.loading');
		expect(log()).not.toContainElement(loading);
		expect(log()).toHaveAttribute('aria-busy', 'true');
	});

	test('a new message from someone else is announced once as sender and text', async () => {
		setThread([msg(ANA, 'Hello')]);
		await show();
		// The log itself is not live: it would read the buttons of each message too
		expect(log()).toHaveAttribute('aria-live', 'off');
		await vi.advanceTimersByTimeAsync(200);
		expect(polite().textContent).toBe('');

		setThread([...chat.thread(CONV).messages, msg(ANA, 'Is anyone there?')]);
		await vi.advanceTimersByTimeAsync(200);
		expect(polite().textContent).toBe('Ana: Is anyone there?');

		// My own message is not read back to me
		resetAnnouncer();
		setThread([...chat.thread(CONV).messages, msg(ME, 'Yes, here')]);
		await vi.advanceTimersByTimeAsync(200);
		expect(polite().textContent).toBe('');
	});

	test('older messages from Earlier messages are not announced; the log is busy until they arrive', async () => {
		const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
		const newest = msg(ANA, 'Newest');
		setThread([newest], true);
		await show();
		await user.click(screen.getByRole('button', { name: 'chat.earlier' }));
		expect(log()).toHaveAttribute('aria-busy', 'true');
		setThread([msg(ANA, 'Old one'), msg(ANA, 'Old two'), newest], false);
		await vi.advanceTimersByTimeAsync(200);
		expect(polite().textContent).toBe('');
		expect(log()).not.toHaveAttribute('aria-busy', 'true');
	});

	test('the typing line is not a live region; message actions are a group, not a toolbar', async () => {
		setThread([msg(ANA, 'Hello')]);
		await show();
		chat.typing = { [CONV]: { from: { kind: 'user', id: '9', name: 'Ana' }, until: Date.now() + 4000 } };
		await vi.advanceTimersByTimeAsync(1100);
		const line = screen.getByText('chat.typing_name');
		expect(line.closest('[aria-live]')).toBeNull();
		expect(screen.queryByRole('toolbar')).toBeNull();
		expect(screen.getByRole('group', { name: 'chat.message_actions' })).toBeInTheDocument();
	});
});
