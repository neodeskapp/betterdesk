/**
 * The filter of a long settings page (PATTERN.md rule 9). SettingsPage
 * provides one; each SettingRow asks it whether its name, help, key or
 * other words match, and each SettingsGroup hides itself when none of its
 * rows does. Every word typed must appear; case and accents don't matter.
 */

import { getContext, setContext } from 'svelte';
import { SvelteMap } from 'svelte/reactivity';

const FILTER = Symbol('settings-filter');
const GROUP = Symbol('settings-group');

/** Lower case without accents, so "Gerät" finds "gerat" and the other way round */
export function fold(text: string): string {
	return text.normalize('NFD').replace(/\p{M}/gu, '').toLowerCase();
}

/** Whether every word of the query appears in one of the texts */
export function matchesQuery(query: string, texts: readonly (string | undefined | null)[]): boolean {
	const words = fold(query).split(/\s+/).filter(Boolean);
	if (!words.length) return true;
	const hay = fold(texts.filter(Boolean).join(' '));
	return words.every((w) => hay.includes(w));
}

export class SettingsFilter {
	query = $state('');

	get active(): boolean {
		return this.query.trim().length > 0;
	}

	matches(texts: readonly (string | undefined | null)[]): boolean {
		return matchesQuery(this.query, texts);
	}

	/** Rows shown under the current filter, by row id; the page says "Nothing matches" when it is 0 */
	readonly shown = new SvelteMap<string, boolean>();

	get shownCount(): number {
		let n = 0;
		for (const v of this.shown.values()) if (v) n++;
		return n;
	}
}

/**
 * The page's filter for its rows and groups. A getter, since a page may
 * hand its filter over only once its data has loaded (Policies, Permissions)
 */
export function provideFilter(filter: () => SettingsFilter | undefined): void {
	setContext(FILTER, filter);
}

/** The page's filter, read inside a $derived or $effect; null without one */
export function useFilter(): () => SettingsFilter | null {
	const get = getContext<(() => SettingsFilter | undefined) | undefined>(FILTER);
	return () => get?.() ?? null;
}

/** The rows of one group and whether each matches the filter */
export class GroupRows {
	readonly rows = new SvelteMap<string, boolean>();

	get anyShown(): boolean {
		if (!this.rows.size) return true;
		for (const v of this.rows.values()) if (v) return true;
		return false;
	}
}

export function provideGroup(rows: GroupRows): GroupRows {
	return setContext(GROUP, rows);
}

export function useGroup(): GroupRows | null {
	return getContext<GroupRows | undefined>(GROUP) ?? null;
}
