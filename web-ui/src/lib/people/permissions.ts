/**
 * The logic of Roles and permissions without a DOM: the permissions
 * grouped by what a person does, their names and sentences, what a role
 * has with the unsaved changes applied, the sentence that sums a role up,
 * the notes for permissions that need others, the views (what differs from
 * the default, a second role to compare with, who has a permission) and
 * the bodies of the batch save and its undo.
 *
 * A permission's state in the draft is its override: true (allowed),
 * false (not allowed) or null (the role's default).
 */

import type { Change } from '#lib/ui/settings/draft.svelte.ts';
import type { PermissionNeed, RoleChange, RoleInfo, RoleOverride } from '#lib/api/roles.ts';

type T = (key: string, params?: Record<string, string | number>) => string;

/** A permission's override in the draft: allowed, not allowed or the role's default */
export type Ovr = boolean | null;

export interface PermissionGroup {
	id: string;
	permissions: readonly string[];
	/** Permissions that only let a person see things; the others change something */
	see?: readonly string[];
	/** Permissions the summary names on their own (permissions.sum_<key>), apart from the group */
	apart?: readonly string[];
}

/** The groups in the order the page shows them; a permission the server lists that none names goes to Other */
export const PERMISSION_GROUPS: readonly PermissionGroup[] = [
	{
		id: 'connect',
		permissions: [
			'device.connect',
			'guest.create',
			'device.signin_use',
			'remote_target.connect',
			'agent.terminal',
			'agent.files',
			'mesh.terminal',
			'mesh.files',
			'mesh.power',
			'cdap.command',
			'cdap.terminal',
			'cdap.files'
		],
		// Making a guest link connects nobody: the summary says "make guest links"
		apart: ['guest.create']
	},
	{
		id: 'manage',
		permissions: [
			'device.view',
			'device.edit',
			'device.delete',
			'device.ban',
			'device.change_id',
			'device.connection_mode',
			'device.signin_manage',
			'remote_target.view',
			'remote_target.edit',
			'remote_target.delete',
			'remote_target.test',
			'enrollment.manage',
			'enrollment.approve',
			'cdap.view'
		],
		see: ['device.view', 'remote_target.view', 'cdap.view'],
		// Letting devices join changes no device: the summary says "manage joining"
		apart: ['enrollment.manage', 'enrollment.approve']
	},
	{ id: 'support', permissions: ['chat.support', 'chat.team', 'chat.assign', 'chat.canned_manage'] },
	{
		id: 'people',
		permissions: [
			'user.view',
			'user.create',
			'user.edit',
			'user.delete',
			'org.create',
			'org.edit',
			'org.delete',
			'org.manage_users',
			'org.manage_devices'
		],
		see: ['user.view']
	},
	{
		id: 'server',
		permissions: [
			'server.config',
			'server.keys',
			'server.attestation',
			'blocklist.edit',
			'audit.view',
			'metrics.view',
			'branding.edit'
		],
		see: ['audit.view', 'metrics.view']
	},
	{
		id: 'billing',
		permissions: ['billing.view', 'billing.manage', 'billing.reports', 'billing.export'],
		see: ['billing.view']
	}
];

/** The groups to show for the permissions the server knows (all of them when it lists none) */
export function permissionGroups(all: readonly string[]): { id: string; permissions: string[] }[] {
	const known = new Set(all);
	const listed = new Set<string>();
	const groups = PERMISSION_GROUPS.map((g) => {
		const permissions = g.permissions.filter((p) => !known.size || known.has(p));
		permissions.forEach((p) => listed.add(p));
		return { id: g.id, permissions };
	}).filter((g) => g.permissions.length);
	const rest = all.filter((p) => !listed.has(p));
	if (rest.length) groups.push({ id: 'other', permissions: rest });
	return groups;
}

/** The element id of a permission's row: the settings search and #links reach it */
export function permRowId(perm: string): string {
	return `perm-${perm.replace(/\./g, '-')}`;
}

const keyPart = (perm: string) => perm.replace(/\./g, '_');

