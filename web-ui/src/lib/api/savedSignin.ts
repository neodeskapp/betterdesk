/**
 * Saved sign-in of a device (BUGS L469): the account name and password of
 * the computer's own account, which the browser viewer types at its lock
 * or sign-in screen. The password is write-only: set, replaced and
 * cleared here, never read back. useSignin is the one call whose answer
 * holds it, once per open viewer session; its answer goes straight to the
 * typing and is never kept in page state or logged.
 */

import * as v from 'valibot';
import { api, request, ApiError } from './client.ts';

const enc = encodeURIComponent;

export const SigninForm = v.picklist(['password', 'name_password']);
export type SigninForm = v.InferOutput<typeof SigninForm>;

export const SigninStatus = v.looseObject({
	/** The server has its key for saved sign-in details */
	available: v.optional(v.boolean(), false),
	set: v.optional(v.boolean(), false),
	username: v.optional(v.string(), ''),
	form: v.optional(v.fallback(SigninForm, 'password'), 'password'),
	auto: v.optional(v.boolean(), false),
	updated_by: v.optional(v.string(), ''),
	updated_at: v.optional(v.string(), ''),
	/** This account may use them in the viewer (Use saved sign-in and Connect to devices) */
	may_use: v.optional(v.boolean(), false)
});
export type SigninStatus = v.InferOutput<typeof SigninStatus>;

/** Whether details are saved, for which account, the form, the switch, who and when */
export function getSignin(id: string, signal?: AbortSignal): Promise<SigninStatus> {
	return api(SigninStatus, `/api/devices/${enc(id)}/signin`, { signal });
}

export interface SigninInput {
	username: string;
	password: string;
	form: SigninForm;
}

export function setSignin(id: string, input: SigninInput): Promise<SigninStatus> {
	return api(SigninStatus, `/api/devices/${enc(id)}/signin`, { method: 'PUT', body: input });
}

export function changeSignin(
	id: string,
	change: { auto?: boolean; form?: SigninForm }
): Promise<SigninStatus> {
	return api(SigninStatus, `/api/devices/${enc(id)}/signin`, { method: 'PATCH', body: change });
}

export function clearSignin(id: string): Promise<unknown> {
	return request(`/api/devices/${enc(id)}/signin`, { method: 'DELETE' });
}

/** The details for one use; never store them */
export interface SigninDetails {
	username: string;
	password: string;
	form: SigninForm;
}

/**
 * The saved details for the viewer's open session (the relay session's
 * id), once. A refusal throws ApiError with the server's code
 * (refusalCode): token_not_allowed, session_not_open, already_used,
 * not_set, cannot_open, vault_unavailable.
 */
export async function useSignin(id: string, session: string): Promise<SigninDetails> {
	const data = await request(`/api/devices/${enc(id)}/signin/use`, { method: 'POST', body: { session } });
	const o = data && typeof data === 'object' ? (data as Record<string, unknown>) : {};
	const username = typeof o.username === 'string' ? o.username : '';
	const password = typeof o.password === 'string' ? o.password : '';
	const form: SigninForm = o.form === 'name_password' ? 'name_password' : 'password';
	if (!password) throw new ApiError('', 200, { code: 'invalid_response' });
	return { username, password, form };
}

/** The server's code of a refused call, '' for none */
export function refusalCode(err: unknown): string {
	if (!(err instanceof ApiError)) return '';
	const data = err.data && typeof err.data === 'object' ? (err.data as Record<string, unknown>) : {};
	return err.code || (typeof data.error === 'string' ? data.error : '');
}
