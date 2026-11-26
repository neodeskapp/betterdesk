/**
 * The RustDesk client gives each failure its reason where it starts
 * (L556): the signal server's PunchHoleResponse failure and refusal
 * values, and the device's login refusals. The viewer reads the reason,
 * never the message.
 */
import { beforeEach, describe, expect, test, vi } from 'vitest';
import { RDClient } from '../../../src/lib/remote/client/client.ts';
import { LoginResponse } from '../../../src/lib/remote/client/generated/protos.ts';
import { RDPunchHoleResponse } from '../../../src/lib/remote/client/punchhole-response.ts';

beforeEach(() => {
	vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(null);
});

async function client() {
	const c = new RDClient(document.createElement('canvas'), { deviceId: '828416401' });
	await c.proto.load();
	const errors: [unknown, Record<string, unknown>][] = [];
	const logins: unknown[] = [];
	c.on('error', (msg: unknown, meta: Record<string, unknown>) => errors.push([msg, meta]));
	c.on('login_error', (e: unknown) => logins.push(e));
	return { c, errors, logins };
}

describe('the signal server', () => {
	test.each([
		[{ failure: 2 }, 'device_offline'],
		[{ failure: 0 }, 'device_unknown'],
		[{ failure: 4 }, 'session_limit'],
		[
			{ failure: 0, otherFailure: 'The terminal is turned off for your account on this server' },
			'feature_off'
		]
	])('PunchHoleResponse %o is %s', (resp, reason) => {
		const out = RDPunchHoleResponse.interpretPunchHoleResponse(resp);
		expect(out).toMatchObject({ reason });
	});
});

describe("the device's login refusals", () => {
	test('a wrong password stays in the password form', async () => {
		const { c, errors, logins } = await client();
		c._handleLoginResponse(LoginResponse.fromObject({ error: 'Wrong Password' }));
		expect(logins).toEqual(['Wrong Password']);
		expect(errors).toEqual([]);
	});

	test('any other refusal is a failure with its reason, not a password error', async () => {
		const { c, errors, logins } = await client();
		c._handleLoginResponse(LoginResponse.fromObject({ error: 'Connection not allowed' }));
		expect(logins).toEqual([]);
		expect(errors[0]?.[1]).toMatchObject({ reason: 'device_not_allowed' });
	});

	test('a refusal the client has no code for is an unknown failure, with the words for Details', async () => {
		const { c, errors } = await client();
		c._handleLoginResponse(LoginResponse.fromObject({ error: 'Something the device said' }));
		expect(errors[0]?.[0]).toMatch(/Something the device said/);
		expect(errors[0]?.[1]).toMatchObject({ reason: 'unknown' });
	});
});
