/**
 * The roles the directory and single sign-on can give, for the Default
 * role picker and the group to role map: the server's roles with their
 * names and sentences in the console's language (pickers.ts roleOptions).
 * Listing roles needs user.view; without it (or when the list fails) the
 * three roles the old form offered stand in.
 */
import { listRoles } from '#lib/api/roles.ts';
import { t, tf } from '#lib/i18n/i18n.svelte.ts';
import type { Choice } from '#lib/ui/ChoiceList.svelte';
import { roleOptions } from '#lib/ui/settings/pickers.ts';
import type { PickOption } from '#lib/ui/settings/RecordPicker.svelte';

const FALLBACK = ['viewer', 'operator', 'admin'];

export async function signInRoles(): Promise<PickOption[]> {
	try {
		const { roles } = await listRoles();
		if (roles.length) return roleOptions(roles, t);
	} catch {
		/* the stand-ins below */
	}
	return roleOptions(
		FALLBACK.map((name) => ({ name })),
		t
	);
}

/** The roles as choices with their sentences; a stored role the server no longer has stays choosable */
export function roleChoices(id: string, roles: readonly PickOption[], current: string): Choice<string>[] {
	const out: Choice<string>[] = roles.map((r) => ({
		id: `${id}-${r.value}`,
		value: r.value,
		title: r.label,
		description: r.hint
	}));
	if (current && !roles.some((r) => r.value === current))
		out.push({
			id: `${id}-${current}`,
			value: current,
			title: tf('settings_ui.gone_record', '{name} (no longer exists)', { name: current })
		});
	return out;
}
