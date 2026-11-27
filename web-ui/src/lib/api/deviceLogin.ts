/**
 * Sign in with NeoDesk (/authorize-device/<id>): the desktop app opens
 * this page, and the person signed in here allows or denies the sign-in
 * of that computer to their account. The server keeps the request for
 * five minutes and decides it once.
 *
 * GET answers the request, or state "expired" for one that is unknown
 * or over. Approve and deny answer the decided request; 404 when it is
 * over, 409 already_decided with its state, 409 with the account's
 * reason (account_disabled, totp_setup_required,
 * password_change_required) when the account may not sign in to the app
 * now (the request stays open), 409 impersonating while viewing NeoDesk
 * as another account.
 */

import * as v from 'valibot';
import { api } from './client.ts';

const Str = v.pipe(
	v.optional(v.nullable(v.string()), ''),
	v.transform((s) => s ?? '')
);

export const DeviceLoginState = v.picklist(['pending', 'allowed', 'declined', 'expired']);
export type DeviceLoginState = v.InferOutput<typeof DeviceLoginState>;

export const DeviceLogin = v.looseObject({
	state: v.fallback(DeviceLoginState, 'expired'),
	device_id: Str,
	device_name: Str,
	device_os: Str,
	ip: Str,
	created_at: Str,
	expires_at: Str,
	/** The console account Allow signs the computer in as */
	account: Str
});
export type DeviceLogin = v.InferOutput<typeof DeviceLogin>;

export function getDeviceLogin(id: string, signal?: AbortSignal): Promise<DeviceLogin> {
	return api(DeviceLogin, `/api/device-login/${encodeURIComponent(id)}`, { signal });
}

export function decideDeviceLogin(id: string, allow: boolean): Promise<DeviceLogin> {
	return api(DeviceLogin, `/api/device-login/${encodeURIComponent(id)}/${allow ? 'approve' : 'deny'}`, {
		method: 'POST'
	});
}
