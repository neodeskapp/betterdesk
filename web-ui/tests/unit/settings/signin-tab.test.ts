/**
 * Settings › Sign-in on the settings pattern: one save bar for the client
 * sessions, "Require two-factor sign-in" and "One session per account"
 * (BUGS L325: Save of the two-factor rule once sent nothing), each part
 * sent to its own endpoint, Undo in the toast for these sign-in rules,
 * session days refused at the field (L418), and SESSION_MAX_PER_USER
 * locking "One session per account" (A239), and the Sign in as mail,
 * disabling accounts that don't sign in and the security notices on the
 * same save bar.
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { serve } from './serve.ts';

vi.mock('$app/paths', () => ({ resolve: (p: string) => p || '/', asset: (p: string) => '/' + p }));

import SignInTab from '../../../src/lib/settings/tabs/SignInTab.svelte';
import { session } from '../../../src/lib/state/session.svelte.ts';
import { toasts } from '../../../src/lib/ui/toasts.svelte.ts';

const TWO_FACTOR = '/api/settings/two-factor';
const SESSIONS = '/api/settings/client-sessions';
const CONSOLE = '/api/settings/console-sessions';
const IMPERSONATION = '/api/settings/impersonation';
const INACTIVE = '/api/settings/inactive-accounts';
const NOTICES = '/api/settings/security-notices';

function routes(extra: Record<string, unknown> = {}) {
	return serve({
		[`GET ${SESSIONS}`]: { expiry_days: 9, sliding: true, max_days: 30 },
		[`GET ${TWO_FACTOR}`]: { required: false },
		[`GET ${CONSOLE}`]: { single_session: false, limit: 10, source: 'default', env_value: null },
		[`GET ${IMPERSONATION}`]: { notice_mail: true },
		[`PUT ${IMPERSONATION}`]: (body: unknown) => body,
		[`GET ${INACTIVE}`]: { days: 0, notice_days: 7, min_days: 14, max_days: 3650 },
		[`GET ${NOTICES}`]: {
			new_signin: true,
			password_changed: false,
			two_factor_changed: true,
			email_changed: true
		},
		'GET /api/panel/settings/sources': {
			'client_sessions.expiry_days': {
				value_source: 'stored',
				env_var: 'API_TOKEN_EXPIRY_DAYS',
				locked: false,
				default: 7
			}
		},
		...extra
	});
}

const save = () => userEvent.click(screen.getByRole('button', { name: 'Save changes' }));

beforeEach(() => {
	session.permissions = ['server.config'];
	toasts.items = [];
});

afterEach(() => {
	cleanup();
	vi.unstubAllGlobals();
	session.permissions = [];
});

describe('the sign-in rules, one save', () => {
	test('ticking Require two-factor sign-in and saving sends it; Undo in the toast puts it back', async () => {
		const server = routes();
		render(SignInTab);
		await userEvent.click(await screen.findByRole('checkbox', { name: /settings\.signin\.require_2fa/ }));
		expect(screen.getByRole('region', { name: 'Unsaved changes' })).toHaveTextContent(
			'settings.signin.require_2fa: settings.side.off → settings.side.on'
		);
		await save();
		await vi.waitFor(() => expect(server.sent('PUT', TWO_FACTOR)).toEqual([{ required: true }]));
		expect(server.sent('PUT', SESSIONS)).toEqual([]);
		expect(server.sent('PUT', CONSOLE)).toEqual([]);
		const done = await vi.waitFor(() => {
			const item = toasts.items.find((i) => i.action);
			if (!item) throw new Error('no toast with Undo');
			return item;
		});
		expect(done.message).toBe('Saved: settings.signin.require_2fa settings.side.on.');
		done.action!.onclick();
		await vi.waitFor(() =>
			expect(server.sent('PUT', TWO_FACTOR)).toEqual([{ required: true }, { required: false }])
		);
	});

	test('session days out of range are refused on the field, nothing is sent', async () => {
		const server = routes();
		render(SignInTab);
		const days = await screen.findByRole('spinbutton', { name: 'settings.client_sessions_expiry_days' });
		await vi.waitFor(() => expect(days).toHaveValue(9));
		await userEvent.clear(days);
		await userEvent.type(days, '0');
		expect(document.getElementById('client-sessions-expiry-days-error')).toHaveTextContent(
			'settings.signin.check_days_range'
		);
		await save();
		expect(server.sent('PUT', SESSIONS)).toEqual([]);

		await userEvent.clear(days);
		await userEvent.type(days, '40');
		expect(document.getElementById('client-sessions-max-days-error')).toHaveTextContent(
			'settings.signin.check_max_below_duration'
		);
		await save();
		expect(server.sent('PUT', SESSIONS)).toEqual([]);
	}, 30_000);

	test('the longest lifetime shows only while sessions are extended; a wrong hidden value is not sent', async () => {
		const server = routes();
		render(SignInTab);
		const max = await screen.findByRole('spinbutton', { name: 'settings.client_sessions_max_days' });
		await userEvent.clear(max);
		const extend = screen.getByRole('checkbox', { name: /settings\.client_sessions_sliding/ });
		expect(extend).toHaveAttribute('aria-controls', 'client-sessions-max-group');
		await userEvent.click(extend);
		expect(screen.queryByRole('spinbutton', { name: 'settings.client_sessions_max_days' })).toBeNull();
		await save();
		await vi.waitFor(() =>
			expect(server.sent('PUT', SESSIONS)).toEqual([{ expiry_days: 9, sliding: false, max_days: 30 }])
		);
	});

	test('the session duration says where it comes from and goes back to its default', async () => {
		routes();
		render(SignInTab);
		await screen.findByRole('spinbutton', { name: 'settings.client_sessions_expiry_days' });
		expect(document.getElementById('client-sessions-expiry-days')).toHaveTextContent('Set here');
		await userEvent.click(
			screen.getByRole('button', { name: 'Reset to default: settings.client_sessions_expiry_days' })
		);
		expect(screen.getByRole('spinbutton', { name: 'settings.client_sessions_expiry_days' })).toHaveValue(7);
	});
});

describe('One session per account', () => {
	test('the switch saves with the others', async () => {
		const server = routes();
		render(SignInTab);
		await userEvent.click(await screen.findByRole('checkbox', { name: /One session per account/ }));
		await save();
		await vi.waitFor(() => expect(server.sent('PUT', CONSOLE)).toEqual([{ single_session: true }]));
	});

	function envSays(value: string, limit: number) {
		routes({
			[`GET ${CONSOLE}`]: { single_session: false, limit, source: 'env', env_value: value },
			'GET /api/panel/settings/sources': {
				'console_sessions.single_session': {
					value_source: 'environment',
					env_var: 'SESSION_MAX_PER_USER',
					locked: true,
					default: false
				}
			}
		});
		render(SignInTab);
		return screen.findByRole('checkbox', { name: /One session per account/ });
	}
	const row = () => document.getElementById('single-session')!;
	const times = (text: string, part: string) => text.split(part).length - 1;

	test('SESSION_MAX_PER_USER=1 decides: the switch shows on, locked, the variable named once', async () => {
		const box = await envSays('1', 1);
		expect(box).toBeChecked();
		expect(box).toBeDisabled();
		expect(row()).toHaveTextContent(
			'Set in the environment file (SESSION_MAX_PER_USER=1). Remove it there to use this switch.'
		);
		expect(times(row().textContent ?? '', 'SESSION_MAX_PER_USER')).toBe(1);
		expect(row()).not.toHaveTextContent(/up to 1 browsers/);
		expect(screen.queryByRole('button', { name: /Reset to default: One session per account/ })).toBeNull();
	});

	test('a limit above one from the environment shows off and says how many', async () => {
		const box = await envSays('3', 3);
		expect(box).not.toBeChecked();
		expect(box).toBeDisabled();
		expect(row()).toHaveTextContent('An account can be signed in on up to 3 browsers and devices at once.');
		expect(times(row().textContent ?? '', 'SESSION_MAX_PER_USER')).toBe(1);
	});

	test('0 from the environment is any number', async () => {
		await envSays('0', 0);
		expect(row()).toHaveTextContent(
			'An account can be signed in on any number of browsers and devices at once.'
		);
	});

	test('with the switch on, the help still counts the browsers the switch off allows', async () => {
		routes({ [`GET ${CONSOLE}`]: { single_session: true, limit: 1, source: 'setting', env_value: null } });
		render(SignInTab);
		const box = await screen.findByRole('checkbox', { name: /One session per account/ });
		expect(box).toBeChecked();
		expect(row()).toHaveTextContent(
			'Off: an account can be signed in on up to 10 browsers and devices at once.'
		);
		expect(row()).not.toHaveTextContent(/up to 1 browsers/);
	});
});

test('Sign-in loads only its own settings, nothing of the directory, single sign-on or joining', async () => {
	const server = routes();
	render(SignInTab);
	await screen.findByRole('checkbox', { name: /settings\.signin\.require_2fa/ });
	const paths = server.calls.map((c) => c.path);
	for (const gone of ['/api/settings/ldap', '/api/settings/oidc', '/api/settings/enrollment'])
		expect(paths).not.toContain(gone);
	for (const gone of ['account-password', 'current-password', 'auth-panel-totp', 'ldap-form', 'oidc-form'])
		expect(document.getElementById(gone), gone).toBeNull();
});

describe('Sign in as mail, inactive accounts and security notices on the same save bar', () => {
	test('the notice switches show what the server has and Save sends all four', async () => {
		const server = routes();
		render(SignInTab);
		const changed = await screen.findByRole('checkbox', { name: /settings\.notices\.password_changed/ });
		expect(changed).not.toBeChecked();
		const signin = screen.getByRole('checkbox', { name: /settings\.notices\.new_signin/ });
		expect(signin).toBeChecked();
		await userEvent.click(signin);
		await save();
		await vi.waitFor(() =>
			expect(server.sent('PUT', NOTICES)).toEqual([
				{ new_signin: false, password_changed: false, two_factor_changed: true, email_changed: true }
			])
		);
		expect(server.sent('PUT', IMPERSONATION)).toEqual([]);
		expect(server.sent('PUT', INACTIVE)).toEqual([]);
	});

	test('the Sign in as mail switch saves its own setting', async () => {
		const server = routes();
		render(SignInTab);
		const mail = await screen.findByRole('checkbox', { name: /settings\.signin\.impersonation_mail/ });
		expect(mail).toBeChecked();
		await userEvent.click(mail);
		await save();
		await vi.waitFor(() => expect(server.sent('PUT', IMPERSONATION)).toEqual([{ notice_mail: false }]));
		expect(server.sent('PUT', NOTICES)).toEqual([]);
	});

	test('inactive accounts: days out of range are refused, then on sends the days', async () => {
		const server = routes();
		render(SignInTab);
		const on = await screen.findByRole('checkbox', { name: /settings\.inactive_label/ });
		expect(on).not.toBeChecked();
		await userEvent.click(on);
		const days = screen.getByRole('spinbutton', { name: 'settings.inactive_days' });
		await userEvent.clear(days);
		await userEvent.type(days, '5');
		expect(document.getElementById('inactive-accounts-days-error')).toHaveTextContent(
			'settings.inactive_days_invalid'
		);
		await save();
		expect(server.sent('PUT', INACTIVE)).toEqual([]);
		await userEvent.clear(days);
		await userEvent.type(days, '60');
		await save();
		await vi.waitFor(() => expect(server.sent('PUT', INACTIVE)).toEqual([{ days: 60 }]));
	}, 30_000);
});
