/**
 * The role list with what each permission needs, and a batch of changes
 * to one role whose answer carries what undoes it.
 */
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { setCsrfToken } from '../../../src/lib/api/client.ts';
import { listRoles, saveRoleChanges } from '../../../src/lib/api/roles.ts';

type Call = { url: string; init: RequestInit };
let calls: Call[];
let reply: unknown;
let status: number;

beforeEach(() => {
	calls = [];
	status = 200;
	vi.stubGlobal('fetch', (url: string, init: RequestInit) => {
		calls.push({ url, init });
		return Promise.resolve(
			new Response(JSON.stringify(reply), { status, headers: { 'content-type': 'application/json' } })
		);
	});
	setCsrfToken('tok-1');
});
afterEach(() => vi.unstubAllGlobals());

describe('listRoles', () => {
	test('names what each permission needs, with the reason', async () => {
		reply = {
			success: true,
			data: {
				roles: [{ name: 'operator', permissions: ['device.view'] }],
				all_permissions: ['device.view', 'device.edit'],
				requires: { 'device.edit': [{ permission: 'device.view', reason: 'devices_page' }] }
			}
		};
		const out = await listRoles();
		expect(out.all).toEqual(['device.view', 'device.edit']);
		expect(out.roles[0]?.name).toBe('operator');
		expect(out.requires).toEqual({ 'device.edit': [{ permission: 'device.view', reason: 'devices_page' }] });
	});

	test('a server without the table needs nothing', async () => {
		reply = { success: true, data: { roles: [], all_permissions: [] } };
		expect((await listRoles()).requires).toEqual({});
	});
});

describe('saveRoleChanges', () => {
	test('sends the batch and answers what undoes it and the permissions after', async () => {
		reply = {
			success: true,
			data: {
				previous: [
					{ permission: 'device.edit', granted: null },
					{ permission: 'device.view', granted: false }
				],
				permissions: ['device.view', 'device.edit']
			}
		};
		const out = await saveRoleChanges('operator', [
			{ permission: 'device.edit', granted: true },
			{ permission: 'device.view', granted: null }
		]);
		expect(calls).toHaveLength(1);
		expect(calls[0]?.url).toBe('/api/panel/role-permissions/batch');
		expect(calls[0]?.init.method).toBe('POST');
		expect(JSON.parse(String(calls[0]?.init.body))).toEqual({
			role: 'operator',
			changes: [
				{ permission: 'device.edit', granted: true },
				{ permission: 'device.view', granted: null }
			]
		});
		expect(out).toEqual({
			previous: [
				{ permission: 'device.edit', granted: null },
				{ permission: 'device.view', granted: false }
			],
			permissions: ['device.view', 'device.edit']
		});
	});

	test('a refused batch is an error with the server reason', async () => {
		status = 400;
		reply = { success: false, error: 'user.create is fixed for the role operator and cannot be changed' };
		await expect(saveRoleChanges('operator', [{ permission: 'user.create', granted: true }])).rejects.toThrow(
			'user.create is fixed for the role operator and cannot be changed'
		);
	});
});
