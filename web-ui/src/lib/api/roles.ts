/**
 * Roles and permission overrides (routes/permissions.routes.js, a proxy to
 * the Go server's /api/roles and /api/role-permissions). Reading roles needs
 * user.view, reading overrides server.config; changing them needs a super
 * admin (commit 024540ff).
 */

import * as v from 'valibot';
import { api, Ok, wrapped } from './client.ts';

const Strings = v.pipe(
	v.optional(v.nullable(v.array(v.string())), null),
	v.transform((a) => a ?? [])
);

export const RoleInfo = v.looseObject({
	name: v.string(),
	level: v.pipe(
		v.optional(v.nullable(v.number()), 0),
		v.transform((n) => n ?? 0)
	),
	is_super_admin: v.pipe(
		v.optional(v.nullable(v.boolean()), false),
		v.transform((b) => !!b)
	),
	is_server_level: v.pipe(
		v.optional(v.nullable(v.boolean()), false),
		v.transform((b) => !!b)
	),
	/** The role's defaults */
	permissions: Strings,
	/** Permissions the code fixes for the role, each with the rule's name: a tick there has no effect */
	fixed: v.pipe(
		v.optional(v.nullable(v.record(v.string(), v.string())), null),
		v.transform((f) => f ?? {})
	)
});
export type RoleInfo = v.InferOutput<typeof RoleInfo>;

/** A permission another one needs to be of any use; reason is a key ("devices_page") */
export const PermissionNeed = v.looseObject({ permission: v.string(), reason: v.string() });
export type PermissionNeed = v.InferOutput<typeof PermissionNeed>;

/** For each permission the ones it needs; a permission that needs none is missing */
const Requires = v.pipe(
	v.optional(v.nullable(v.record(v.string(), v.array(PermissionNeed))), null),
	v.transform((r) => r ?? {})
);

export async function listRoles(
	signal?: AbortSignal
): Promise<{ roles: RoleInfo[]; all: string[]; requires: Record<string, PermissionNeed[]> }> {
	const out = await api(
		wrapped(
			v.looseObject({
				roles: v.optional(v.nullable(v.array(RoleInfo)), null),
				all_permissions: Strings,
				requires: Requires
			})
		),
		'/api/panel/roles',
		{ signal }
	);
	return { roles: out.roles ?? [], all: out.all_permissions, requires: out.requires };
}

/** What the role has now: defaults with the overrides applied */
export async function roleEffective(role: string, signal?: AbortSignal): Promise<string[]> {
	const out = await api(
		wrapped(v.looseObject({ permissions: Strings })),
		`/api/panel/roles/${encodeURIComponent(role)}/permissions`,
		{ signal }
	);
	return out.permissions;
}

export const RoleOverride = v.looseObject({
	role: v.string(),
	permission: v.string(),
	granted: v.pipe(
		v.union([v.boolean(), v.number()]),
		v.transform((b) => b === true || b === 1)
	)
});
export type RoleOverride = v.InferOutput<typeof RoleOverride>;

/** Needs server.config */
export async function listOverrides(signal?: AbortSignal): Promise<RoleOverride[]> {
	const out = await api(
		wrapped(v.looseObject({ overrides: v.optional(v.nullable(v.array(RoleOverride)), null) })),
		'/api/panel/role-permissions',
		{ signal }
	);
	return out.overrides ?? [];
}

/** Allow or deny one permission for a role. Needs a super admin */
export function setOverride(role: string, permission: string, granted: boolean) {
	return api(Ok, '/api/panel/role-permissions', { method: 'POST', body: { role, permission, granted } });
}

/** Back to the role's default for one permission. Needs a super admin */
export function clearOverride(role: string, permission: string) {
	return api(
		Ok,
		`/api/panel/role-permissions/${encodeURIComponent(role)}/${encodeURIComponent(permission)}`,
		{
			method: 'DELETE'
		}
	);
}

/** One change of a batch: allow (true), deny (false) or back to the role's default (null) */
export interface RoleChange {
	permission: string;
	granted: boolean | null;
}

const RoleChangeSchema = v.looseObject({ permission: v.string(), granted: v.nullable(v.boolean()) });

const BatchAnswer = v.looseObject({
	previous: v.array(RoleChangeSchema),
	permissions: Strings
});

/**
 * Change several permissions of a role at once. The server checks every
 * change before it writes one and writes one audit entry. previous holds
 * what was stored before each change: sending it back undoes the batch.
 * permissions are the role's permissions after. Needs a super admin
 */
export async function saveRoleChanges(
	role: string,
	changes: RoleChange[]
): Promise<{ previous: RoleChange[]; permissions: string[] }> {
	const out = await api(wrapped(BatchAnswer), '/api/panel/role-permissions/batch', {
		method: 'POST',
		body: { role, changes: changes.map((c) => ({ permission: c.permission, granted: c.granted })) }
	});
	return {
		previous: out.previous.map((c) => ({ permission: c.permission, granted: c.granted })),
		permissions: out.permissions
	};
}
