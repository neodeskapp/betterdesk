/**
 * Data loss prevention (routes/dataguard.routes.js at /api/dataguard):
 * policies (read with device.view, written with server.config) and the
 * events devices report (audit.view).
 */

import * as v from 'valibot';
import { api, request } from './client.ts';
import { listIn, Str, Bool } from './lists.ts';

const enc = encodeURIComponent;
const Id = v.pipe(v.union([v.number(), v.string()]), v.transform(String));
const NullableId = v.pipe(
	v.optional(v.nullable(v.union([v.number(), v.string()])), null),
	v.transform((x) => (x === null || x === '' ? null : String(x)))
);

export const DlpPolicy = v.looseObject({
	id: Id,
	name: Str,
	description: Str,
	policy_type: Str,
	type: Str,
	action: Str,
	scope: Str,
	enabled: Bool,
	/** A JSON list of conditions; older rows may hold a string */
	rules: v.optional(v.unknown())
});
export type DlpPolicy = v.InferOutput<typeof DlpPolicy>;

export async function listDlpPolicies(signal?: AbortSignal): Promise<DlpPolicy[]> {
	return api(listIn(DlpPolicy, 'policies', 'data'), '/api/dataguard/policies', { signal });
}

export async function getDlpPolicy(id: string, signal?: AbortSignal): Promise<DlpPolicy> {
	return api(DlpPolicy, `/api/dataguard/policies/${enc(id)}`, { signal });
}

export interface DlpPolicyBody {
	name: string;
	description: string;
	policy_type: string;
	action: string;
	scope: string | null;
	rules: unknown[];
	enabled: boolean;
}

export async function createDlpPolicy(body: DlpPolicyBody): Promise<void> {
	await request('/api/dataguard/policies', { method: 'POST', body });
}

export async function updateDlpPolicy(id: string, body: DlpPolicyBody): Promise<void> {
	await request(`/api/dataguard/policies/${enc(id)}`, { method: 'PATCH', body });
}

export async function deleteDlpPolicy(id: string): Promise<void> {
	await request(`/api/dataguard/policies/${enc(id)}`, { method: 'DELETE' });
}

export const DlpEvent = v.looseObject({
	id: v.optional(Id),
	device_id: Str,
	event_source: Str,
	event_type: Str,
	policy_id: NullableId,
	policy_name: Str,
	action: Str,
	details: v.optional(v.unknown()),
	created_at: Str
});
export type DlpEvent = v.InferOutput<typeof DlpEvent>;

/** Events per request: one page of the event table */
export const DLP_EVENT_PAGE = 100;

/**
 * One page of events, newest first: at most `limit`, and with `to` only
 * those reported at that time or before (the oldest time of the page
 * before, so the events of that very time come again and are dropped by
 * the caller).
 */
export async function listDlpEvents(
	opts: { limit?: number; to?: string } = {},
	signal?: AbortSignal
): Promise<DlpEvent[]> {
	const query: Record<string, string> = { limit: String(opts.limit ?? DLP_EVENT_PAGE) };
	if (opts.to) query.to = opts.to;
	return api(listIn(DlpEvent, 'events', 'data'), '/api/dataguard/events', { query, signal });
}

const Count = v.pipe(
	v.optional(v.nullable(v.union([v.number(), v.string()])), null),
	v.transform((n) => {
		const num = Number(n ?? 0);
		return Number.isFinite(num) ? num : 0;
	})
);

export const DlpStats = v.looseObject({
	total: Count,
	blocked: Count,
	logged: Count,
	/** Events per policy id; null when the server does not count them */
	by_policy: v.optional(v.nullable(v.record(v.string(), Count)), null)
});
export type DlpStats = v.InferOutput<typeof DlpStats>;

/** The number of events of all time, by action and per policy */
export async function getDlpStats(signal?: AbortSignal): Promise<DlpStats> {
	return api(DlpStats, '/api/dataguard/stats', { signal });
}
