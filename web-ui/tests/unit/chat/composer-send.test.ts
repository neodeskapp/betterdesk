/**
 * The composer (A242): Send is a button with its word, the field is named
 * and says who reads it, Enter sends, and onsend runs once after a new
 * message went out, not when it couldn't be sent and not after an edit.
 */
import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, test, vi } from 'vitest';
import { chat } from '../../../src/lib/chat/client.svelte.ts';
import Composer from '../../../src/lib/chat/Composer.svelte';
import type { Message } from '../../../src/lib/chat/types.ts';

vi.mock('$app/paths', () => ({ resolve: (p: string) => '/app' + (p || '/'), asset: (p: string) => '/' + p }));

const mine: Message = {
	id: '1001',
	conversation_id: 'c1',
	from: { kind: 'user', id: '1', name: 'Admin' },
	text: 'Old text',
	reply_to: null,
	attachments: [],
	created_at: Date.UTC(2026, 10, 26, 9, 0),
	edited_at: null,
	deleted: false,
	reactions: {}
};

function show(editing: Message | null = null) {
	const onsend = vi.fn();
	render(Composer, {
		props: {
			conv: 'c1',
			target: 'Ana',
			replyTo: null,
			editing,
			oncancel: () => {},
			onmanagecanned: () => {},
			onsend
		}
	});
	return onsend;
}

const field = () => screen.getByRole('textbox', { name: 'chat.message_to' });

afterEach(() => vi.restoreAllMocks());

describe('chat composer', () => {
	test('Send sends the message and calls onsend once', async () => {
		const sendMessage = vi.spyOn(chat, 'sendMessage').mockReturnValue(true);
		const onsend = show();
		const user = userEvent.setup();
		const send = screen.getByRole('button', { name: 'chat.send' });
		expect(send).toBeDisabled();
		await user.type(field(), 'Hello');
		await user.click(send);
		expect(sendMessage).toHaveBeenCalledWith('c1', 'Hello', { replyTo: undefined, attachments: [] });
		expect(onsend).toHaveBeenCalledTimes(1);
		expect(field()).toHaveValue('');
	});

	test('Enter sends too; a message that could not go out does not call onsend', async () => {
		const sendMessage = vi.spyOn(chat, 'sendMessage').mockReturnValue(true);
		const onsend = show();
		const user = userEvent.setup();
		await user.type(field(), 'First{Enter}');
		expect(sendMessage).toHaveBeenCalledTimes(1);
		expect(onsend).toHaveBeenCalledTimes(1);

		sendMessage.mockReturnValue(false);
		await user.type(field(), 'Second{Enter}');
		expect(sendMessage).toHaveBeenCalledTimes(2);
		expect(onsend).toHaveBeenCalledTimes(1);
	});

	test('saving an edit does not call onsend', async () => {
		const sendMessage = vi.spyOn(chat, 'sendMessage').mockReturnValue(true);
		const edit = vi.spyOn(chat, 'edit').mockReturnValue(true);
		const onsend = show(mine);
		const user = userEvent.setup();
		await user.clear(field());
		await user.type(field(), 'New text');
		await user.click(screen.getByRole('button', { name: 'chat.save_edit' }));
		expect(edit).toHaveBeenCalledWith('1001', 'New text');
		expect(sendMessage).not.toHaveBeenCalled();
		expect(onsend).not.toHaveBeenCalled();
	});
});
