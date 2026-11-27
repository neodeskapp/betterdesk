/**
 * The policy form without a DOM. The Policies page edits one flat draft of
 * every setting ("connection.mode", "features.allow_audio"), each either a
 * value or null: null is "no rule here" (each device keeps its own
 * setting, or the server's value applies), which each control shows as
 * its "Not set" choice. Nothing is filled in that the organization did
 * not store.
 *
 * Saving writes only the groups that changed: PUT with the settings that
 * have a rule, DELETE when none is left. Undo writes the previous group
 * bodies back the same way.
 */

import type { AuditEntry, ResolvedPolicies } from '#lib/api/policies.ts';

export const SECTIONS = ['connection', 'features', 'security', 'network', 'update'] as const;
export type Section = (typeof SECTIONS)[number];

/** Heading key per group (policies.<key>) */
export const SECTION_TITLE: Readonly<Record<Section, string>> = {
	connection: 'policies.connection_title',
	features: 'policies.features_title',
	security: 'policies.security_title',
	network: 'policies.network_title',
	update: 'policies.update_title'
};

export type FieldKind =
	| { kind: 'choice'; options: readonly string[] }
	| { kind: 'whole'; min: number; max: number }
	| { kind: 'switch' }
	| { kind: 'list'; entry: 'name' | 'relay' | 'range' };

/**
 * Every setting of every group, in page order, with what it takes (the
 * same checks as neodesk-server/api/org_policy_check.go)
 */
export const FIELDS: Readonly<Record<Section, Readonly<Record<string, FieldKind>>>> = {
	connection: {
		mode: { kind: 'choice', options: ['attended', 'unattended', 'ask_always'] },
		max_session_duration: { kind: 'whole', min: 0, max: 1440 },
		idle_lock_timeout: { kind: 'whole', min: 0, max: 120 },
		wallpaper: { kind: 'choice', options: ['show', 'hide_during_session', 'hide_always'] }
	},
	features: {
		allow_file_transfer: { kind: 'switch' },
		allow_clipboard: { kind: 'switch' },
		allow_audio: { kind: 'switch' },
		allow_recording: { kind: 'switch' }
	},
	security: {
		require_2fa: { kind: 'switch' },
		password_min_length: { kind: 'whole', min: 6, max: 128 },
		password_require_special: { kind: 'switch' },
		password_max_age_days: { kind: 'whole', min: 0, max: 365 },
		allowed_operators: { kind: 'list', entry: 'name' },
		allow_uninstall: { kind: 'switch' }
	},
	network: {
		block_direct_p2p: { kind: 'switch' },
		allowed_relay_servers: { kind: 'list', entry: 'relay' },
		ip_allowlist: { kind: 'list', entry: 'range' }
	},
	update: {
		auto_update: { kind: 'switch' },
		channel: { kind: 'choice', options: ['stable', 'beta', 'dev'] }
	}
};

/** "connection.mode" and the rest, in page order */
export const FIELD_IDS: readonly string[] = SECTIONS.flatMap((s) =>
	Object.keys(FIELDS[s]).map((name) => `${s}.${name}`)
);

/** The draft: every field id, a value or null for no rule */
export type PolicyDraft = Record<string, unknown>;

export function groupFields(group: Section): string[] {
	return Object.keys(FIELDS[group]).map((name) => `${group}.${name}`);
}

function split(field: string): [Section, string] {
	const at = field.indexOf('.');
	return [field.slice(0, at) as Section, field.slice(at + 1)];
}

export function kindOf(field: string): FieldKind | undefined {
	const [group, name] = split(field);
	return FIELDS[group]?.[name];
}

/** The element id of a setting's row: "pol-connection-max-session-duration" */
export function rowId(field: string): string {
	return 'pol-' + field.replace(/[._]/g, '-');
}

function normalize(kind: FieldKind | undefined, value: unknown): unknown {
	if (kind?.kind === 'list') return Array.isArray(value) ? value.map((x) => String(x)) : [];
	return value;
}

/** The draft as loaded: what the organization stored, null everywhere else */
export function valuesFromResolved(r: ResolvedPolicies | null): PolicyDraft {
	const out: PolicyDraft = {};
	for (const id of FIELD_IDS) {
		const [group, name] = split(id);
		const f = r?.[group]?.fields[name];
		out[id] =
			f && f.source === 'organization' && f.value !== null && f.value !== undefined
				? normalize(kindOf(id), f.value)
				: null;
	}
	return out;
}

const same = (a: unknown, b: unknown) => a === b || JSON.stringify(a) === JSON.stringify(b);

/** Groups with a field that differs from what was loaded */
export function changedGroups(v: PolicyDraft, loaded: PolicyDraft): Section[] {
	return SECTIONS.filter((s) => groupFields(s).some((f) => !same(v[f] ?? null, loaded[f] ?? null)));
}

