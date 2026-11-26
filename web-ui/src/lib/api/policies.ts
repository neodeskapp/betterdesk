/**
 * Organization policies (routes/policies.routes.js, proxied to the Go
 * server's /api/org/{id}/policy): five groups per organization, each saved
 * on its own, and the change log the Go server keeps in memory.
 *
 * listOrgs and the two counts read /api/panel/org (routes/organizations.routes.js);
 * they are here only for the Policies page and can move to an
 * organizations API file when that page is ported.
 */

import * as v from 'valibot';
import { api, request } from './client.ts';
import { listIn, Str } from './lists.ts';

const enc = encodeURIComponent;

const Id = v.pipe(v.union([v.string(), v.number()]), v.transform(String));

export const Org = v.looseObject({ id: Id, name: Str });
export type Org = v.InferOutput<typeof Org>;

/** The Go server answers { organizations }, older builds a bare list or { data } */
const OrgList = listIn(Org, 'organizations', 'data');

export async function listOrgs(signal?: AbortSignal): Promise<Org[]> {
	return api(OrgList, '/api/panel/org', { signal });
}

const Group = v.optional(v.nullable(v.record(v.string(), v.unknown())), null);

/** One organization's policies by group; a group never saved is {} (or missing) */
export const Policies = v.looseObject({
	connection: Group,
	features: Group,
	security: Group,
	network: Group,
	update: Group
});
export type Policies = v.InferOutput<typeof Policies>;

const PoliciesAnswer = v.pipe(
	v.unknown(),
	v.transform((o) => {
		const inner = o && typeof o === 'object' ? (o as Record<string, unknown>).data : undefined;
		return inner && typeof inner === 'object' && !Array.isArray(inner) ? inner : o;
	}),
	Policies
);

export async function getPolicies(orgId: string, signal?: AbortSignal): Promise<Policies> {
	return api(PoliciesAnswer, `/api/panel/policies/${enc(orgId)}`, { signal });
}

/** PUT one group; the server stores the body as it is */
export async function savePolicyGroup(
	orgId: string,
	group: string,
	body: Record<string, unknown>
): Promise<void> {
	await request(`/api/panel/policies/${enc(orgId)}/${enc(group)}`, { method: 'PUT', body });
}

/** Clear one group: none of its settings has a rule any more */
export async function clearPolicyGroup(orgId: string, group: string): Promise<void> {
	await request(`/api/panel/policies/${enc(orgId)}/${enc(group)}`, { method: 'DELETE' });
}

/**
 * Where the value in force of a setting comes from: set for this
 * organization, the server-wide setting, or no rule (each device keeps its
 * own setting)
 */
export type PolicySource = 'organization' | 'server' | 'none';

export const ResolvedField = v.looseObject({
	/** The stored value, null when the organization sets none */
	value: v.optional(v.unknown(), null),
	source: v.pipe(
		v.optional(v.nullable(v.string()), null),
		v.transform((s): PolicySource => (s === 'organization' || s === 'server' ? s : 'none'))
	),
	/** The server-wide value, only where a server-wide setting exists */
	server_value: v.optional(v.unknown()),
	/** Where the server-wide setting is set: label_key is a translation key */
	server_setting: v.optional(v.looseObject({ label_key: v.string(), href: v.string() }))
});
export type ResolvedField = v.InferOutput<typeof ResolvedField>;

export const ResolvedGroup = v.looseObject({
	/** Anything stored for the group */
	set: v.boolean(),
	/** Who applies the group: the server itself, or the device that asks for its policy */
	applied_by: v.picklist(['server', 'device']),
	/** A device app applies the group (false while no client release reads it) */
	device_app_applies: v.boolean(),
	/** When a change takes effect: "server_minute" or "device_next_check" */
	timing: v.string(),
	fields: v.record(v.string(), ResolvedField)
});
export type ResolvedGroup = v.InferOutput<typeof ResolvedGroup>;

export const ResolvedPolicies = v.record(v.string(), ResolvedGroup);
export type ResolvedPolicies = v.InferOutput<typeof ResolvedPolicies>;

/** Each group with the value in force of each of its settings and where it comes from */
export async function getResolvedPolicies(orgId: string, signal?: AbortSignal): Promise<ResolvedPolicies> {
	return api(ResolvedPolicies, `/api/panel/policies/${enc(orgId)}/resolved`, { signal });
}

export const AuditEntry = v.looseObject({
	timestamp: Str,
	created_at: Str,
	action: Str,
	actor: Str,
	user: Str,
	target: Str,
	section: Str,
	category: Str,
	detail: Str,
	description: Str,
	details: v.optional(v.nullable(v.record(v.string(), v.unknown())), null)
});
export type AuditEntry = v.InferOutput<typeof AuditEntry>;

/** Go answers { entries } (null when there are none) */
const AuditAnswer = listIn(AuditEntry, 'entries', 'data');

export async function getPolicyAudit(orgId: string, signal?: AbortSignal): Promise<AuditEntry[]> {
	return api(AuditAnswer, `/api/panel/policies/${enc(orgId)}/audit`, { signal });
}

const Listed = (key: string) =>
	v.pipe(
		listIn(v.unknown(), key),
		v.transform((a) => a.length)
	);

/** Devices in the organization (any signed-in user) */
export async function countOrgDevices(orgId: string, signal?: AbortSignal): Promise<number> {
	return api(Listed('devices'), `/api/panel/org/${enc(orgId)}/devices`, { signal });
}

/** People in the organization (needs user.view and org.manage_users) */
export async function countOrgUsers(orgId: string, signal?: AbortSignal): Promise<number> {
	return api(Listed('users'), `/api/panel/org/${enc(orgId)}/users`, { signal });
}
