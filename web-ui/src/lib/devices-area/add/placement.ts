/**
 * Where a computer that joins with a device token goes: a folder and an
 * organization whose policies it gets. The token dialog and the Receiver
 * app tab offer both as optional pickers; a list the account can't read
 * is empty, and its picker stays away.
 */

import { listFolders } from '#lib/api/devices.ts';
import { listOrgs } from '#lib/api/orgs.ts';
import type { SelectOption } from '#lib/ui/Select.svelte';

export interface Places {
	folders: { id: number; name: string }[];
	orgs: { id: string; name: string }[];
}

/** The folders and organizations the account may pick, each [] when it can't read them */
export async function loadPlaces(signal?: AbortSignal): Promise<Places> {
	const [folders, orgs] = await Promise.all([
		listFolders(signal).catch(() => []),
		listOrgs(signal).catch(() => [])
	]);
	return {
		folders: folders.map((f) => ({ id: Number(f.id), name: f.name })),
		orgs: orgs.map((o) => ({ id: String(o.id), name: o.name || String(o.id) }))
	};
}

/** The folder picker: none first ('0'), then the folders by name */
export function folderOptions(folders: Places['folders'], none: string): SelectOption<string>[] {
	const sorted = [...folders].sort((a, b) => a.name.localeCompare(b.name));
	return [{ value: '0', label: none }, ...sorted.map((f) => ({ value: String(f.id), label: f.name }))];
}

/** The policies picker: none first (''), then the organizations by name */
export function orgOptions(orgs: Places['orgs'], none: string): SelectOption<string>[] {
	const sorted = [...orgs].sort((a, b) => a.name.localeCompare(b.name));
	return [{ value: '', label: none }, ...sorted.map((o) => ({ value: o.id, label: o.name }))];
}

/**
 * A picked value that is no longer in the list (a folder deleted since)
 * is none, so a save does not send a folder the server refuses
 */
export function keepKnown(value: string, options: readonly SelectOption<string>[], none: string): string {
	return options.some((o) => o.value === value) ? value : none;
}
