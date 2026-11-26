/**
 * Settings, Sign-in, Disable accounts that don't sign in: off sends 0, on
 * sends the number of days, and a number out of range is refused at the
 * field.
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import InactiveAccountsForm from '../../../src/lib/settings/InactiveAccountsForm.svelte';

let saved: unknown[] = [];

beforeEach(() => {
	saved = [];
	vi.stubGlobal('fetch', async (_input: string, init: RequestInit = {}) => {
		if ((init.method ?? 'GET') === 'PUT') saved.push(JSON.parse(String(init.body)));
		const data = { days: 0, notice_days: 7, min_days: 14, max_days: 3650 };
		return new Response(JSON.stringify({ success: true, data }), {
			headers: { 'Content-Type': 'application/json' }
		});
	});
});

afterEach(() => {
	cleanup();
	vi.unstubAllGlobals();
});

describe('inactive accounts rule', () => {
	test('turned on with a number of days', async () => {
		render(InactiveAccountsForm);
		const on = await screen.findByLabelText('settings.inactive_label');
		await vi.waitFor(() => expect(on).not.toBeChecked());
		await userEvent.click(on);
		const days = screen.getByLabelText('settings.inactive_days');
		await userEvent.clear(days);
		await userEvent.type(days, '5');
		await userEvent.click(screen.getByRole('button', { name: 'actions.save' }));
		expect(saved).toEqual([]);
		await userEvent.clear(days);
		await userEvent.type(days, '60');
		await userEvent.click(screen.getByRole('button', { name: 'actions.save' }));
		await vi.waitFor(() => expect(saved).toEqual([{ days: 60 }]));
	});
});
