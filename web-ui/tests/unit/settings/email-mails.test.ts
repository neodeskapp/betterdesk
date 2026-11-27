/**
 * Settings › Email, the mails: the sender and footer form saves what was
 * typed and shows a refused field; the preview shows the mail in a
 * sandboxed frame and asks again for dark; "Send me this mail" sends the
 * mail picked in the language picked; a reader's turned-off notification
 * can be turned on again.
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

vi.mock('#lib/api/settings-system.ts', async (orig) => ({
	...(await orig<typeof import('../../../src/lib/api/settings-system.ts')>()),
	getMailIdentity: vi.fn(async () => ({
		identity: { from_name: '', organisation: '', support_email: '', support_url: '' },
		default_from_name: 'Acme IT via NeoDesk'
	})),
	saveMailIdentity: vi.fn(async (i: unknown) => ({ identity: i, default_from_name: 'Acme IT via NeoDesk' })),
	getMailKinds: vi.fn(async () => ({
		kinds: [
			{ kind: 'invite', title: 'Computer invite' },
			{ kind: 'alert', title: 'Alert' }
		],
		languages: [
			{ code: 'en', name: 'English' },
			{ code: 'de', name: 'Deutsch' }
		],
		default_language: 'en'
	})),
	getMailPreview: vi.fn(async (kind: string, lang: string, scheme: string) => ({
		kind,
		lang,
		from: 'Acme IT via NeoDesk',
		subject: `${kind} in ${lang}`,
		preheader: 'pre',
		html: `<p>${scheme}</p>`,
		text: 'text'
	})),
	sendMailPreview: vi.fn(async () => ({ to: 'admin@example.com' })),
	getUnsubscribed: vi.fn(async () => ({ unsubscribed: { alert: ['ops@example.com'], chat: [], help: [] } })),
	resubscribe: vi.fn(async () => ({ unsubscribed: { alert: [], chat: [], help: [] } }))
}));

import * as api from '../../../src/lib/api/settings-system.ts';
import MailsPanel from '../../../src/lib/settings/tabs/MailsPanel.svelte';

beforeEach(() => {
	for (const f of [api.saveMailIdentity, api.getMailPreview, api.sendMailPreview, api.resubscribe])
		vi.mocked(f).mockClear();
});
afterEach(() => cleanup());

describe('mails', () => {
	test('the sender and footer are saved as typed, and the default sender is named', async () => {
		const user = userEvent.setup();
		render(MailsPanel);
		const name = await screen.findByLabelText('settings.email.from_name');
		expect(name).toHaveAttribute('placeholder', 'Acme IT via NeoDesk');
		await user.type(screen.getByLabelText('settings.email.organisation'), 'Acme GmbH');
		await user.type(screen.getByLabelText('settings.email.support_email'), 'it@example.com');
		await user.click(screen.getByRole('button', { name: 'settings.email.identity_save' }));
		await vi.waitFor(() => expect(api.saveMailIdentity).toHaveBeenCalledTimes(1));
		expect(vi.mocked(api.saveMailIdentity).mock.calls[0]![0]).toMatchObject({
			organisation: 'Acme GmbH',
			support_email: 'it@example.com'
		});
	});

	test('a support address that is none is not sent', async () => {
		const user = userEvent.setup();
		render(MailsPanel);
		await user.type(await screen.findByLabelText('settings.email.support_email'), 'nope');
		await user.click(screen.getByRole('button', { name: 'settings.email.identity_save' }));
		expect(api.saveMailIdentity).not.toHaveBeenCalled();
	});

	test('the preview is a sandboxed frame, dark on request, and the mail goes to the admin', async () => {
		const user = userEvent.setup();
		render(MailsPanel);
		const frame = await screen.findByTitle(/settings.email.preview_frame|Computer invite/);
		expect(frame).toHaveAttribute('sandbox', '');
		expect(frame.getAttribute('srcdoc')).toBe('<p>light</p>');
		await user.click(screen.getByRole('button', { name: 'settings.email.preview_dark' }));
		await vi.waitFor(() =>
			expect(api.getMailPreview).toHaveBeenLastCalledWith('invite', 'en', 'dark', expect.anything())
		);
		await user.selectOptions(screen.getByLabelText('settings.email.preview_language'), 'de');
		await user.selectOptions(screen.getByLabelText('settings.email.preview_mail'), 'alert');
		await user.click(screen.getByRole('button', { name: 'settings.email.preview_send' }));
		await vi.waitFor(() => expect(api.sendMailPreview).toHaveBeenCalledWith('alert', 'de'));
	});

	test('a turned-off notification is turned on again', async () => {
		const user = userEvent.setup();
		render(MailsPanel);
		await screen.findByText('ops@example.com');
		await user.click(
			screen.getByRole('button', { name: /settings.email.resubscribe_label|ops@example.com/ })
		);
		await vi.waitFor(() => expect(api.resubscribe).toHaveBeenCalledWith('alert', 'ops@example.com'));
		await vi.waitFor(() => expect(screen.queryByText('ops@example.com')).toBeNull());
	});
});
