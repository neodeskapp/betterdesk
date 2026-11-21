/**
 * The chat page sends what is typed as a message, also text that starts
 * with /restart or another of the old quick commands: nothing on the
 * server runs them, so the page neither confirms nor reports them.
 */
import { fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, expect, test, vi } from 'vitest';
import { session } from '../../../src/lib/state/session.svelte.ts';

const live = vi.hoisted(() => ({
	sent: [] as Record<string, unknown>[],
	handlers: null as null | { onmessage(d: unknown): void; onstatus(s: string): void }
}));

vi.mock('$app/paths', () => ({ resolve: (p: string) => '/app' + (p || '/'), asset: (p: string) => '/' + p }));
vi.mock('../../../src/lib/state/live.ts', () => ({
	socket: (_path: string, handlers: typeof live.handlers) => {
		live.handlers = handlers;
		return {
			send: (d: Record<string, unknown>) => {
				live.sent.push(d);
				return true;
			},
			close: () => {}
		};
	}
}));
vi.mock('../../../src/lib/api/chat.ts', () => ({
	MAX_CHAT_FILE: 10 * 1024 * 1024,
	listAvatars: vi.fn(async () => []),
	searchChat: vi.fn(async () => []),
	uploadChatFile: vi.fn()
}));

afterEach(() => {
	session.user = null;
	session.permissions = [];
});

test('a message that starts with /restart goes to the device conversation as text', async () => {
	session.user = { id: 1, username: 'demo_ui_admin', role: 'admin' } as typeof session.user;
	session.permissions = ['chat.access'];
	const Page = (await import('../../../src/routes/(console)/chat/+page.svelte')).default;
	render(Page);
	live.handlers!.onstatus('open');
	live.handlers!.onmessage({
		type: 'contacts',
		contacts: [{ id: '900100012', name: 'Office PC', online: true, role: 'device' }]
	});
	await fireEvent.click(await screen.findByText('Office PC'));
	const box = await screen.findByRole('textbox', { name: /message/i });
	await fireEvent.input(box, { target: { value: '/restart now' } });
	await fireEvent.keyDown(box, { key: 'Enter' });
	expect(live.sent.filter((f) => f.type === 'quick_command')).toEqual([]);
	expect(live.sent.find((f) => f.type === 'message')).toMatchObject({
		text: '/restart now',
		conversation_id: '900100012'
	});
	expect(screen.queryByRole('dialog')).toBeNull();
	expect(screen.queryByText(/Command sent/)).toBeNull();
});
