/**
 * The access rule's assign sheet: unticked targets are taken off the rule,
 * ticked ones put on, and a failed load of the current targets is shown.
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import StrategyAssignSheet from '../../src/routes/(console)/devices/StrategyAssignSheet.svelte';
import type { Device, DeviceGroup } from '../../src/lib/api/devices.ts';
import type { Strategy } from '../../src/lib/api/strategies.ts';
import { dismissAll } from '../../src/lib/ui/toasts.svelte.ts';

interface Call {
	method: string;
	path: string;
	body: unknown;
}
let calls: Call[] = [];
let detailStatus = 200;

const rule = { guid: 'r1', name: 'Kiosk' } as Strategy;
const devices = [{ id: '111' }, { id: '222' }, { id: '333' }] as Device[];
const groups = [{ guid: 'g1', name: 'Front desk' }] as DeviceGroup[];

function reply(status: number, data: unknown, href: string): Response {
	return {
		ok: status < 300,
		status,
		redirected: false,
		url: href,
		headers: new Headers({ 'content-type': 'application/json' }),
		json: async () => data,
		text: async () => JSON.stringify(data)
	} as unknown as Response;
}

beforeEach(() => {
	calls = [];
	detailStatus = 200;
	vi.stubGlobal('fetch', async (input: string, init: RequestInit = {}) => {
		const url = new URL(input, location.origin);
		const method = init.method ?? 'GET';
		const body = typeof init.body === 'string' ? (JSON.parse(init.body) as unknown) : undefined;
		calls.push({ method, path: url.pathname, body });
		if (method === 'GET' && url.pathname === '/api/users')
			return reply(200, { success: true, data: { users: [{ id: 1, username: 'anna' }] } }, url.href);
		if (method === 'GET' && url.pathname === '/api/panel/strategies/r1') {
			if (detailStatus !== 200) return reply(detailStatus, { success: false, error: 'boom' }, url.href);
			const strategy = {
				guid: 'r1',
				name: 'Kiosk',
				peer_ids: ['111', '222'],
				user_names: ['anna'],
				group_guids: []
			};
			return reply(200, { success: true, data: { strategy } }, url.href);
		}
		return reply(200, { success: true }, url.href);
	});
});

afterEach(() => {
	cleanup();
	dismissAll();
	vi.unstubAllGlobals();
});

const writes = () => calls.filter((c) => c.method !== 'GET');
const box = (name: string) => screen.getByRole('checkbox', { name });

describe('assign sheet', () => {
	test('unticked targets go to unassign, newly ticked ones to assign', async () => {
		const user = userEvent.setup();
		const onsaved = vi.fn();
		render(StrategyAssignSheet, { rule, groups, devices, onclose: () => {}, onsaved });
		await vi.waitFor(() => expect(box('111')).toBeChecked());
		await user.click(box('222'));
		await user.click(box('333'));
		await user.click(box('Front desk'));
		await user.click(screen.getByRole('button', { name: 'actions.save' }));
		await vi.waitFor(() => expect(onsaved).toHaveBeenCalled());
		expect(writes()).toEqual([
			{
				method: 'POST',
				path: '/api/panel/strategies/r1/assign',
				body: { peers: ['333'], users: [], groups: ['g1'] }
			},
			{
				method: 'POST',
				path: '/api/panel/strategies/unassign',
				body: { peers: ['222'], users: [], groups: [] }
			}
		]);
	});

	test('unticking everything takes the rule off every target', async () => {
		const user = userEvent.setup();
		const onsaved = vi.fn();
		render(StrategyAssignSheet, { rule, groups, devices, onclose: () => {}, onsaved });
		await vi.waitFor(() => expect(box('111')).toBeChecked());
		await user.click(box('111'));
		await user.click(box('222'));
		await user.click(box('anna'));
		await user.click(screen.getByRole('button', { name: 'actions.save' }));
		await vi.waitFor(() => expect(onsaved).toHaveBeenCalled());
		expect(writes()).toEqual([
			{
				method: 'POST',
				path: '/api/panel/strategies/unassign',
				body: { peers: ['111', '222'], users: ['anna'], groups: [] }
			}
		]);
	});

	test('a failed load of the current targets is shown and nothing can be saved', async () => {
		detailStatus = 403;
		const user = userEvent.setup();
		render(StrategyAssignSheet, { rule, groups, devices, onclose: () => {}, onsaved: () => {} });
		await vi.waitFor(() => expect(screen.getByRole('status')).toHaveTextContent(/\S/));
		await user.click(screen.getByRole('button', { name: 'actions.save' }));
		expect(writes()).toEqual([]);
	});
});
