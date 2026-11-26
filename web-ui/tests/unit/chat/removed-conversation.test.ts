/**
 * A conversation that is gone for this account (a deleted team, or one the
 * account was taken out of): conversation.removed drops its thread and the
 * details the client keeps for it, so reopening its address loads it anew
 * instead of showing what it was.
 */
import { beforeEach, describe, expect, test, vi } from 'vitest';

let onmessage: ((data: unknown) => void) | null = null;

vi.mock('../../../src/lib/state/live.ts', async (orig) => ({
	...(await orig<object>()),
	socket: (_path: string, opts: { onmessage: (d: unknown) => void }) => {
		onmessage = opts.onmessage;
		return { send: () => true, close: () => {} };
	}
}));

vi.mock('../../../src/lib/chat/api.ts', async (orig) => ({
	...(await orig<object>()),
	loadSummary: async () => ({})
}));

const { chat } = await import('../../../src/lib/chat/client.svelte.ts');

const detail = (id: string) =>
	({
		conversation: {
			id,
			kind: 'team',
			state: 'open',
			title: 'Service desk',
			last_message_id: '0',
			last_message_at: 0
		}
	}) as never;

beforeEach(() => {
	chat.stop();
	chat.start();
});

describe('a removed conversation', () => {
	test('loses its cached details and tells the listeners', () => {
		chat.setDetail('team:1a2b3c4d', detail('team:1a2b3c4d'));
		chat.setDetail('team:99887766', detail('team:99887766'));
		const removed: string[] = [];
		const off = chat.onRemoved((id) => removed.push(id));

		onmessage?.({ type: 'conversation.removed', conversation_id: 'team:1a2b3c4d' });
		off();

		expect(chat.details['team:1a2b3c4d']).toBeUndefined();
		expect(chat.details['team:99887766']).toBeDefined();
		expect(removed).toEqual(['team:1a2b3c4d']);
	});
});
