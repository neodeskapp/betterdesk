/**
 * Signed-in devices (A239): an account may be signed in on several
 * browsers; the account menu lists its own sessions with Sign out per entry
 * and everywhere else, the user's page lists another account's with Sign
 * out per entry and everywhere. "One session per account" on Settings,
 * Sign-in is tested with that tab (settings/signin-tab.test.ts).
 */
import { cleanup, render, screen, waitFor, within } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

vi.mock('#lib/ui/dialogs.svelte.ts', () => ({ confirm: vi.fn(async () => true) }));

const { deviceLabel, limitText } = await import('../../../src/lib/people/sessions.ts');
const SessionsSheet = (await import('../../../src/lib/people/SessionsSheet.svelte')).default;

/** t() of the tests: the key with its values, so a test sees which were used */
const t = (key: string, vars?: Record<string, unknown>) =>
	vars
		? `${key}(${Object.entries(vars)
				.map(([k, v]) => `${k}=${String(v)}`)
				.join(',')})`
		: key;

describe('a session in words', () => {
	test('browser and system from the server, plainly', () => {
		expect(deviceLabel({ browser: 'Firefox', system: 'Windows' }, t)).toBe(
			'sessions.browser_on_system(browser=Firefox,system=Windows)'
		);
		expect(deviceLabel({ browser: 'Safari', system: '' }, t)).toBe('Safari');
		expect(deviceLabel({ browser: '', system: 'Linux' }, t)).toBe('sessions.browser_on(system=Linux)');
		expect(deviceLabel({ browser: '', system: '' }, t)).toBe('sessions.unknown_browser');
	});

	test('the limit as the list says it', () => {
		expect(limitText({ max: 10, source: 'default' }, t)).toBe('sessions.limit_many(max=10)');
		expect(limitText({ max: 1, source: 'setting' }, t)).toBe('sessions.limit_one');
		expect(limitText({ max: 1, source: 'env' }, t)).toBe('sessions.limit_one');
		expect(limitText({ max: 0, source: 'env' }, t)).toBe('sessions.limit_none');
	});
});

type Call = { method: string; path: string; body: string };
let calls: Call[] = [];
let lists: unknown[] = [];

const device = (id: string, over: Record<string, unknown> = {}) => ({
	id,
	current: false,
	browser: 'Firefox',
	system: 'Windows',
	ipAddress: '203.0.113.5',
	userAgent: 'x',
	createdAt: Date.now() - 3_600_000,
	lastActivityAt: Date.now() - 60_000,
	...over
});

beforeEach(() => {
	calls = [];
	lists = [];
	vi.stubGlobal('fetch', async (input: string, init: RequestInit = {}) => {
		const method = init.method ?? 'GET';
		const path = new URL(input, location.origin).pathname;
		calls.push({ method, path, body: String(init.body ?? '') });
		let body: unknown = { success: true };
		if (method === 'GET' && path.endsWith('/sessions')) {
			body = { success: true, data: lists.length > 1 ? lists.shift() : lists[0] };
		} else if (method === 'POST') {
			body = { success: true, ended: 2 };
		}
		return new Response(JSON.stringify(body), { headers: { 'Content-Type': 'application/json' } });
	});
});

afterEach(() => {
	cleanup();
	vi.unstubAllGlobals();
});

const writes = () => calls.filter((c) => c.method !== 'GET').map((c) => `${c.method} ${c.path}`);

