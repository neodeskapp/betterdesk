/**
 * Sign in as (read-only): an admin with user.impersonate views the console
 * as an account with fewer rights. The server decides who may view whom,
 * refuses every write and every read of a secret while it lasts, and ends
 * it after 15 minutes (30 once made longer).
 *
 * The browser keeps the admin's own session; the impersonation is a second
 * cookie. Starting and stopping reload the console, and the other tabs of
 * the browser load again too (they share the cookies).
 */

import * as v from 'valibot';
import { api, wrapped, Ok, Text } from './client.ts';

export const ImpersonationCheck = v.looseObject({
	allowed: v.boolean(),
	code: v.optional(v.string(), ''),
	error: v.optional(v.string(), ''),
	username: Text,
	needs: v.looseObject({ password: v.boolean(), code: v.boolean() }),
	minutes: v.optional(v.number(), 15),
	max_minutes: v.optional(v.number(), 30),
	notice_mail: v.optional(v.boolean(), false)
});
export type ImpersonationCheck = v.InferOutput<typeof ImpersonationCheck>;

export const ImpersonationStatus = v.looseObject({
	active: v.boolean(),
	username: v.optional(v.string(), ''),
	admin_username: v.optional(v.string(), ''),
	reason: v.optional(v.string(), ''),
	started_at: v.optional(v.number(), 0),
	expires_at: v.optional(v.number(), 0),
	can_extend: v.optional(v.boolean(), false)
});
export type ImpersonationStatus = v.InferOutput<typeof ImpersonationStatus>;

const userPath = (id: number | string, rest: string) =>
	`/api/users/${encodeURIComponent(String(id))}/${rest}`;

/** Whether the signed-in admin may view NeoDesk as the account, and what the start asks for */
export function checkImpersonation(id: number | string, signal?: AbortSignal): Promise<ImpersonationCheck> {
	return api(wrapped(ImpersonationCheck), userPath(id, 'impersonation'), { signal });
}

/** Start: the admin's own password (and code) and a reason */
export function startImpersonation(
	id: number | string,
	body: { password?: string; code?: string; reason: string }
): Promise<ImpersonationStatus> {
	return api(wrapped(ImpersonationStatus), userPath(id, 'impersonate'), { method: 'POST', body });
}

/** 15 minutes more, once */
export function extendImpersonation(): Promise<ImpersonationStatus> {
	return api(wrapped(ImpersonationStatus), '/api/impersonation/extend', { method: 'POST', body: {} });
}

/** Back to the admin's own session */
export function stopImpersonation(): Promise<unknown> {
	return api(Ok, '/api/impersonation/stop', { method: 'POST', body: {} });
}

const Settings = v.looseObject({ notice_mail: v.boolean() });

/** Settings › Sign-in: mail the person viewed (on by default) */
export function getImpersonationSettings(): Promise<{ notice_mail: boolean }> {
	return api(wrapped(Settings), '/api/settings/impersonation');
}

export function saveImpersonationSettings(noticeMail: boolean): Promise<{ notice_mail: boolean }> {
	return api(wrapped(Settings), '/api/settings/impersonation', {
		method: 'PUT',
		body: { notice_mail: noticeMail }
	});
}

/* ── The other tabs of the browser share the cookies: they load again ── */

const CHANNEL = 'neodesk-impersonation';

/** Tell the other tabs that this browser started or stopped viewing as someone */
export function announceImpersonationChange(): void {
	try {
		const ch = new BroadcastChannel(CHANNEL);
		ch.postMessage({ type: 'changed' });
		ch.close();
	} catch {
		/* no BroadcastChannel: the other tabs find out at their next request */
	}
}

/** Load again when another tab started or stopped; returns the stop function */
export function onImpersonationChange(reload: () => void): () => void {
	try {
		const ch = new BroadcastChannel(CHANNEL);
		ch.onmessage = () => reload();
		return () => ch.close();
	} catch {
		return () => {};
	}
}
