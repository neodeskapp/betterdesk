/**
 * The SMTP form's password: an untouched field sends the server's mask
 * back (the saved password stays), a cleared one an empty text.
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

vi.mock('#lib/api/settings-system.ts', async (orig) => ({
	...(await orig<typeof import('../../../src/lib/api/settings-system.ts')>()),
	getSmtp: vi.fn(async () => ({
		configured: true,
		host: 'smtp.example.com',
		port: 587,
		secure: false,
		user: 'alerts',
		pass: '********',
		from: 'alerts@example.com',
		alert_email: ''
	})),
	saveSmtp: vi.fn(async () => ({}))
}));
vi.mock('#lib/ui/dialogs.svelte.ts', () => ({ confirm: vi.fn(async () => true) }));

import * as api from '../../../src/lib/api/settings-system.ts';
import { serve } from './serve.ts';
import EmailTab from '../../../src/lib/settings/tabs/EmailTab.svelte';
import { session } from '../../../src/lib/state/session.svelte.ts';

beforeEach(() => {
	// The sources answer at once (a real fetch would retry and slow the page down)
	serve({});
	session.isSuperAdmin = true;
	vi.mocked(api.saveSmtp).mockClear();
});

afterEach(() => {
	cleanup();
	vi.unstubAllGlobals();
	session.isSuperAdmin = false;
});

async function setup() {
	const user = userEvent.setup();
	render(EmailTab);
	const host = await screen.findByLabelText('settings.email.host_label');
	await vi.waitFor(() => expect(host).toHaveValue('smtp.example.com'));
	return user;
}

/** A change, so the save bar shows */
async function changeFrom(user: Awaited<ReturnType<typeof setup>>) {
	const from = screen.getByLabelText('settings.email.smtp_from');
	await user.clear(from);
	await user.type(from, 'ops@example.com');
}

describe('SMTP password', () => {
	test('untouched, the mask goes back and the saved password stays', async () => {
		const user = await setup();
		const from = screen.getByLabelText('settings.email.smtp_from');
		await user.clear(from);
		await user.type(from, 'ops@example.com');
		await user.click(screen.getByRole('button', { name: 'Save changes' }));
		await vi.waitFor(() => expect(api.saveSmtp).toHaveBeenCalledTimes(1));
		expect(vi.mocked(api.saveSmtp).mock.calls[0]![0]).toMatchObject({ user: 'alerts', pass: '********' });
	});

	test('cleared, an empty text goes out', async () => {
		const user = await setup();
		await user.clear(screen.getByLabelText('settings.email.smtp_pass'));
		await user.click(screen.getByRole('button', { name: 'Save changes' }));
		await vi.waitFor(() => expect(api.saveSmtp).toHaveBeenCalledTimes(1));
		expect(vi.mocked(api.saveSmtp).mock.calls[0]![0]).toMatchObject({ pass: '' });
	});
});

/**
 * The page has no switch for the certificate check: a save sends the
 * saved tlsVerify back as it was, so saving the form never turns the
 * check off or on (BUGS L57).
 */
describe('certificate check', () => {
	test('a saved tlsVerify goes back as it was', async () => {
		vi.mocked(api.getSmtp).mockResolvedValueOnce({
			configured: true,
			host: 'smtp.example.com',
			port: 587,
			secure: false,
			user: 'alerts',
			pass: '********',
			from: 'alerts@example.com',
			alert_email: '',
			tlsVerify: false
		});
		const user = await setup();
		await changeFrom(user);
		await user.click(screen.getByRole('button', { name: 'Save changes' }));
		await vi.waitFor(() => expect(api.saveSmtp).toHaveBeenCalledTimes(1));
		expect(vi.mocked(api.saveSmtp).mock.calls[0]![0]).toMatchObject({ pass: '********', tlsVerify: false });
	});

	test('settings without one send none', async () => {
		const user = await setup();
		await changeFrom(user);
		await user.click(screen.getByRole('button', { name: 'Save changes' }));
		await vi.waitFor(() => expect(api.saveSmtp).toHaveBeenCalledTimes(1));
		expect(vi.mocked(api.saveSmtp).mock.calls[0]![0]).not.toHaveProperty('tlsVerify');
	});
});

