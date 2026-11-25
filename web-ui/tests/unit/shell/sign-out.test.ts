/**
 * Signing out from the console ends the remote sessions it holds, and the
 * question says how many are open (L479): the sessions live above the
 * pages, so nothing else would close them before the page leaves.
 */
import { beforeEach, describe, expect, test, vi } from 'vitest';

const confirmed: { message: string }[] = [];
vi.mock('#lib/ui/dialogs.svelte.ts', () => ({
	confirm: vi.fn(async (o: { message: string }) => {
		confirmed.push(o);
		return true;
	})
}));
vi.mock('#lib/ui/toasts.svelte.ts', () => ({ toast: { error: vi.fn() } }));
vi.mock('../../../src/lib/shell/api.ts', () => ({ logout: vi.fn(async () => {}) }));
vi.mock('../../../src/lib/shell/session-guard.svelte.ts', () => ({
	broadcastExpired: vi.fn(),
	stopGuard: vi.fn()
}));
const sessions = { count: 0, teardown: vi.fn() };
vi.mock('#lib/remote/console-sessions.svelte.ts', () => ({ consoleSessions: sessions }));

const { signOut } = await import('../../../src/lib/shell/sign-out.ts');

describe('sign-out', () => {
	beforeEach(() => {
		confirmed.length = 0;
		sessions.teardown.mockClear();
		vi.stubGlobal('location', { ...window.location, assign: vi.fn() });
	});

	test('with open remote sessions it says so and ends them', async () => {
		sessions.count = 2;
		await signOut();
		expect(confirmed[0]!.message).toMatch(/Your open remote sessions end too \(2\)/);
		expect(sessions.teardown).toHaveBeenCalledOnce();
		expect(location.assign).toHaveBeenCalledWith('/login');
	});

	test('without sessions the question stays as it was', async () => {
		sessions.count = 0;
		await signOut();
		expect(confirmed[0]!.message).not.toMatch(/remote sessions/);
	});
});
