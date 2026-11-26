/**
 * The settings search: finds a setting on any tab or page of settings the
 * person may open (tabs.ts places), by
 * its label, its section and its hints, in the console language. Same rules
 * as the old settings.js: at least two characters, every word must match,
 * label matches and the current tab first, eight results at most.
 */

import { SEARCH_INDEX, type IndexRow } from './search-index.ts';
import { TAB_TERMS, TERMS } from './search-terms.ts';
import {
	PAGES,
	isPage,
	isPlace,
	placeAllowed,
	placeHref,
	placeTitle,
	type PlaceId,
	type TabId
} from './tabs.ts';

export interface SearchHit {
	place: PlaceId;
	/** Element id the result jumps to */
	id: string;
	label: string;
	/** "Tab › Section" */
	where: string;
	/** /settings?tab=ldap#ldap-host or /server/backups#backup-create (without the base) */
	href: string;
}

export interface SearchContext {
	/** Translate a key */
	t: (key: string) => string;
	can: (permission: string) => boolean;
	/** Super admins also find the permissions on Roles and permissions */
	superAdmin?: boolean;
	current: TabId;
	/** The language the console shows, for lower-casing */
	locale?: string;
}

export const MIN_QUERY = 2;
export const MAX_HITS = 8;

const clean = (s: string) => s.replace(/\s+/g, ' ').replace(/:$/, '').trim();

/** Rows a person with these permissions can reach */
export function visibleRows(
	can: (p: string) => boolean,
	rows: readonly IndexRow[] = SEARCH_INDEX,
	superAdmin = false
): IndexRow[] {
	return rows.filter(
		(r) => isPlace(r[0]) && (r[5] === '' || (r[5] === 'super_admin' ? superAdmin : can(r[5])))
	);
}

/** One setting for the console-wide search (Ctrl K) */
export interface SettingEntry {
	place: PlaceId;
	id: string;
	label: string;
	/** "Settings, Sign-in", "Settings, Sign-in, Directory (LDAP)" or "Server, Backups" */
	where: string;
	/** /settings?tab=ldap#ldap-host or /server/backups#backup-create (without the base) */
	href: string;
	/** Everything the search looks at: label, section, hints, tab, other words */
	hay: string;
}

/**
 * Every setting this account may see, in the console language, for the
 * search in the top bar. A setting on a tab the account can't open, or
 * one that needs a permission it lacks, is not in the list.
 */
export function settingEntries(
	ctx: Pick<SearchContext, 't' | 'can' | 'superAdmin'>,
	rows: readonly IndexRow[] = SEARCH_INDEX
): SettingEntry[] {
	const canConfig = ctx.can('server.config');
	const superAdmin = !!ctx.superAdmin;
	const settings = clean(ctx.t('nav.settings'));
	const seen = new Set<string>();
	const out: SettingEntry[] = [];
	for (const [tabName, id, labelKey, sectionKey, hintKeys] of visibleRows(ctx.can, rows, superAdmin)) {
		if (!isPlace(tabName) || !placeAllowed(tabName, canConfig, superAdmin)) continue;
		const label = clean(ctx.t(labelKey));
		if (!label) continue;
		const once = `${tabName}#${id}#${label}`;
		if (seen.has(once)) continue;
		seen.add(once);
		const section = sectionKey ? clean(ctx.t(sectionKey)) : '';
		const tabTitle = clean(ctx.t(placeTitle(tabName)));
		// A section named like its tab (Directory (LDAP) on its own tab) is said once
		const own = section && section !== label && section !== tabTitle ? section : '';
		// Settings › tab, the page's section › page (Server, Backups), or the page alone (Your account)
		const area = isPage(tabName) ? PAGES[tabName].section && clean(ctx.t(PAGES[tabName].section)) : settings;
		const where = [area, tabTitle, own].filter(Boolean).join(', ');
		out.push({
			place: tabName,
			id,
			label,
			where,
			href: placeHref(tabName, id),
			hay: [label, section, ...hintKeys.map((k) => ctx.t(k)), tabTitle, TAB_TERMS[tabName], TERMS[id]]
				.filter(Boolean)
				.join(' ')
		});
	}
	return out;
}

export function searchSettings(
	query: string,
	ctx: SearchContext,
	rows: readonly IndexRow[] = SEARCH_INDEX
): SearchHit[] {
	const lower = (s: string) => s.toLocaleLowerCase(ctx.locale);
	const q = lower(query.trim());
	if (q.length < MIN_QUERY) return [];
	const words = q.split(/\s+/).filter(Boolean);
	const seen = new Set<string>();
	const hits: { hit: SearchHit; score: number }[] = [];
	for (const row of visibleRows(ctx.can, rows, !!ctx.superAdmin)) {
		const [tabName, id, labelKey, sectionKey, hintKeys] = row;
		if (!isPlace(tabName) || !placeAllowed(tabName, ctx.can('server.config'), !!ctx.superAdmin)) continue;
		const label = clean(ctx.t(labelKey));
		if (!label) continue;
		const once = `${tabName}#${id}#${label}`;
		if (seen.has(once)) continue;
		seen.add(once);
		const section = sectionKey ? clean(ctx.t(sectionKey)) : '';
		const hints = hintKeys.map((k) => ctx.t(k)).join(' ');
		const text = lower(`${label} ${section} ${hints}`);
		if (!words.every((w) => text.includes(w))) continue;
		const tabTitle = ctx.t(placeTitle(tabName));
		hits.push({
			hit: {
				place: tabName,
				id,
				label,
				where:
					section && section !== label && section !== clean(tabTitle) ? `${tabTitle} › ${section}` : tabTitle,
				href: placeHref(tabName, id)
			},
			score: (lower(label).includes(q) ? 0 : 1) + (tabName === ctx.current ? 0 : 0.5)
		});
	}
	// Stable: equal scores keep the order of the page
	return hits
		.map((h, i) => ({ ...h, i }))
		.sort((a, b) => a.score - b.score || a.i - b.i)
		.slice(0, MAX_HITS)
		.map((h) => h.hit);
}
