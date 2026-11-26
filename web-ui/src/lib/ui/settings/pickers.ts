/**
 * Options for the settings pickers (PATTERN.md rule 4) built from the
 * records the server lists: roles with their names and sentences in the
 * console's language, organizations, relay servers.
 */

import type { PickOption } from './RecordPicker.svelte';

type T = (key: string, params?: Record<string, string | number>) => string;

function translated(t: T, key: string, fallback: string): string {
	const v = t(key);
	return v === key ? fallback : v;
}

/** A role's name in the console's language (users.role_<name>), the stored name when there is none */
export function roleLabel(name: string, t: T): string {
	return translated(t, `users.role_${name}`, name);
}

/** Roles as picker options, with the role's sentence as the hint where the language has one */
export function roleOptions(roles: readonly { name: string }[], t: T): PickOption[] {
	return roles.map((r) => {
		const hint = translated(t, `users.role_desc_${r.name}`, '');
		return { value: r.name, label: roleLabel(r.name, t), hint: hint || undefined };
	});
}

/** Relay servers that a list can take with one click: their address, named by name and address */
export function relayOptions(
	relays: readonly { name?: string; address?: string }[]
): { value: string; label: string }[] {
	return relays
		.filter((r): r is { name?: string; address: string } => !!r.address && !!r.address.trim())
		.map((r) => ({
			value: r.address.trim(),
			label:
				r.name && r.name.trim() && r.name.trim() !== r.address.trim()
					? `${r.name.trim()} (${r.address.trim()})`
					: r.address.trim()
		}));
}
