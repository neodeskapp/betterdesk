/**
 * An account's recent sign-ins and security events, newest first: its own
 * on Your account, any account's on the user's page (user.view). The
 * server keeps the one list of actions it shows (routes/users/securityevents.go).
 */

import * as v from 'valibot';
import { api, wrapped, NumLike, Text } from './client.ts';

export const SecurityEvent = v.looseObject({
	id: NumLike,
	action: v.string(),
	/** When, as the server writes times */
	at: Text,
	/** The address of an entry the account wrote itself, "" for the others */
	ip: Text,
	/** Who did it when that was someone else */
	actor: Text,
	/** Why an admin viewed NeoDesk as the account */
	reason: Text
});
export type SecurityEvent = v.InferOutput<typeof SecurityEvent>;

const Events = v.looseObject({ events: v.array(SecurityEvent) });

export async function listOwnSecurityEvents(signal?: AbortSignal): Promise<SecurityEvent[]> {
	return (await api(wrapped(Events), '/api/users/me/security-events', { signal })).events;
}

export async function listUserSecurityEvents(
	id: number | string,
	signal?: AbortSignal
): Promise<SecurityEvent[]> {
	return (
		await api(wrapped(Events), `/api/users/${encodeURIComponent(String(id))}/security-events`, { signal })
	).events;
}