function translated(t: T, key: string, fallback: string): string {
	const v = t(key);
	return v === key ? fallback : v;
}

export const permLabelKey = (perm: string) => `permissions.perm_${keyPart(perm)}`;
export const permHelpKey = (perm: string) => `permissions.help_${keyPart(perm)}`;
export const groupTitleKey = (id: string) => `permissions.group_${id}`;
export const groupDescKey = (id: string) => `permissions.group_${id}_desc`;

export function permLabel(perm: string, t: T): string {
	return translated(t, permLabelKey(perm), perm);
}

export function permHelp(perm: string, t: T): string {
	return translated(t, permHelpKey(perm), '');
}

export function groupTitle(id: string, t: T): string {
	return translated(t, groupTitleKey(id), id.charAt(0).toUpperCase() + id.slice(1));
}

export function groupDesc(id: string, t: T): string {
	return translated(t, groupDescKey(id), '');
}

/** The role's overrides as permission → granted */
export function overrideMap(overrides: readonly RoleOverride[], role: string): Map<string, boolean> {
	return new Map(overrides.filter((o) => o.role === role).map((o) => [o.permission, o.granted]));
}

/** The draft's values for a role: every permission with its override or null */
export function draftValues(
	all: readonly string[],
	overrides: ReadonlyMap<string, boolean>
): Record<string, Ovr> {
	return Object.fromEntries(all.map((p) => [p, overrides.has(p) ? (overrides.get(p) as boolean) : null]));
}

/** ?role= when it names a role, else the first role that can be changed, else the first */
export function pickRole<R extends { name: string; is_super_admin: boolean }>(
	roles: readonly R[],
	wanted: string | null
): string {
	return (
		(roles.find((r) => r.name === wanted) ?? roles.find((r) => !r.is_super_admin) ?? roles[0])?.name ?? ''
	);
}

/** The role's default for a permission: off when the code fixes it */
export function defaultOn(perm: string, role: Pick<RoleInfo, 'permissions' | 'fixed'>): boolean {
	return !role.fixed[perm] && role.permissions.includes(perm);
}

/**
 * What the role has with the unsaved changes: a permission the draft did
 * not change is as the server says, a changed one is its new override or
 * the role's default, a fixed one is off. Super admin roles have everything.
 */
export function liveEffective(
	role: Pick<RoleInfo, 'permissions' | 'fixed' | 'is_super_admin'>,
	server: ReadonlySet<string>,
	loaded: Readonly<Record<string, Ovr>>,
	values: Readonly<Record<string, Ovr>>,
	all: readonly string[]
): Set<string> {
	if (role.is_super_admin) return new Set(all);
	const out = new Set<string>();
	for (const p of all) {
		if (role.fixed[p]) continue;
		const v = values[p] ?? null;
		const on = v !== (loaded[p] ?? null) ? (v ?? role.permissions.includes(p)) : server.has(p);
		if (on) out.add(p);
	}
	return out;
}

/**
 * The override a switch sets: back to what was saved is no change, the
 * role's default clears the override, anything else is an override.
 */
export function toggleValue(on: boolean, saved: Ovr, byDefault: boolean): Ovr {
	if (saved !== null && on === saved) return saved;
	if (on === byDefault) return null;
	return on;
}

/** The permissions a permission needs that are off now; one the code fixes for the role can't be turned on */
export function missingNeeds(
	perm: string,
	live: ReadonlySet<string>,
	requires: Readonly<Record<string, readonly PermissionNeed[]>>,
	fixed: Readonly<Record<string, string>>
): { permission: string; reason: string; fixable: boolean }[] {
	if (!live.has(perm)) return [];
	return (requires[perm] ?? [])
		.filter((n) => !live.has(n.permission))
		.map((n) => ({ permission: n.permission, reason: n.reason, fixable: !fixed[n.permission] }));
}

/** The permissions that are on and stop working while this one is off, in the order of the page */
export function brokenBy(
	perm: string,
	live: ReadonlySet<string>,
	requires: Readonly<Record<string, readonly PermissionNeed[]>>,
	all: readonly string[]
): string[] {
	if (live.has(perm)) return [];
	const order = permissionGroups(all).flatMap((g) => g.permissions);
	return order.filter((p) => live.has(p) && (requires[p] ?? []).some((n) => n.permission === perm));
}