/** What PUT /api/panel/policies/:org/<group> gets: the settings with a rule; null when none has one */
export function groupBody(group: Section, v: PolicyDraft): Record<string, unknown> | null {
	const body: Record<string, unknown> = {};
	for (const f of groupFields(group)) {
		const value = v[f];
		if (value !== null && value !== undefined) body[split(f)[1]] = value;
	}
	return Object.keys(body).length ? body : null;
}

export interface GroupWrite {
	group: Section;
	/** null: DELETE the group */
	body: Record<string, unknown> | null;
}

/** The writes a save makes, one per changed group */
export function writesFor(v: PolicyDraft, loaded: PolicyDraft): GroupWrite[] {
	return changedGroups(v, loaded).map((group) => ({ group, body: groupBody(group, v) }));
}

/** The writes that put the groups back as they were before a save */
export function undoWrites(before: PolicyDraft, groups: readonly Section[]): GroupWrite[] {
	return groups.map((group) => ({ group, body: groupBody(group, before) }));
}

function ipv4(text: string): boolean {
	const parts = text.split('.');
	return parts.length === 4 && parts.every((p) => /^\d{1,3}$/.test(p) && Number(p) <= 255);
}

function ipv6(text: string): boolean {
	if (!/^[0-9a-fA-F:.]+$/.test(text) || !text.includes(':')) return false;
	try {
		return new URL(`http://[${text}]/`).hostname.length > 2;
	} catch {
		return false;
	}
}

const HOST_NAME =
	/^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$/;

/** An IP address, or a range in CIDR notation (192.168.1.0/24, 2001:db8::/32) */
export function isAddressRange(text: string): boolean {
	const [address = '', bits, ...rest] = text.split('/');
	if (rest.length) return false;
	const v4 = ipv4(address);
	if (!v4 && !ipv6(address)) return false;
	if (bits === undefined) return true;
	return /^\d{1,3}$/.test(bits) && Number(bits) <= (v4 ? 32 : 128);
}

/** A relay server: a host name or an address, with a port of 1 to 65535 or none */
export function isRelayAddress(text: string): boolean {
	let host = text;
	let port: string | undefined;
	const bracketed = /^\[([^\]]+)\](?::(.*))?$/.exec(text);
	if (bracketed) {
		host = bracketed[1] ?? '';
		port = bracketed[2];
		if (!ipv6(host)) return false;
	} else if (text.split(':').length === 2) {
		[host = '', port] = text.split(':');
	} else if (text.includes(':')) {
		return ipv6(text);
	}
	if (port !== undefined && !(/^\d{1,5}$/.test(port) && Number(port) >= 1 && Number(port) <= 65535))
		return false;
	if (bracketed) return true;
	return ipv4(host) || (host.length <= 253 && HOST_NAME.test(host));
}

const ENTRY_CHECK: Readonly<Record<'name' | 'relay' | 'range', (entry: string) => boolean>> = {
	name: (s) => s.trim() !== '',
	relay: isRelayAddress,
	range: isAddressRange
};

/** Whether one entry of a list setting is what it must be */
export function entryOk(field: string, entry: string): boolean {
	const kind = kindOf(field);
	return kind?.kind !== 'list' || ENTRY_CHECK[kind.entry](entry.trim());
}

/** Whether a whole-number setting's value is in its range */
export function wholeOk(field: string, value: unknown): boolean {
	const kind = kindOf(field);
	if (kind?.kind !== 'whole') return true;
	return typeof value === 'number' && Number.isInteger(value) && value >= kind.min && value <= kind.max;
}

/**
 * Settings with a rule whose value the server would refuse, in page order:
 * a number out of range (entry ''), or the first wrong entry of a list
 */
export function fieldProblems(v: PolicyDraft): { field: string; entry: string }[] {
	const out: { field: string; entry: string }[] = [];
	for (const field of FIELD_IDS) {
		const value = v[field];
		if (value === null || value === undefined) continue;
		const kind = kindOf(field);
		if (kind?.kind === 'whole' && !wholeOk(field, value)) out.push({ field, entry: '' });
		if (kind?.kind === 'list' && Array.isArray(value)) {
			const bad = value.map(String).find((e) => !entryOk(field, e));
			if (bad !== undefined) out.push({ field, entry: bad });
		}
	}
	return out;
}

/** Groups the organization stored anything for */
export function setCount(r: ResolvedPolicies | null): number {
	return r ? SECTIONS.filter((s) => r[s]?.set).length : 0;
}

/** The group a change log line is about */
export function auditSection(e: AuditEntry): string {
	const fromDetails = e.details && typeof e.details.category === 'string' ? e.details.category : '';
	return fromDetails || e.section || e.category || '';
}

export function auditTime(e: AuditEntry): string {
	return e.timestamp || e.created_at || '';
}
