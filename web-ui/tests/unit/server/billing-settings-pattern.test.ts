/**
 * Billing settings in the settings pattern (rules 1, 5, 6 and 11): each
 * option says what it does; a change waits in the save bar until Save,
 * and the toast names it; options that cannot have an effect say why.
 */
import { cleanup, render, screen, within } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';

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

import BillingSettings from '../../../src/routes/(console)/billing/BillingSettings.svelte';

let smtp = true;
let config: Record<string, boolean> = {};
let saved: unknown = null;

beforeEach(() => {
	smtp = true;
	saved = null;
	config = {
		help_requests_enabled: true,
		notify_assigned_operators: true,
		fallback_alert_email: true,
		include_folder_in_subject: true
	};
	for (const fn of Object.values(toastMock)) fn.mockReset();
	Element.prototype.scrollIntoView = () => {};
	vi.stubGlobal('fetch', async (input: string, init: RequestInit = {}) => {
		const path = new URL(input, location.origin).pathname;
		if (init.method && init.method !== 'GET') {
			saved = JSON.parse(String(init.body));
			return new Response(JSON.stringify({ success: true, config: saved }), {
				headers: { 'Content-Type': 'application/json' }
			});
		}
		const body = path.endsWith('/email-config') ? { config } : { configured: smtp };
		return new Response(JSON.stringify(body), { headers: { 'Content-Type': 'application/json' } });
	});
});

afterEach(() => {
	cleanup();
	vi.unstubAllGlobals();
});

const box = (key: string) =>
	screen.findByRole('checkbox', { name: `commercialization.notifications.${key}` });

test('each option says what it does', async () => {
	render(BillingSettings, { open: true, canManage: true });
	expect(await box('notify_assigned_operators')).toHaveAccessibleDescription(
		'The mail goes to the operators who have the device’s folder or group.'
	);
	expect(await box('fallback_alert_email')).toHaveAccessibleDescription(
		'When no operator gets the mail, it goes to the alert address in Settings › Email.'
	);
});

test('a change waits in the save bar; Save sends it and the toast names it', async () => {
	const user = userEvent.setup();
	render(BillingSettings, { open: true, canManage: true });
	await user.click(await box('include_folder_in_subject'));
	expect(saved).toBeNull();
	const bar = screen.getByRole('region', { name: 'Unsaved changes' });
	expect(bar.textContent).toContain('commercialization.notifications.include_folder_in_subject: on → off');
	await user.click(within(bar).getByRole('button', { name: 'commercialization.notifications.save' }));
	await vi.waitFor(() => expect(saved).toEqual({ ...config, include_folder_in_subject: false }));
	await vi.waitFor(() =>
		expect(toastMock.show).toHaveBeenCalledWith(
			expect.objectContaining({
				message: 'Saved: commercialization.notifications.include_folder_in_subject off.'
			})
		)
	);
});

test('with both recipients off it says that nobody gets the mail', async () => {
	const user = userEvent.setup();
	render(BillingSettings, { open: true, canManage: true });
	await user.click(await box('notify_assigned_operators'));
	await user.click(await box('fallback_alert_email'));
	expect(
		screen.getByText('Has no effect: nobody gets the mail while both options below are off.')
	).toBeTruthy();
});

test('without email sending the main option says it has no effect yet', async () => {
	smtp = false;
	render(BillingSettings, { open: true, canManage: true });
	await box('help_requests_enabled');
	expect(
		await screen.findByText('Has no effect until email sending is set up in Settings › Email.')
	).toBeTruthy();
});

test('without billing.manage the options are shown switched off for editing and no save bar appears', async () => {
	render(BillingSettings, { open: true, canManage: false });
	expect(await box('help_requests_enabled')).toBeDisabled();
	expect(screen.queryByRole('region', { name: 'Unsaved changes' })).toBeNull();
});
