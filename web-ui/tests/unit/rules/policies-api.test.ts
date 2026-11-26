/**
 * The value in force of each policy setting, and clearing one group.
 */
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { setCsrfToken } from '../../../src/lib/api/client.ts';
import { clearPolicyGroup, getResolvedPolicies } from '../../../src/lib/api/policies.ts';

type Call = { url: string; init: RequestInit };
let calls: Call[];
let reply: unknown;

beforeEach(() => {
	calls = [];
	vi.stubGlobal('fetch', (url: string, init: RequestInit) => {
		calls.push({ url, init });
		return Promise.resolve(
			new Response(JSON.stringify(reply), { status: 200, headers: { 'content-type': 'application/json' } })
		);
	});
	setCsrfToken('tok-1');
});
afterEach(() => vi.unstubAllGlobals());

const setting = { label_key: 'settings.connection_mode_title', href: '/settings#connection-mode-section' };

describe('getResolvedPolicies', () => {
	test('reads each group with its fields and where their values come from', async () => {
		reply = {
			network: {
				set: true,
				applied_by: 'server',
				device_app_applies: false,
				timing: 'server_minute',
				fields: {
					block_direct_p2p: { value: null, source: 'server', server_value: true, server_setting: setting },
					ip_allowlist: { value: ['10.0.0.0/8'], source: 'organization' }
				}
			},
			update: {
				set: false,
				applied_by: 'device',
				device_app_applies: false,
				timing: 'device_next_check',
				fields: { channel: { value: null, source: 'none' } }
			}
		};
		const out = await getResolvedPolicies('org 1');
		expect(calls[0]?.url).toBe('/api/panel/policies/org%201/resolved');
		expect(out.network?.applied_by).toBe('server');
		expect(out.network?.timing).toBe('server_minute');
		expect(out.network?.fields.block_direct_p2p).toEqual({
			value: null,
			source: 'server',
			server_value: true,
			server_setting: setting
		});
		expect(out.network?.fields.ip_allowlist?.value).toEqual(['10.0.0.0/8']);
		expect(out.update).toEqual({
			set: false,
			applied_by: 'device',
			device_app_applies: false,
			timing: 'device_next_check',
			fields: { channel: { value: null, source: 'none' } }
		});
	});

	test('a source the client does not know is no rule', async () => {
		reply = {
			update: {
				set: false,
				applied_by: 'device',
				device_app_applies: false,
				timing: 'device_next_check',
				fields: { channel: { value: null, source: 'later' } }
			}
		};
		expect((await getResolvedPolicies('o')).update?.fields.channel?.source).toBe('none');
	});
});

describe('clearPolicyGroup', () => {
	test('deletes one group', async () => {
		reply = { success: true };
		await clearPolicyGroup('org 1', 'network');
		expect(calls).toHaveLength(1);
		expect(calls[0]?.url).toBe('/api/panel/policies/org%201/network');
		expect(calls[0]?.init.method).toBe('DELETE');
	});
});