/** Whether a row shows under "Only show what differs from the default": by value, or an override is set */
export function differsFromDefault(
	perm: string,
	role: Pick<RoleInfo, 'permissions' | 'fixed'>,
	live: ReadonlySet<string>,
	values: Readonly<Record<string, Ovr>>
): boolean {
	return live.has(perm) !== defaultOn(perm, role) || (values[perm] ?? null) !== null;
}

/** A row's value in the second role and whether it differs from this role's */
export function compareWith(
	perm: string,
	live: ReadonlySet<string>,
	other: ReadonlySet<string>
): { other: boolean; differs: boolean } {
	return { other: other.has(perm), differs: live.has(perm) !== other.has(perm) };
}

/** The roles that have a permission: super admin roles always, the shown role with its unsaved changes */
export function rolesWith(
	perm: string,
	roles: readonly Pick<RoleInfo, 'name' | 'is_super_admin'>[],
	effective: ReadonlyMap<string, ReadonlySet<string>>,
	shown: string,
	live: ReadonlySet<string>
): string[] {
	return roles
		.filter((r) => r.is_super_admin || (r.name === shown ? live : effective.get(r.name))?.has(perm))
		.map((r) => r.name);
}

/** The batch for the server: one change per changed permission, null for back to the default */
export function batchBody(changes: readonly Pick<Change, 'field' | 'toValue'>[]): RoleChange[] {
	return changes.map((c) => ({ permission: c.field, granted: (c.toValue ?? null) as Ovr }));
}

/** The batch that undoes a save: what the server says was stored before each change */
export function undoBody(previous: readonly RoleChange[]): RoleChange[] {
	return previous.map((c) => ({ permission: c.permission, granted: c.granted }));
}

/**
 * The sentence that sums the role up, by group: what it can do and what it
 * can't. A group with permissions that only show things says "see" for
 * them and "change" for the rest. `and` and `or` join the parts in the
 * console's language.
 */
export function roleSummary(
	live: ReadonlySet<string>,
	all: readonly string[],
	t: T,
	and: (items: string[]) => string,
	or: (items: string[]) => string
): string {
	const known = new Set(all);
	const can: string[] = [];
	const cant: string[] = [];
	for (const g of PERMISSION_GROUPS) {
		const perms = g.permissions.filter((p) => known.has(p) && !g.apart?.includes(p));
		if (perms.length) groupSummary(g, perms, live, t, can, cant);
		// After the group's own phrase: "see devices and manage joining"
		for (const p of g.apart ?? [])
			if (known.has(p) && live.has(p)) can.push(t(`permissions.sum_${keyPart(p)}`));
	}
	if (!can.length) return t('permissions.sum_nothing');
	const first = t('permissions.sum_can', { list: and(can) });
	return cant.length ? `${first} ${t('permissions.sum_cant', { list: or(cant) })}` : first;
}

/** One group's phrases for roleSummary: what the role can and can't do there */
function groupSummary(
	g: PermissionGroup,
	perms: readonly string[],
	live: ReadonlySet<string>,
	t: T,
	can: string[],
	cant: string[]
): void {
	const key = (part: string) => t(`permissions.sum_${g.id}_${part}`);
	if (!g.see) {
		if (perms.some((p) => live.has(p))) can.push(key('can'));
		else cant.push(key('cant'));
		return;
	}
	const see = perms.filter((p) => g.see?.includes(p));
	const change = perms.filter((p) => !g.see?.includes(p));
	const sees = see.some((p) => live.has(p));
	const changes = change.some((p) => live.has(p));
	if (sees && changes) can.push(key('both'));
	else if (sees) {
		can.push(key('see'));
		if (change.length) cant.push(key('cant_change'));
	} else if (changes) {
		can.push(key('change'));
		if (see.length) cant.push(key('cant_see'));
	} else cant.push(key('cant_any'));
}
