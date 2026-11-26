/**
 * The values stored with an organization, sorted for the Settings tab of
 * its page (PATTERN.md rule 2): each policy group the Policies page saves
 * (policy_<group>), whether it is set here and the values it holds, in
 * the Policies page's order (fields the console does not know after them,
 * by their key); the shared address book's switch; and everything else as
 * stored.
 */

import { FIELDS, SECTIONS, type Section } from '#lib/rules/policies.ts';

export interface StoredSetting {
	key: string;
	value: string;
}

export interface PolicyGroup {
	section: Section;
	key: string;
	/** Saved for this organization; otherwise no rule applies and each device keeps its own setting */
	set: boolean;
	/** The saved values: field id ("connection.mode") for known ones, else the stored key */
	values: { field: string; known: boolean; value: unknown }[];
	raw: string;
}

export interface OrgSettingsView<R extends StoredSetting = StoredSetting> {
	policies: PolicyGroup[];
	/** The shared address book's switch: null while nothing is stored (the server's default, on) */
	addressBook: { on: boolean; raw: string } | null;
	other: R[];
}

export const ADDRESS_BOOK_KEY = 'shared_address_book_enabled';

function storedValues(section: Section, raw: string): PolicyGroup['values'] {
	let obj: Record<string, unknown> = {};
	try {
		const v: unknown = JSON.parse(raw);
		if (v && typeof v === 'object' && !Array.isArray(v)) obj = v as Record<string, unknown>;
	} catch {
		return [];
	}
	const names = Object.keys(FIELDS[section]);
	const known = names
		.filter((n) => n in obj)
		.map((n) => ({ field: `${section}.${n}`, known: true, value: obj[n] }));
	const unknown = Object.keys(obj)
		.filter((n) => !names.includes(n))
		.sort()
		.map((n) => ({ field: n, known: false, value: obj[n] }));
	return [...known, ...unknown];
}

export function orgSettingsView<R extends StoredSetting>(rows: readonly R[]): OrgSettingsView<R> {
	const byKey = new Map(rows.map((r) => [r.key, r.value]));
	const policies = SECTIONS.map((section) => {
		const key = `policy_${section}`;
		const raw = byKey.get(key);
		return {
			section,
			key,
			set: raw !== undefined,
			values: raw === undefined ? [] : storedValues(section, raw),
			raw: raw ?? ''
		};
	});
	const book = byKey.get(ADDRESS_BOOK_KEY);
	const known = new Set([...policies.map((p) => p.key), ADDRESS_BOOK_KEY]);
	return {
		policies,
		addressBook: book === undefined ? null : { on: book.trim().toLowerCase() !== 'false', raw: book },
		other: rows.filter((r) => !known.has(r.key)).sort((a, b) => a.key.localeCompare(b.key))
	};
}
