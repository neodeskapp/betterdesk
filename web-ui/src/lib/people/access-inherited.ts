/**
 * What a person's "Role or group default" gives right now, so the access
 * sheet can say it (PATTERN.md rule 2). The same rules as the server's
 * mergeUserPolicy (console/access/policy_resolve.go) without the person's
 * own rows: for a section the highest row among their user groups, for the
 * section or its catalog group ("group:<key>"), else the role's limit; the
 * result is capped at the role's limit. A client feature is on when any of
 * their groups turns it on, off when their groups only turn it off, else
 * on; a hard-locked feature is always off.
 */

import type { AccessLevel } from '#lib/api/access.ts';

const RANK: Record<AccessLevel, number> = { none: 0, read: 1, write: 2 };

function level(v: string): AccessLevel {
	const s = v.trim().toLowerCase();
	return s === 'read' || s === 'write' ? s : 'none';
}

export interface InheritedLevel {
	level: AccessLevel;
	source: 'group' | 'role';
}

export function inheritedSection(
	key: string,
	catalogGroup: string,
	groupRows: readonly { resource_key: string; access_level: string }[],
	roleCeiling: AccessLevel | undefined
): InheritedLevel {
	const ceiling = roleCeiling ?? 'none';
	let found: AccessLevel | null = null;
	for (const row of groupRows) {
		if (row.resource_key !== key && row.resource_key !== `group:${catalogGroup}`) continue;
		const l = level(row.access_level);
		if (found === null || RANK[l] > RANK[found]) found = l;
	}
	if (found === null) return { level: ceiling, source: 'role' };
	return { level: RANK[found] > RANK[ceiling] ? ceiling : found, source: 'group' };
}

export interface InheritedFeature {
	enabled: boolean;
	source: 'group' | 'default';
}

export function inheritedFeature(
	key: string,
	groupRows: readonly { feature_key: string; enabled: boolean }[],
	hardLock: boolean
): InheritedFeature {
	const rows = groupRows.filter((r) => r.feature_key === key);
	const out: InheritedFeature = rows.length
		? { enabled: rows.some((r) => r.enabled), source: 'group' }
		: { enabled: true, source: 'default' };
	if (hardLock) out.enabled = false;
	return out;
}
