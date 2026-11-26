/**
 * Add a computer and Quick support: what the dialogs send, how the quick
 * support dialog follows its session to Connect, and the session password,
 * which is fetched once and never put into the viewer's address.
 */
import { cleanup, render, screen, waitFor } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import type { SupportSession } from '../../src/lib/api/onboard.ts';
import { session as signedIn } from '../../src/lib/state/session.svelte.ts';
import { inviteBody } from '../../src/lib/onboard/invite.ts';
import {
	canConnect,
	desktopLink,
	go,
	isOver,
	stateKey,
	viewerPath
} from '../../src/lib/onboard/quick-support.ts';

vi.mock('$app/paths', () => ({ resolve: (p: string) => p || '/', asset: (p: string) => '/' + p }));
vi.mock('$app/navigation', () => ({ goto: vi.fn(), pushState: vi.fn(), replaceState: vi.fn() }));

const AddComputerDialog = (await import('../../src/lib/onboard/AddComputerDialog.svelte')).default;
const QuickSupportDialog = (await import('../../src/lib/onboard/QuickSupportDialog.svelte')).default;

describe('request bodies', () => {
	test('an invite sends only what was filled in; an address is mailed at once', () => {
		expect(inviteBody({ name: '  ', folder: 0, email: '', hours: 168, uses: 1 })).toEqual({
			lifetime_hours: 168,
			uses: 1
		});
		expect(
			inviteBody({ name: ' Front desk ', folder: 7, email: ' ana@example.com ', hours: 24, uses: 5 })
		).toEqual({
			name: 'Front desk',
			folder_id: 7,
			email: 'ana@example.com',
			send_email: true,
			lifetime_hours: 24,
			uses: 5
		});
	});
});

describe('session states', () => {
	test('Connect only once the person allowed it and a device is known', () => {
		expect(canConnect({ status: 'allowed', device_id: '123456789' })).toBe(true);
		expect(canConnect({ status: 'allowed', device_id: '' })).toBe(false);
		expect(canConnect({ status: 'asking', device_id: '123456789' })).toBe(false);
		expect(isOver('declined') && isOver('ended') && isOver('expired')).toBe(true);
		expect(isOver('allowed')).toBe(false);
		expect(stateKey({ status: 'ended', ended_by: 'timeout', password_ready: false })).toBe(
			'onboard.support.state_timeout'
		);
		expect(stateKey({ status: 'allowed', ended_by: '', password_ready: false })).toBe(
			'onboard.support.state_allowed_wait'
		);
	});

	test('the desktop link carries the password, the viewer path never does', () => {
		// NeoDesk's own scheme; rustdesk:// only when RustDesk is asked for (L423)
		expect(desktopLink('123 456', 'a&b c')).toBe('neodesk://connection/new/123%20456?password=a%26b%20c');
		expect(desktopLink('123456789', '')).toBe('neodesk://connection/new/123456789');
		expect(desktopLink('123456789', 'pw', 'rustdesk')).toBe(
			'rustdesk://connection/new/123456789?password=pw'
		);
		// Quick support keeps the standalone viewer in its own tab, not the console's
		expect(viewerPath('123456789')).toBe('/remote/123456789?standalone=1');
	});
});

/* ── The dialogs against a fake server ── */

type Call = { method: string; path: string; body: unknown };
let calls: Call[];
let routes: Record<string, (body: unknown) => { status?: number; body: unknown }>;

function session(over: Partial<SupportSession> = {}): SupportSession {
	return {
		id: 'aaaaaaaaaaaaaaaa',
		status: 'waiting',
		name: 'Ana',
		email: '',
		folder_id: null,
		created_by: 'e2e_operator',
		created_at: '2026-11-22T12:00:00Z',
		expires_at: '2026-11-22T13:00:00Z',
		redeemed_at: '',
		consent_at: '',
		ended_at: '',
		ended_by: '',
		end_reason: '',
		device_id: '',
		hostname: '',
		platform: '',
		password_ready: false,
		...over
	};
}

beforeEach(() => {
	calls = [];
	routes = {
		'GET /api/onboard/status': () => ({
			body: { success: true, console: { base: 'https://desk.example.com', https: true }, mail: true }
		}),
		'GET /api/onboard/invites': () => ({ body: { success: true, invites: [] } })
	};
	vi.stubGlobal(
		'fetch',
		vi.fn(async (input: string, init?: RequestInit) => {
			const url = new URL(input, 'http://localhost');
			const method = init?.method ?? 'GET';
			const body = init?.body ? JSON.parse(String(init.body)) : null;
			calls.push({ method, path: url.pathname, body });
			const route = routes[`${method} ${url.pathname}`];
			const out = route ? route(body) : { status: 404, body: { success: false, error: 'nope' } };
			return new Response(JSON.stringify(out.body), {
				status: out.status ?? 200,
				headers: { 'Content-Type': 'application/json' }
			});
		})
	);
});

