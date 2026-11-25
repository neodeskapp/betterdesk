/**
 * The setup guide: the overview shows what the server says (which steps,
 * which are done, whether it is open for this account) and never ticks a
 * step itself; Hide and Show only ask the server; each step leads to the
 * place it is done and that place offers the way back.
 */
import { cleanup, render, screen, waitFor, within } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeAll, beforeEach, describe, expect, test, vi } from 'vitest';

const pageUrl = vi.hoisted(() => ({ url: new URL('http://localhost/app') }));
const navigation = vi.hoisted(() => ({ goto: vi.fn(async () => {}) }));
vi.mock('$app/state', () => ({ page: pageUrl }));
vi.mock('$app/paths', () => ({ resolve: (p: string) => '/app' + (p || '/'), asset: (p: string) => '/' + p }));
vi.mock('$app/navigation', () => ({ goto: navigation.goto, pushState: vi.fn(), replaceState: vi.fn() }));

import SetupGuide from '../../../src/lib/guide/SetupGuide.svelte';
import GuideLink from '../../../src/lib/guide/GuideLink.svelte';
import GuideReturn from '../../../src/lib/guide/GuideReturn.svelte';
import { guideStep, STEP_TARGETS } from '../../../src/lib/guide/steps.ts';
import { STEP_IDS } from '../../../src/lib/api/setupGuide.ts';
import { guide } from '../../../src/lib/guide/guide.svelte.ts';
import { setStrings } from '../../../src/lib/i18n/i18n.svelte.ts';
import { session } from '../../../src/lib/state/session.svelte.ts';
import { dismissAll, toasts } from '../../../src/lib/ui/toasts.svelte.ts';

beforeAll(async () => {
	await import('../../../src/lib/onboard/AddComputerDialog.svelte');
}, 60_000);

type Step = { id: string; done: boolean };
let calls: { method: string; path: string }[];
let state: { visible: boolean; steps: Step[] };

const ALL = (done: string[] = []): Step[] => STEP_IDS.map((id) => ({ id, done: done.includes(id) }));

function signIn(permissions: string[], id = 1) {
	session.isSuperAdmin = false;
	session.permissions = permissions;
	session.access = null;
	session.user = { id, username: 'admin', role: 'admin', preferredLanguage: null, avatarVersion: null };
}

beforeEach(() => {
	calls = [];
	state = { visible: true, steps: ALL(['two_factor', 'https', 'people']) };
	pageUrl.url = new URL('http://localhost/app');
	navigation.goto.mockClear();
	signIn(['server.config', 'enrollment.approve', 'device.connect', 'user.create']);
	guide.status = null;
	setStrings('en', 'ltr', { setup_guide: { progress: '{done} of {total} done.' } });
	vi.stubGlobal('fetch', async (input: string, init: RequestInit = {}) => {
		const url = new URL(input, location.origin);
		const method = init.method ?? 'GET';
		calls.push({ method, path: url.pathname });
		let body: unknown = { success: false, error: 'nope' };
		let status = 404;
		if (method === 'GET' && url.pathname === '/api/setup-guide')
			[status, body] = [200, { success: true, data: state }];
		if (method === 'POST' && url.pathname === '/api/setup-guide/hide') {
			state = { ...state, visible: false };
			[status, body] = [200, { success: true, data: state }];
		}
		if (method === 'POST' && url.pathname === '/api/setup-guide/show') {
			state = { ...state, visible: true };
			[status, body] = [200, { success: true, data: state }];
		}
		if (url.pathname === '/api/folders') [status, body] = [200, { success: true, folders: [] }];
		if (url.pathname === '/api/onboard/status')
			[status, body] = [200, { success: true, console: { base: 'http://desk', https: false }, mail: false }];
		if (url.pathname === '/api/onboard/invites') [status, body] = [200, { success: true, invites: [] }];
		return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
	});
});

afterEach(() => {
	cleanup();
	dismissAll();
	vi.unstubAllGlobals();
});

describe('where each step leads', () => {
	test('every step has a place; pages carry the step for the way back, before their #id', () => {
		for (const id of STEP_IDS) expect(STEP_TARGETS[id]).toBeTruthy();
		expect(STEP_TARGETS.password).toEqual({
			kind: 'page',
			href: '/account?guide=password#password'
		});
		expect(STEP_TARGETS.two_factor).toEqual({
			kind: 'page',
			href: '/account?guide=two_factor#two-factor'
		});
		expect(STEP_TARGETS.address).toEqual({ kind: 'page', href: '/settings?guide=address#public-endpoints' });
		expect(STEP_TARGETS.backup).toEqual({
			kind: 'page',
			href: '/server/backups?guide=backup#backup-create'
		});
		expect(STEP_TARGETS.computer).toEqual({ kind: 'add-computer' });
		expect(STEP_TARGETS.https.kind).toBe('docs');
	});

	test('only a known step counts as the way back', () => {
		expect(guideStep(new URLSearchParams('guide=backup'))).toBe('backup');
		expect(guideStep(new URLSearchParams('guide=nope'))).toBeNull();
		expect(guideStep(new URLSearchParams(''))).toBeNull();
	});
});

