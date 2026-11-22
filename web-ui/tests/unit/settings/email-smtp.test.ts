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
import EmailTab from '../../../src/lib/settings/tabs/EmailTab.svelte';
import { session } from '../../../src/lib/state/session.svelte.ts';

beforeEach(() => {
	session.isSuperAdmin = true;
	vi.mocked(api.saveSmtp).mockClear();
});

afterEach(() => {
	cleanup();
	session.isSuperAdmin = false;
});

async function setup() {
	const user = userEvent.setup();
	render(EmailTab);
	const host = await screen.findByLabelText('settings.email.host_label');
	await vi.waitFor(() => expect(host).toHaveValue('smtp.example.com'));
	return user;
}

describe('SMTP password', () => {
	test('untouched, the mask goes back and the saved password stays', async () => {
		const user = await setup();
		const from = screen.getByLabelText('settings.email.smtp_from');
		await user.clear(from);
		await user.type(from, 'ops@example.com');
		await user.click(screen.getByRole('button', { name: 'settings.email.smtp_save' }));
		await vi.waitFor(() => expect(api.saveSmtp).toHaveBeenCalledTimes(1));
		expect(vi.mocked(api.saveSmtp).mock.calls[0]![0]).toMatchObject({ user: 'alerts', pass: '********' });
	});

	test('cleared, an empty text goes out', async () => {
		const user = await setup();
		await user.clear(screen.getByLabelText('settings.email.smtp_pass'));
		await user.click(screen.getByRole('button', { name: 'settings.email.smtp_save' }));
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
		await user.click(screen.getByRole('button', { name: 'settings.email.smtp_save' }));
		await vi.waitFor(() => expect(api.saveSmtp).toHaveBeenCalledTimes(1));
		expect(vi.mocked(api.saveSmtp).mock.calls[0]![0]).toMatchObject({ pass: '********', tlsVerify: false });
	});

	test('settings without one send none', async () => {
		const user = await setup();
		await user.click(screen.getByRole('button', { name: 'settings.email.smtp_save' }));
		await vi.waitFor(() => expect(api.saveSmtp).toHaveBeenCalledTimes(1));
		expect(vi.mocked(api.saveSmtp).mock.calls[0]![0]).not.toHaveProperty('tlsVerify');
	});
});