afterEach(() => {
	cleanup();
	vi.unstubAllGlobals();
	vi.restoreAllMocks();
});

describe('Add a computer', () => {
	test('creates the invite with the choices and shows the link once', async () => {
		routes['POST /api/onboard/invites'] = () => ({
			status: 201,
			body: {
				success: true,
				invite: {
					id: '0123456789abcdef',
					name: 'Front desk',
					folder_id: 7,
					folder_name: 'Office',
					email: '',
					created_by: 'e2e_admin',
					created_at: '2026-11-22T12:00:00Z',
					expires_at: '2026-11-29T12:00:00Z',
					max_uses: 1,
					uses_left: 1
				},
				code: 'ABCD2345',
				display_code: 'ABCD-2345',
				link: 'https://desk.example.com/join/ABCD2345',
				deep_link: 'neodesk://join/desk.example.com/ABCD2345',
				mailed: false
			}
		});
		render(AddComputerDialog, {
			folders: [{ id: 7, name: 'Office', color: '', icon: '' }],
			onclose: () => {}
		});
		const user = userEvent.setup();
		await user.type(screen.getByLabelText('onboard.invite.name'), 'Front desk');
		await user.selectOptions(screen.getByLabelText('onboard.invite.folder'), '7');
		await user.click(screen.getByRole('button', { name: 'onboard.invite.create' }));
		expect(await screen.findByText('ABCD-2345')).toBeInTheDocument();
		const post = calls.find((c) => c.method === 'POST' && c.path === '/api/onboard/invites');
		expect(post?.body).toEqual({ name: 'Front desk', folder_id: 7, lifetime_hours: 168, uses: 1 });
		// Mail is set up: the form to send it is there
		expect(screen.getByRole('button', { name: 'onboard.result.send' })).toBeInTheDocument();
	});
});