describe('the guide on the overview', () => {
	test('lists the steps the server sends, in its order, with what it says is done', async () => {
		render(SetupGuide);
		const section = await screen.findByRole('region', { name: 'setup_guide.title' });
		const rows = within(section).getAllByRole('listitem');
		expect(rows.map((r) => r.dataset.step)).toEqual([...STEP_IDS]);
		const done = rows.filter((r) => r.classList.contains('is-done')).map((r) => r.dataset.step);
		expect(done).toEqual(['two_factor', 'https', 'people']);
		// The mark people see: a tick for those, an empty circle for the rest
		const marks = rows.map((r) => r.querySelector('.guide-mark')?.textContent?.trim());
		expect(marks.filter((m) => m === 'check_circle')).toHaveLength(3);
		expect(marks[1]).toBe('check_circle');
		expect(marks[0]).toBe('radio_button_unchecked');
		// Each row says its state to screen readers, and nothing on the row ticks it
		expect(within(rows[0]!).getByText('setup_guide.state_todo:')).toBeInTheDocument();
		expect(within(rows[1]!).getByText('setup_guide.state_done:')).toBeInTheDocument();
		expect(within(section).queryByRole('checkbox')).toBeNull();
		// Rows lead to their place: a page with the step, the docs in a new tab, the dialog here
		expect(within(rows[0]!).getByRole('link')).toHaveAttribute(
			'href',
			'/app/account?guide=password#password'
		);
		expect(within(rows[3]!).getByRole('link')).toHaveAttribute('target', '_blank');
		expect(within(rows[4]!).getByRole('button')).toBeInTheDocument();
	});

	test('a step the server leaves out is not there, and a new state from the server shows', async () => {
		state.steps = state.steps.filter((s) => s.id !== 'people');
		render(SetupGuide);
		const section = await screen.findByRole('region', { name: 'setup_guide.title' });
		expect(within(section).getAllByRole('listitem')).toHaveLength(7);
		expect(section.querySelector('.count-line')).toHaveTextContent('2 of 7 done.');
		state.steps = ALL([...STEP_IDS]);
		window.dispatchEvent(new Event('app:refresh'));
		expect(await screen.findByText('setup_guide.all_done')).toBeInTheDocument();
		expect(within(section).getAllByRole('listitem')).toHaveLength(8);
	});

	test('nothing shows while the guide is closed for this account', async () => {
		state.visible = false;
		render(SetupGuide);
		await waitFor(() => expect(calls.some((c) => c.path === '/api/setup-guide')).toBe(true));
		expect(screen.queryByRole('region', { name: 'setup_guide.title' })).toBeNull();
	});

	test('an account without server.config does not ask and sees nothing', async () => {
		signIn(['device.view', 'device.connect'], 2);
		render(SetupGuide);
		await new Promise((r) => setTimeout(r, 50));
		expect(calls.filter((c) => c.path.startsWith('/api/setup-guide'))).toEqual([]);
		expect(screen.queryByRole('region', { name: 'setup_guide.title' })).toBeNull();
	});

	test('Hide asks the server to close it, and the guide goes', async () => {
		render(SetupGuide);
		const user = userEvent.setup();
		await user.click(await screen.findByRole('button', { name: 'setup_guide.hide' }));
		await waitFor(() => expect(screen.queryByRole('region', { name: 'setup_guide.title' })).toBeNull());
		expect(calls).toContainEqual({ method: 'POST', path: '/api/setup-guide/hide' });
		expect(toasts.items.map((t) => t.message)).toEqual(['setup_guide.hidden']);
	});

	test('Add your first computer opens Add a computer on the overview', async () => {
		render(SetupGuide);
		const user = userEvent.setup();
		const row = (await screen.findAllByRole('listitem')).find((r) => r.dataset.step === 'computer')!;
		await user.click(within(row).getByRole('button'));
		expect(
			await screen.findByRole('dialog', { name: 'onboard.invite.title' }, { timeout: 5000 })
		).toBeInTheDocument();
	});
});

describe('bringing it back and the way back', () => {
	test('Show the setup guide asks the server to open it and goes to the overview', async () => {
		state.visible = false;
		render(GuideLink);
		const user = userEvent.setup();
		await user.click(screen.getByRole('button', { name: 'setup_guide.link_button' }));
		await waitFor(() => expect(navigation.goto).toHaveBeenCalledWith('/app'));
		expect(calls).toContainEqual({ method: 'POST', path: '/api/setup-guide/show' });
	});

	test('the link is not there for an account without server.config', () => {
		signIn(['device.view'], 3);
		render(GuideLink);
		expect(screen.queryByRole('button', { name: 'setup_guide.link_button' })).toBeNull();
	});

	test('a page a step led to offers the way back to the overview', () => {
		pageUrl.url = new URL('http://localhost/app/settings?tab=auth&guide=password');
		render(GuideReturn);
		const back = screen.getByRole('link', { name: 'setup_guide.back' });
		expect(back).toHaveAttribute('href', '/app');
		expect(screen.getByText('setup_guide.back_step')).toBeInTheDocument();
	});

	test('no way back on a page opened any other way', () => {
		pageUrl.url = new URL('http://localhost/app/settings?tab=auth');
		render(GuideReturn);
		expect(screen.queryByRole('link', { name: 'setup_guide.back' })).toBeNull();
	});
});
