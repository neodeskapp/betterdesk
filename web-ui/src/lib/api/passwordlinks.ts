/**
 * The page a password link leads to (/set-password): a link from an invite
 * or a password reset mail. Neither call needs a session (allowAnonymous:
 * a 401 is no reason to leave the page); both count against the server's
 * limit per address. A link that is unknown, used or expired gets 404 from
 * both, with no word on which.
 */

import * as v from 'valibot';
import { api, wrapped } from './client.ts';

const Str = v.pipe(
	v.optional(v.nullable(v.union([v.string(), v.number()])), ''),
	v.transform((s) => (s === null || s === undefined ? '' : String(s)))
);

export const PasswordLink = v.looseObject({
	username: Str,
	/** invite: a new account; reset: a new password */
	purpose: Str,
	expires_at: Str
});
export type PasswordLink = v.InferOutput<typeof PasswordLink>;

/** Which account the link is for; throws ApiError 404 for one that cannot be used */
export function lookupPasswordLink(token: string, signal?: AbortSignal): Promise<PasswordLink> {
	return api(wrapped(PasswordLink), '/api/auth/password-link/lookup', {
		method: 'POST',
		body: { token },
		signal,
		allowAnonymous: true,
		activity: false
	});
}

/**
 * Sets the password and uses the link up. 404: the link cannot be used
 * (any more); 400: the password breaks the rules (the server's sentence).
 */
export function redeemPasswordLink(body: { token: string; password: string }): Promise<{ username: string }> {
	return api(wrapped(v.looseObject({ username: Str })), '/api/auth/password-link/redeem', {
		method: 'POST',
		body,
		allowAnonymous: true
	});
}

/** Whether the sign-in page offers Forgot password (the console can send mail) */
export async function passwordResetAvailable(signal?: AbortSignal): Promise<boolean> {
	const out = await api(
		wrapped(v.looseObject({ available: v.optional(v.boolean(), false) })),
		'/api/auth/password-reset/available',
		{
			signal,
			allowAnonymous: true,
			activity: false
		}
	);
	return out.available;
}

/**
 * Asks for a reset link for a username or an email address. The answer is
 * the same whether an account was found or not; a found one gets a link
 * that works for an hour.
 */
export function requestPasswordReset(login: string) {
	return api(v.looseObject({ success: v.optional(v.boolean()) }), '/api/auth/password-reset/request', {
		method: 'POST',
		body: { login },
		allowAnonymous: true
	});
}

/** The token of a link: what follows # (the browser never sends it), or a whole link pasted */
export function linkToken(value: string): string {
	const text = value.trim();
	const hash = text.indexOf('#');
	const token = hash >= 0 ? text.slice(hash + 1) : text;
	return /^[A-Za-z0-9_-]{20,200}$/.test(token) ? token : '';
}