describe('the own list', () => {
	test('this browser is marked and has no Sign out; another one is signed out by its entry', async () => {
		const user = userEvent.setup();
		lists = [
			{
				sessions: [device('aaa', { current: true, browser: 'Chrome', system: 'macOS' }), device('bbb')],
				limit: { max: 10, source: 'default' }
			},
			{
				sessions: [device('aaa', { current: true, browser: 'Chrome', system: 'macOS' })],
				limit: { max: 10, source: 'default' }
			}
		];
		render(SessionsSheet, { onclose: () => {} });
		const rows = await screen.findAllByRole('listitem');
		expect(rows).toHaveLength(2);
		const mine = rows.find((r) => within(r).queryByText('sessions.this_browser'));
		const other = rows.find((r) => r !== mine)!;
		expect(mine).toBeTruthy();
		expect(within(mine!).queryByRole('button', { name: /sessions.sign_out/ })).toBeNull();
		expect(within(other).getByText(/203.0.113.5/)).toBeInTheDocument();
		// The address line stays inside its row: the global .facts style (a
		// table of facts that hangs out of the column with a rule on top)
		// must not reach it
		const meta = within(other)
			.getByText(/203.0.113.5/)
			.closest('.row-sub')!;
		expect(meta).not.toBeNull();
		expect(meta.querySelector('.facts')).toBeNull();
		await user.click(within(other).getByRole('button', { name: /sessions.sign_out/ }));
		await waitFor(() => expect(writes()).toEqual(['DELETE /api/users/me/sessions/bbb']));
		await waitFor(() => expect(screen.getAllByRole('listitem')).toHaveLength(1));
	});

	test('Sign out everywhere else ends the others in one request', async () => {
		const user = userEvent.setup();
		lists = [
			{
				sessions: [device('aaa', { current: true }), device('bbb'), device('ccc')],
				limit: { max: 10, source: 'default' }
			}
		];
		render(SessionsSheet, { onclose: () => {} });
		await screen.findAllByRole('listitem');
		await user.click(screen.getByRole('button', { name: 'sessions.sign_out_everywhere_else' }));
		await waitFor(() => expect(writes()).toEqual(['POST /api/users/me/sessions/end-others']));
	});

	test('alone: nothing to sign out, and no button for it', async () => {
		lists = [{ sessions: [device('aaa', { current: true })], limit: { max: 1, source: 'setting' } }];
		render(SessionsSheet, { onclose: () => {} });
		await screen.findAllByRole('listitem');
		expect(screen.getByText('sessions.none_other')).toBeInTheDocument();
		expect(screen.getByText('sessions.limit_one')).toBeInTheDocument();
		expect(screen.queryByRole('button', { name: 'sessions.sign_out_everywhere_else' })).toBeNull();
	});
});

describe('Your account › Signed-in devices', () => {
	test('the page lists the own sessions, signs one out and all the others', async () => {
		const user = userEvent.setup();
		lists = [
			{
				sessions: [device('aaa', { current: true }), device('bbb'), device('ccc')],
				limit: { max: 10, source: 'default' }
			}
		];
		const AccountSessions = (await import('../../../src/lib/account/Sessions.svelte')).default;
		render(AccountSessions);
		const part = await screen.findByRole('region', { name: 'account.sessions' });
		const rows = await within(part).findAllByRole('listitem');
		expect(rows).toHaveLength(3);
		expect(calls[0]?.path).toBe('/api/users/me/sessions');
		await user.click(within(rows[1]!).getByRole('button', { name: /sessions.sign_out/ }));
		await user.click(within(part).getByRole('button', { name: 'sessions.sign_out_everywhere_else' }));
		await waitFor(() =>
			expect(writes()).toEqual([
				'DELETE /api/users/me/sessions/bbb',
				'POST /api/users/me/sessions/end-others'
			])
		);
		await waitFor(() =>
			expect(within(part).getByRole('status')).toHaveTextContent(/sessions.signed_out_count/)
		);
	});
});

describe("another account's list on the user's page", () => {
	test('every entry has Sign out, and Sign out everywhere ends them all', async () => {
		const user = userEvent.setup();
		lists = [{ sessions: [device('aaa'), device('bbb')], limit: { max: 10, source: 'default' } }];
		render(SessionsSheet, { user: { id: 5, username: 'anna' }, onclose: () => {} });
		const rows = await screen.findAllByRole('listitem');
		expect(calls[0]?.path).toBe('/api/users/5/sessions');
		for (const row of rows)
			expect(within(row).getByRole('button', { name: /sessions.sign_out/ })).toBeInTheDocument();
		await user.click(within(rows[0]!).getByRole('button', { name: /sessions.sign_out/ }));
		await user.click(screen.getByRole('button', { name: 'sessions.sign_out_all' }));
		await waitFor(() =>
			expect(writes()).toEqual(['DELETE /api/users/5/sessions/aaa', 'POST /api/users/5/sessions/end-all'])
		);
	});
});