describe('Quick support', () => {
	test('follows the session to Connect; the desktop app gets the password fetched once', async () => {
		let state = session();
		routes['POST /api/support/sessions'] = () => ({
			status: 201,
			body: {
				success: true,
				session: state,
				code: 'ABCD2345',
				display_code: 'ABCD-2345',
				link: 'https://desk.example.com/s/ABCD2345',
				deep_link: 'neodesk://support/desk.example.com/ABCD2345',
				mailed: false
			}
		});
		routes['GET /api/support/sessions/aaaaaaaaaaaaaaaa'] = () => ({
			body: { success: true, session: state }
		});
		routes['POST /api/support/sessions/aaaaaaaaaaaaaaaa/password'] = () => ({
			body: { success: true, password: 'pw 1&2' }
		});
		const opened: string[] = [];
		const hrefs: string[] = [];
		vi.spyOn(go, 'tab').mockImplementation((url: string) => void opened.push(url));
		vi.spyOn(go, 'here').mockImplementation((url: string) => void hrefs.push(url));

		render(QuickSupportDialog, { onclose: () => {} });
		const user = userEvent.setup();
		// Opening the dialog makes no link; Make a new link does
		expect(screen.getByRole('textbox', { name: 'ID' })).toBeInTheDocument();
		expect(calls.some((c) => c.method === 'POST')).toBe(false);
		await user.click(screen.getByRole('button', { name: 'onboard.support.again' }));
		expect(await screen.findByText('onboard.support.state_waiting')).toBeInTheDocument();
		expect(screen.queryByRole('textbox', { name: 'ID' })).toBeNull();
		expect(calls.filter((c) => c.method === 'POST' && c.path === '/api/support/sessions')).toHaveLength(1);
		expect(screen.getByRole('button', { name: 'onboard.support.cancel_link' })).toBeInTheDocument();
		expect(screen.queryByRole('button', { name: 'onboard.support.connect_browser' })).toBeNull();

		state = session({
			status: 'allowed',
			device_id: '423456789',
			hostname: 'ana-laptop',
			password_ready: true
		});
		const connect = await screen.findByRole(
			'button',
			{ name: 'onboard.support.connect_browser' },
			{ timeout: 5000 }
		);
		await user.click(connect);
		expect(opened).toEqual(['/remote/423456789?standalone=1']);
		expect(calls.some((c) => c.path.endsWith('/password'))).toBe(false);

		// The desktop app: NeoDesk by its own scheme, RustDesk by rustdesk:// (L423);
		// the password is fetched once and goes with both
		await user.click(screen.getByRole('button', { name: 'onboard.support.connect_app' }));
		await user.click(await screen.findByRole('menuitem', { name: 'NeoDesk' }));
		await waitFor(() => expect(hrefs).toEqual(['neodesk://connection/new/423456789?password=pw%201%262']));
		await user.click(screen.getByRole('button', { name: 'onboard.support.connect_app' }));
		await user.click(await screen.findByRole('menuitem', { name: 'RustDesk' }));
		await waitFor(() =>
			expect(hrefs).toEqual([
				'neodesk://connection/new/423456789?password=pw%201%262',
				'rustdesk://connection/new/423456789?password=pw%201%262'
			])
		);
		expect(calls.filter((c) => c.path.endsWith('/password'))).toHaveLength(1);
	}, 15000);

	test('Connect by ID opens the session in the console, without a link', async () => {
		const pages: string[] = [];
		vi.spyOn(go, 'page').mockImplementation((path: string) => void pages.push(path));
		const onclose = vi.fn();
		render(QuickSupportDialog, { onclose });
		const user = userEvent.setup();
		const field = screen.getByRole('textbox', { name: 'ID' });

		// Not an ID: says so and stays open
		await user.type(field, 'x{Enter}');
		expect(screen.getByText(/Enter a device ID/)).toBeInTheDocument();
		expect(pages).toEqual([]);

		expect(onclose).not.toHaveBeenCalled();

		await user.clear(field);
		await user.type(field, '423 456 789{Enter}');
		expect(pages).toEqual(['/remote/session/423456789']);
		expect(onclose).toHaveBeenCalledOnce();
		expect(calls.some((c) => c.path.startsWith('/api/support/sessions'))).toBe(false);
	});

	test('a session from the list shows its state, not the choice', async () => {
		const state = session({ status: 'waiting' });
		routes['GET /api/support/sessions/aaaaaaaaaaaaaaaa'] = () => ({
			body: { success: true, session: state }
		});
		render(QuickSupportDialog, { onclose: () => {}, session: state });
		expect(screen.getByText('onboard.support.state_waiting')).toBeInTheDocument();
		expect(screen.queryByRole('textbox', { name: 'ID' })).toBeNull();
	});

	test('a computer the server refused says why, and an admin can unblock it there (L550)', async () => {
		signedIn.isSuperAdmin = true;
		const state = session({
			status: 'ended',
			ended_by: 'refused',
			end_reason: 'device_revoked',
			device_id: '828416401',
			hostname: 'desktop-3mu2dcg'
		});
		routes['POST /api/devices/828416401/unblock'] = () => ({ body: { success: true } });
		render(QuickSupportDialog, { onclose: () => {}, session: state });
		expect(screen.getByText('desktop-3mu2dcg was revoked on this server.')).toBeInTheDocument();
		expect(screen.getByText(/revoked this computer in the console/)).toBeInTheDocument();
		const user = userEvent.setup();
		await user.click(screen.getByRole('button', { name: 'onboard.support.unblock' }));
		await waitFor(() =>
			expect(calls.some((c) => c.method === 'POST' && c.path === '/api/devices/828416401/unblock')).toBe(true)
		);
		expect(screen.getByRole('button', { name: 'onboard.support.again' })).toBeInTheDocument();
	});

	test('someone who may not unblock is told to ask an administrator', async () => {
		signedIn.isSuperAdmin = false;
		signedIn.permissions = ['device.connect'];
		render(QuickSupportDialog, {
			onclose: () => {},
			session: session({ status: 'ended', ended_by: 'refused', end_reason: 'device_revoked', hostname: 'pc' })
		});
		expect(screen.getByText(/Ask an administrator to unblock this computer/)).toBeInTheDocument();
		expect(screen.queryByRole('button', { name: 'onboard.support.unblock' })).toBeNull();
		signedIn.isSuperAdmin = true;
	});

	test('a declined session offers a new link', async () => {
		let state = session({ status: 'asking', device_id: '423456789' });
		routes['GET /api/support/sessions/aaaaaaaaaaaaaaaa'] = () => ({
			body: { success: true, session: state }
		});
		render(QuickSupportDialog, { onclose: () => {}, session: state });
		expect(screen.getByText('onboard.support.state_asking')).toBeInTheDocument();
		state = session({ status: 'declined', ended_by: 'customer' });
		expect(
			await screen.findByRole('button', { name: 'onboard.support.again' }, { timeout: 5000 })
		).toBeInTheDocument();
		expect(screen.getByText('onboard.support.state_declined')).toBeInTheDocument();
	}, 15000);
});