/** Port 0 was sent as 587 with "Saved" (BUGS L418): it is refused at the field */
describe('port', () => {
	test('a port outside 1 to 65535 is refused at its field and nothing is sent', async () => {
		const user = await setup();
		const port = screen.getByLabelText('settings.email.smtp_port');
		await user.clear(port);
		await user.type(port, '0');
		await user.click(screen.getByRole('button', { name: 'Save changes' }));
		await vi.waitFor(() =>
			expect(screen.getAllByText('Enter a port from 1 to 65535.').length).toBeGreaterThan(0)
		);
		expect(api.saveSmtp).not.toHaveBeenCalled();
	});
});

/** The server's refusal shows at the field it names (BUGS L406, L419) */
describe('refusals', () => {
	test('a sender that is no address is refused at the From field', async () => {
		const { ApiError } = await import('../../../src/lib/api/client.ts');
		vi.mocked(api.saveSmtp).mockRejectedValueOnce(
			new ApiError('Enter one email address', 400, {
				data: { error: 'Enter one email address', field: 'from' }
			})
		);
		const user = await setup();
		const from = screen.getByLabelText('settings.email.smtp_from');
		await user.clear(from);
		await user.type(from, 'not an address');
		await user.click(screen.getByRole('button', { name: 'Save changes' }));
		await vi.waitFor(() =>
			expect(document.getElementById('email-smtp-from-error')?.textContent).toContain(
				'Enter one email address'
			)
		);
	});
});

/** The settings pattern: dependencies, sources and the alert list */
describe('the mail server rows', () => {
	test('user name and password show only while the mail server needs a sign-in', async () => {
		const user = await setup();
		const auth = screen.getByRole('checkbox', { name: /settings\.email\.auth_label/ });
		expect(auth).toHaveAttribute('aria-controls', 'email-smtp-auth-fields');
		expect(screen.getByLabelText('settings.email.smtp_user')).toBeVisible();
		await user.click(auth);
		expect(document.getElementById('email-smtp-auth-fields')).not.toBeVisible();
		await user.click(screen.getByRole('button', { name: 'Save changes' }));
		await vi.waitFor(() => expect(api.saveSmtp).toHaveBeenCalledTimes(1));
		expect(vi.mocked(api.saveSmtp).mock.calls[0]![0]).toMatchObject({ user: '', pass: '' });
	});

	test('a value from the environment says so; the alert addresses are one per row', async () => {
		serve({
			'GET /api/panel/settings/sources': {
				'smtp.host': { value_source: 'environment', env_var: 'SMTP_HOST', locked: false },
				'smtp.port': { value_source: 'environment', env_var: 'SMTP_PORT', locked: false, default: 587 }
			}
		});
		const user = await setup();
		expect(document.getElementById('email-smtp-host')).toHaveTextContent(
			'Set in the environment file (SMTP_HOST). Saving here replaces it.'
		);
		await user.click(screen.getByRole('button', { name: 'Add an address' }));
		await user.type(screen.getByRole('textbox', { name: 'Alert email 1' }), 'ops@');
		expect(screen.getByText(/not an email address/)).toBeInTheDocument();
		await user.type(screen.getByRole('textbox', { name: 'Alert email 1' }), 'example.com');
		await user.click(screen.getByRole('button', { name: 'Add an address' }));
		await user.type(screen.getByRole('textbox', { name: 'Alert email 2' }), 'it@example.com');
		await user.click(screen.getByRole('button', { name: 'Save changes' }));
		await vi.waitFor(() => expect(api.saveSmtp).toHaveBeenCalledTimes(1));
		expect(vi.mocked(api.saveSmtp).mock.calls[0]![0]).toMatchObject({
			alert_email: 'ops@example.com, it@example.com'
		});
	});
});

describe('a mail server that is not set up', () => {
	test('says nothing is wrong until a change or a save', async () => {
		vi.mocked(api.getSmtp).mockResolvedValueOnce({
			configured: false,
			host: '',
			port: 587,
			secure: false,
			user: '',
			pass: '',
			from: '',
			alert_email: ''
		});
		const user = userEvent.setup();
		render(EmailTab);
		await screen.findByLabelText('settings.email.host_label');
		expect(document.getElementById('email-smtp-host-error')).toBeNull();
		await user.type(screen.getByLabelText('settings.email.smtp_from'), 'ops@example.com');
		await user.click(screen.getByRole('button', { name: 'Save changes' }));
		expect(document.getElementById('email-smtp-host-error')).toHaveTextContent(
			'Enter the address of the mail server.'
		);
		expect(api.saveSmtp).not.toHaveBeenCalled();
	});
});
