/**
 * Billing notification settings: the form is drawn only after the stored
 * values loaded, so a save never writes the start values over them.
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import BillingSettings from '../../../src/routes/(console)/commercialization/BillingSettings.svelte';

let configStatus = 200;

beforeEach(() => {
	configStatus = 200;
	Element.prototype.scrollIntoView = () => {};
	vi.stubGlobal('fetch', async (input: string) => {
		const path = new URL(input, location.origin).pathname;
		const body = path.endsWith('/email-config')
			? configStatus === 200
				? { config: { help_requests_enabled: false } }
				: { error: 'no' }
			: { configured: true };
		return new Response(JSON.stringify(body), {
			status: path.endsWith('/email-config') ? configStatus : 200,
			headers: { 'Content-Type': 'application/json' }
		});
	});
});

afterEach(() => {
	cleanup();
	vi.unstubAllGlobals();
});

const helpBox = () =>
	screen.queryByRole('checkbox', { name: 'commercialization.notifications.help_requests_enabled' });

describe('billing settings', () => {
	test('a failed load shows the error and no form to save', async () => {
		configStatus = 403;
		render(BillingSettings, { open: true, canManage: true });
		await screen.findByText('commercialization.ui.error_settings');
		expect(helpBox()).toBeNull();
		expect(screen.queryByRole('button', { name: 'commercialization.notifications.save' })).toBeNull();
	});

	test('after a load the stored values show', async () => {
		render(BillingSettings, { open: true, canManage: true });
		await vi.waitFor(() => expect(helpBox()).not.toBeNull());
		expect(helpBox()).not.toBeChecked();
	});
});
