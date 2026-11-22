/**
 * Redeeming an organization invitation (/invite): the two calls a person
 * makes with the code from the invitation link, signed in to the console
 * or not. Neither needs a session (allowAnonymous: a 401 is no reason to
 * leave the page); both count against the server's limit per address.
 *
 * A code that is unknown, used, expired or revoked gets 404 from both,
 * with no word on which; redeeming then names no organization either.
 */

import * as v from 'valibot';
import { api } from './client.ts';

const Str = v.pipe(
	v.optional(v.nullable(v.union([v.string(), v.number()])), ''),
	v.transform((s) => (s === null || s === undefined ? '' : String(s)))
);

const InvitationOrg = v.object({ name: Str, slug: Str });

export const InvitationLookup = v.looseObject({
	success: v.optional(v.boolean()),
	organization: InvitationOrg,
	expires_at: Str,
	/** The console account this browser is signed in with, null without one */
	account: v.optional(v.nullable(v.looseObject({ username: Str })), null)
});
export type InvitationLookup = v.InferOutput<typeof InvitationLookup>;

/** What the invitation is for; throws ApiError 404 for a code that cannot be used */
export function lookupInvitation(token: string, signal?: AbortSignal): Promise<InvitationLookup> {
	return api(InvitationLookup, '/api/invitations/lookup', {
		method: 'POST',
		body: { token },
		signal,
		allowAnonymous: true,
		activity: false
	});
}

export const Redeemed = v.looseObject({
	success: v.optional(v.boolean()),
	organization: InvitationOrg,
	username: Str
});
export type Redeemed = v.InferOutput<typeof Redeemed>;

/**
 * Makes the organization sign-in and uses the invitation up. 404: the code
 * cannot be used (any more); 409: the organization has the username; 400:
 * the username or the password break the rules (the server's sentence).
 */
export function redeemInvitation(body: {
	token: string;
	username: string;
	password: string;
}): Promise<Redeemed> {
	return api(Redeemed, '/api/invitations/redeem', { method: 'POST', body, allowAnonymous: true });
}
