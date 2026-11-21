/**
 * The device group's support package sheet: a new package is assigned by
 * the id its create answer carries, also when an older package has the
 * same name, and a retry after a failed assign creates no second one.
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import GroupBillingSheet from '../../src/routes/(console)/devices/GroupBillingSheet.svelte';
import type { DeviceGroup } from '../../src/lib/api/devices.ts';
import { dismissAll } from '../../src/lib/ui/toasts.svelte.ts';

interface Call {
	method: string;
	path: string;
	body: unknown;
}
let calls: Call[] = [];
let contractStatus = 200;

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
	contractStatus = 200;
	vi.stubGlobal('fetch', async (input: string, init: RequestInit = {}) => {
		const url = new URL(input, location.origin);
		const method = init.method ?? 'GET';
		const body = typeof init.body === 'string' ? (JSON.parse(init.body) as unknown) : undefined;
		calls.push({ method, path: url.pathname, body });
		if (url.pathname === '/api/panel/billing/packages' && method === 'GET')
			return reply(200, { packages: [{ id: 'old', name: 'Gold', included_minutes: 60 }] }, url.href);
		if (url.pathname === '/api/panel/billing/packages')
			return reply(201, { id: 'new', name: 'Gold', included_minutes: 600 }, url.href);
		if (url.pathname === '/api/panel/billing/contracts')
			return reply(contractStatus, contractStatus < 300 ? { id: 'c1' } : { error: 'boom' }, url.href);
		return reply(404, {}, url.href);
	});
});

afterEach(() => {
	cleanup();
	dismissAll();
	vi.unstubAllGlobals();
});

const writes = () => calls.filter((c) => c.method !== 'GET');

describe('support package for a device group', () => {
	test('a new package with a name that exists is assigned by its own id, once, also after a failed assign', async () => {
		contractStatus = 400;
		const user = userEvent.setup();
		const onclose = vi.fn();
		const group = { guid: 'g1', name: 'Front desk' } as DeviceGroup;
		render(GroupBillingSheet, { group, onclose });
		await user.click(
			await screen.findByRole('radio', { name: /devices.group_commercialization_create_new/ })
		);
		await user.type(screen.getByLabelText('commercialization.packages.name'), 'Gold');
		await user.click(screen.getByRole('button', { name: 'actions.save' }));
		await vi.waitFor(() => expect(writes()).toHaveLength(2));

		contractStatus = 200;
		await user.click(screen.getByRole('button', { name: 'actions.save' }));
		await vi.waitFor(() => expect(onclose).toHaveBeenCalled());
		expect(writes().map((c) => [c.path, (c.body as { package_id?: string }).package_id])).toEqual([
			['/api/panel/billing/packages', undefined],
			['/api/panel/billing/contracts', 'new'],
			['/api/panel/billing/contracts', 'new']
		]);
	});
});
