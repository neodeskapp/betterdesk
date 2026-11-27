/**
 * The access rule's form: each client setting is its three choices side by
 * side (client decides, on, off), one click to change, and Save sends only
 * the settings that are on or off.
 */
import { cleanup, render, screen, waitFor, within } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import StrategySheet from '../../src/routes/(console)/devices/StrategySheet.svelte';
import type { Strategy } from '../../src/lib/api/strategies.ts';
import { dismissAll } from '../../src/lib/ui/toasts.svelte.ts';

let posted: { method: string; path: string; body: unknown }[] = [];

beforeEach(() => {
	posted = [];
	vi.stubGlobal('fetch', async (input: string, init: RequestInit = {}) => {
		const url = new URL(input, location.origin);
		const method = init.method ?? 'GET';
		if (method !== 'GET')
			posted.push({
				method,
				path: url.pathname,
				body: typeof init.body === 'string' ? (JSON.parse(init.body) as unknown) : undefined
			});
		const data = url.pathname === '/api/user-groups' ? { success: true, data: [] } : { success: true };
		return {
			ok: true,
			status: 200,
			redirected: false,
			url: url.href,
			headers: new Headers({ 'content-type': 'application/json' }),
			json: async () => data,
			text: async () => JSON.stringify(data)
		} as unknown as Response;
	});
});

afterEach(() => {
	cleanup();
	dismissAll();
	vi.unstubAllGlobals();
});

describe('client settings', () => {
	test('each setting is a group of three choices; one click sets it, Save sends only what is on or off', async () => {
		const rule = {
			guid: 'r1',
			name: 'Kiosk',
			enabled: true,
			permissions: { 'enable-audio': 'N' }
		} as unknown as Strategy;
		const onsaved = vi.fn();
		render(StrategySheet, { rule, groups: [], onclose: vi.fn(), onsaved });

		const transfer = screen.getByRole('radiogroup', { name: 'devices.strategy_perm_file_transfer' });
		const choices = within(transfer).getAllByRole('radio');
		expect(choices.map((c) => c.closest('.segment')?.textContent?.trim())).toEqual([
			'devices.strategy_perm_default',
			'devices.strategy_perm_allow',
			'devices.strategy_perm_deny'
		]);
		expect(choices[0]).toBeChecked();
		// No select to open first
		expect(screen.queryAllByRole('combobox').map((c) => c.id)).not.toContain(transfer.id);

		const audio = screen.getByRole('radiogroup', { name: 'devices.strategy_perm_audio' });
		expect(within(audio).getAllByRole('radio')[2]).toBeChecked();

		await userEvent.click(within(transfer).getByLabelText('devices.strategy_perm_allow'));
		await userEvent.click(within(audio).getByLabelText('devices.strategy_perm_default'));
		await userEvent.click(screen.getByRole('button', { name: 'actions.save' }));

		await waitFor(() => expect(onsaved).toHaveBeenCalled());
		expect(posted).toEqual([
			{
				method: 'PATCH',
				path: '/api/panel/strategies/r1',
				body: {
					name: 'Kiosk',
					user_group_guid: '',
					device_group_guid: '',
					enabled: true,
					permissions: { 'enable-file-transfer': 'Y' }
				}
			}
		]);
	});
});
