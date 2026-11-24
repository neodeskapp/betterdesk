/**
 * Saved sign-in of a device (BUGS L469): the account name and password of
 * the computer's own account, which the browser viewer types at its lock
 * or sign-in screen. The password is write-only: set, replaced and
 * cleared here, never read back. useSealedSignin answers it sealed for the
 * computer's own NeoDesk; useSignin is the one call whose answer holds it
 * readable, for clients that can't open sealed packets, once per open
 * viewer session; its answer goes straight to the typing and is never kept
 * in page state or logged.
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
	may_use: v.optional(v.boolean(), false),
	/** The computer's NeoDesk signs in with sealed packets; the browser never gets the password */
	sealed_signin: v.optional(v.boolean(), false)
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

/** A sign-in sealed for the computer's NeoDesk: the page forwards the bytes and can't read them */
export interface SealedSignin {
	packet: Uint8Array;
	nonce: string;
	notAfter: number;
}

function fromBase64(text: string): Uint8Array {
	const raw = atob(text);
	const out = new Uint8Array(raw.length);
	for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
	return out;
}

async function use(id: string, session: string, mode: 'sealed' | 'typed'): Promise<Record<string, unknown>> {
	const data = await request(`/api/devices/${enc(id)}/signin/use`, {
		method: 'POST',
		body: { session, mode }
	});
	return data && typeof data === 'object' ? (data as Record<string, unknown>) : {};
}

/**
 * The saved details for the viewer's open session (the relay session's
 * id), once, for the viewer to type. A refusal throws ApiError with the
 * server's code (refusalCode): token_not_allowed, session_not_open,
 * already_used, not_set, cannot_open, vault_unavailable, sealed_required.
 */
export async function useSignin(id: string, session: string): Promise<SigninDetails> {
	const o = await use(id, session, 'typed');
	const username = typeof o.username === 'string' ? o.username : '';
	const password = typeof o.password === 'string' ? o.password : '';
	const form: SigninForm = o.form === 'name_password' ? 'name_password' : 'password';
	if (!password) throw new ApiError('', 200, { code: 'invalid_response' });
	return { username, password, form };
}

/**
 * The saved sign-in sealed for the computer's NeoDesk, for the viewer's
 * open session, once: the password never reaches this page. Refusals as
 * useSignin, and no_device_key when the server has no key of the computer.
 */
export async function useSealedSignin(id: string, session: string): Promise<SealedSignin> {
	const o = await use(id, session, 'sealed');
	const packet = typeof o.sealed === 'string' ? fromBase64(o.sealed) : new Uint8Array(0);
	if (o.path !== 'sealed' || packet.length < 48) throw new ApiError('', 200, { code: 'invalid_response' });
	return {
		packet,
		nonce: typeof o.nonce === 'string' ? o.nonce : '',
		notAfter: typeof o.not_after === 'number' ? o.not_after : 0
	};
}

/** The server's code of a refused call, '' for none */
export function refusalCode(err: unknown): string {
	if (!(err instanceof ApiError)) return '';
	const data = err.data && typeof err.data === 'object' ? (err.data as Record<string, unknown>) : {};
	return err.code || (typeof data.error === 'string' ? data.error : '');
}
