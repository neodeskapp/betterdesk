/**
 * Signed-in devices (A239): the console sessions of an account. An account
 * may be signed in on several browsers at once; the account menu lists its
 * own, the user's page lists another account's (user.edit). Each entry is
 * named by a handle, never by the session's id.
 */

import * as v from 'valibot';
import { api, wrapped, NumLike, Ok, Text } from './client.ts';

const Millis = v.pipe(
	v.optional(v.nullable(NumLike), 0),
	v.transform((n) => n ?? 0)
);

export const SignedInDevice = v.looseObject({
	id: v.string(),
	current: v.optional(v.boolean(), false),
	browser: Text,
	system: Text,
	ipAddress: Text,
	userAgent: Text,
	createdAt: Millis,
	lastActivityAt: Millis
});
export type SignedInDevice = v.InferOutput<typeof SignedInDevice>;

export const SessionLimit = v.looseObject({
	/** 0: any number */
	max: v.optional(v.number(), 0),
	/** env (SESSION_MAX_PER_USER), setting (One session per account) or default */
	source: v.optional(v.string(), 'default')
});
export type SessionLimit = v.InferOutput<typeof SessionLimit>;

export const SignedInDevices = v.looseObject({
	sessions: v.array(SignedInDevice),
	limit: v.optional(SessionLimit, { max: 0, source: 'default' })
});
export type SignedInDevices = v.InferOutput<typeof SignedInDevices>;

const Ended = v.looseObject({ success: v.optional(v.boolean()), ended: v.optional(v.number(), 0) });

const userPath = (userId: number | string) => `/api/users/${encodeURIComponent(String(userId))}/sessions`;

/** The signed-in account's own sessions */
export function listOwnSessions(signal?: AbortSignal): Promise<SignedInDevices> {
	return api(wrapped(SignedInDevices), '/api/users/me/sessions', { signal });
}

/** Sign out one of the account's other browsers; never this one (that is Sign out) */
export function endOwnSession(id: string) {
	return api(Ok, `/api/users/me/sessions/${encodeURIComponent(id)}`, { method: 'DELETE' });
}

/** Sign out everywhere else: every session of the account but this one */
export async function endOtherSessions(): Promise<number> {
	return (await api(Ended, '/api/users/me/sessions/end-others', { method: 'POST', body: {} })).ended;
}

/** Another account's sessions. Needs user.edit */
export function listUserSessions(userId: number | string, signal?: AbortSignal): Promise<SignedInDevices> {
	return api(wrapped(SignedInDevices), userPath(userId), { signal });
}

/** End one session of another account. Needs user.edit */
export function endUserSession(userId: number | string, id: string) {
	return api(Ok, `${userPath(userId)}/${encodeURIComponent(id)}`, { method: 'DELETE' });
}

/** End every session of another account (your own: all but this one). Needs user.edit */
export async function endAllUserSessions(userId: number | string): Promise<number> {
	return (await api(Ended, `${userPath(userId)}/end-all`, { method: 'POST', body: {} })).ended;
}

export const ConsoleSessions = v.looseObject({
	single_session: v.optional(v.boolean(), false),
	limit: v.optional(v.number(), 0),
	source: v.optional(v.string(), 'default'),
	/** SESSION_MAX_PER_USER as set on the server; it then decides over the switch */
	env_value: v.optional(v.nullable(v.string()), null)
});
export type ConsoleSessions = v.InferOutput<typeof ConsoleSessions>;

/** Settings, Sign-in: One session per account. Needs server.config */
export function getConsoleSessions(signal?: AbortSignal): Promise<ConsoleSessions> {
	return api(wrapped(ConsoleSessions), '/api/settings/console-sessions', { signal });
}

export function saveConsoleSessions(singleSession: boolean) {
	return api(Ok, '/api/settings/console-sessions', {
		method: 'PUT',
		body: { single_session: singleSession }
	});
}
