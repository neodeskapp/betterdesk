/**
 * Chat settings for this server (PATTERN.md rules 4, 6, 7 and 11): the
 * waiting mail is a switch plus the minutes, not 0 as a code; quoting the
 * message says it has no effect while no mail goes out; the dialog says
 * once whom the settings reach; a wrong number is named at its field; the
 * toast after saving names what was saved.
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';

const apiMock = vi.hoisted(() => ({
	loadServerSettings: vi.fn(async () => ({ mail_after_minutes: 15, mail_preview: true, computers: true })),
	saveServerSettings: vi.fn(async () => undefined)
}));
vi.mock('../../../src/lib/chat/api.ts', () => apiMock);
const toastMock = vi.hoisted(() => ({
	show: vi.fn(),
	success: vi.fn(),
	error: vi.fn(),
	warning: vi.fn(),
	info: vi.fn()
}));
vi.mock('#lib/ui/toasts.svelte.ts', async (orig) => ({
	...(await orig<typeof import('../../../src/lib/ui/toasts.svelte.ts')>()),
	toast: toastMock
}));

import ChatServerDialog from '../../../src/lib/chat/ChatServerDialog.svelte';

beforeEach(() => {
	apiMock.saveServerSettings.mockClear();
	for (const fn of Object.values(toastMock)) fn.mockReset();
});

afterEach(cleanup);

function show(minutes = 15) {
	apiMock.loadServerSettings.mockResolvedValueOnce({
		mail_after_minutes: minutes,
		mail_preview: true,
		computers: true
	});
	const onclose = vi.fn();
	render(ChatServerDialog, { onclose });
	return onclose;
}

test('the dialog says once whom the settings reach', async () => {
	show();
	expect(
		await screen.findByText('These apply to everyone on this server, from the next message.')
	).toBeTruthy();
});

test('the waiting mail is a switch and the minutes, with the stored value', async () => {
	show(15);
	expect(
		await screen.findByRole('checkbox', { name: /Mail when a person waits for an answer/ })
	).toBeChecked();
	expect(screen.getByRole('spinbutton', { name: 'Minutes to wait' })).toHaveValue(15);
});

test('0 stored shows the mail as off, without the minutes, and quoting says it has no effect', async () => {
	show(0);
	expect(
		await screen.findByRole('checkbox', { name: /Mail when a person waits for an answer/ })
	).not.toBeChecked();
	expect(screen.queryByRole('spinbutton', { name: 'Minutes to wait' })).toBeNull();
	expect(screen.getByText('Has no effect while no mail is sent.')).toBeTruthy();
});

test('turning the mail off saves 0 minutes and the toast names it', async () => {
	const user = userEvent.setup();
	const onclose = show(15);
	await user.click(await screen.findByRole('checkbox', { name: /Mail when a person waits for an answer/ }));
	await user.click(screen.getByRole('button', { name: 'actions.save' }));
	await vi.waitFor(() =>
		expect(apiMock.saveServerSettings).toHaveBeenCalledWith({
			mail_after_minutes: 0,
			mail_preview: true,
			computers: true
		})
	);
	await vi.waitFor(() =>
		expect(toastMock.show).toHaveBeenCalledWith(
			expect.objectContaining({ message: 'Saved: Mail when a person waits for an answer off.' })
		)
	);
	expect(onclose).toHaveBeenCalled();
});

test('a wrong number is named at its field and nothing is saved', async () => {
	const user = userEvent.setup();
	show(15);
	const field = await screen.findByRole('spinbutton', { name: 'Minutes to wait' });
	await user.clear(field);
	await user.type(field, '0');
	await user.click(screen.getByRole('button', { name: 'actions.save' }));
	expect(field).toHaveAccessibleDescription(/from 1 to 10080/);
	expect(apiMock.saveServerSettings).not.toHaveBeenCalled();
});
