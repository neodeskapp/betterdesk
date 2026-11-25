/**
 * Global search (public/js/ui-search.js): pages from the navigation the
 * server sent (with their keywords and earlier names), things to do,
 * settings (lib/settings/search.ts, the same list the Settings page
 * searches), devices and people. Every word typed must match somewhere;
 * case and accents don't matter. Pure functions; SearchDialog.svelte does
 * the loading and the keyboard.
 */

import type { SearchPage } from './nav-model.ts';

export const MAX_PAGES = 6;
export const MAX_ACTIONS = 4;
export const MAX_SETTINGS = 8;
export const MAX_DEVICES = 8;
export const MAX_USERS = 5;

export interface SearchDevice {
	id: string;
	hostname?: string;
	display_name?: string;
	alias?: string;
	username?: string;
	user?: string;
	platform?: string;
	os?: string;
	tags?: string[] | string;
	note?: string;
	online?: boolean;
	banned?: boolean;
}

export interface SearchUser {
	username: string;
	email?: string;
	role?: string;
}

/** A setting as lib/settings/search.ts settingEntries() makes it */
export interface SearchSetting {
	id: string;
	label: string;
	where: string;
	href: string;
	hay: string;
}

/** Something to do, as lib/shell/actions.ts lists it for this account */
export interface SearchAction {
	id: string;
	title: string;
	/** Other words, English and German */
	keywords: string;
	icon: string;
}

/** 'recent' is a search typed before, shown while the field is empty */
export type ResultKind = 'recent' | 'page' | 'action' | 'setting' | 'device' | 'user';

export interface SearchResult {
	kind: ResultKind;
	title: string;
	/** Second line: section › group, where a setting lives, or ID and platform */
	sub: string;
	/** Old URL; the dialog passes it through link(); empty for actions */
	href: string;
	external: boolean;
	/** For devices: online, offline or banned (the dialog translates it) */
	state?: 'online' | 'offline' | 'banned';
	/** A device's ID (shown in monospace), a setting's element id, or an action's id */
	id?: string;
	icon?: string;
}

/** Lower case without accents, so "Geräte" matches "gerate" */
export function norm(s: unknown): string {
	return String(s ?? '')
		.toLowerCase()
		.normalize('NFD')
		.replace(/[̀-ͯ]/g, '');
}

export function words(query: string): string[] {
	return norm(query).split(/\s+/).filter(Boolean);
}

function matches(hay: unknown[], ws: string[]): boolean {
	const h = norm(hay.filter((x) => x !== undefined && x !== null).join(' '));
	return ws.every((w) => h.includes(w));
}

/** Titles that start with the first word, then titles with every word, then the rest; ties keep their order */
function ranked<T>(items: readonly T[], title: (x: T) => string, ws: string[]): T[] {
	const first = ws[0] ?? '';
	return items
		.map((x, i) => {
			const tt = norm(title(x));
			return { x, i, rank: tt.startsWith(first) ? 0 : ws.every((w) => tt.includes(w)) ? 1 : 2 };
		})
		.sort((a, b) => a.rank - b.rank || a.i - b.i)
		.map((r) => r.x);
}

export interface SearchData {
	pages: readonly SearchPage[];
	actions?: readonly SearchAction[] | null;
	settings?: readonly SearchSetting[] | null;
	devices?: readonly SearchDevice[] | null;
	users?: readonly SearchUser[] | null;
}

export interface SearchOutcome {
	results: SearchResult[];
	/** How many matched in each group, also those the list leaves out */
	totals: Record<ResultKind, number>;
}

export function searchGrouped(query: string, data: SearchData): SearchOutcome {
	const totals: Record<ResultKind, number> = {
		recent: 0,
		page: 0,
		action: 0,
		setting: 0,
		device: 0,
		user: 0
	};
	const ws = words(query);
	if (!ws.length) return { results: [], totals };
	const first = ws[0] as string;
	const out: SearchResult[] = [];

	const pages = data.pages
		.filter((p) => matches([p.text, p.section, p.group, p.keywords], ws))
		// Titles that start with the first word come first; otherwise the nav order stays
		.map((p, i) => ({ p, i, lead: norm(p.text).startsWith(first) ? 0 : 1 }))
		.sort((a, b) => a.lead - b.lead || a.i - b.i);
	totals.page = pages.length;
	for (const { p } of pages.slice(0, MAX_PAGES))
		out.push({
			kind: 'page',
			title: p.text,
			sub: [p.section, p.group].filter(Boolean).join(' › '),
			href: p.href,
			external: p.external
		});

	const actions = ranked(
		(data.actions ?? []).filter((a) => matches([a.title, a.keywords], ws)),
		(a) => a.title,
		ws
	);
	totals.action = actions.length;
	for (const a of actions.slice(0, MAX_ACTIONS))
		out.push({ kind: 'action', title: a.title, sub: '', href: '', external: false, id: a.id, icon: a.icon });

	const settings = ranked(
		(data.settings ?? []).filter((s) => matches([s.hay, s.where], ws)),
		(s) => s.label,
		ws
	);
	totals.setting = settings.length;
	for (const s of settings.slice(0, MAX_SETTINGS))
		out.push({ kind: 'setting', title: s.label, sub: s.where, href: s.href, external: false, id: s.id });

	const devices = (data.devices ?? []).filter((d) => {
		const tags = Array.isArray(d.tags) ? d.tags.join(' ') : (d.tags ?? '');
		return matches(
			[d.id, d.hostname, d.display_name, d.alias, d.username, d.user, d.platform, d.os, tags, d.note],
			ws
		);
	});
	totals.device = devices.length;
	for (const d of devices.slice(0, MAX_DEVICES))
		out.push({
			kind: 'device',
			title: d.display_name || d.hostname || d.id,
			id: d.id,
			sub: d.platform || d.os || '',
			state: d.banned ? 'banned' : d.online ? 'online' : 'offline',
			href: `/devices?search=${encodeURIComponent(d.id)}&open=${encodeURIComponent(d.id)}`,
			external: false
		});

	const users = (data.users ?? []).filter((u) => matches([u.username, u.email, u.role], ws));
	totals.user = users.length;
	for (const u of users.slice(0, MAX_USERS))
		out.push({
			kind: 'user',
			title: u.username,
			sub: [u.email, u.role].filter(Boolean).join(', '),
			href: `/users?search=${encodeURIComponent(u.username)}`,
			external: false
		});

	return { results: out, totals };
}

export function searchAll(query: string, data: SearchData): SearchResult[] {
	return searchGrouped(query, data).results;
}

/* ── Recent searches: per account, kept only in this browser ── */

export const RECENT_MAX = 6;
export const recentKey = (userId: string | number): string => `nd_search_recent:${userId}`;

export function readRecent(storage: Pick<Storage, 'getItem'> | null, key: string): string[] {
	try {
		const v: unknown = JSON.parse(storage?.getItem(key) ?? '[]');
		return Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string').slice(0, RECENT_MAX) : [];
	} catch {
		return [];
	}
}

/** The query goes first; the same words again (any case) replace the older entry */
export function addRecent(list: readonly string[], query: string): string[] {
	const q = query.trim().replace(/\s+/g, ' ');
	if (!q) return [...list];
	return [q, ...list.filter((x) => norm(x) !== norm(q))].slice(0, RECENT_MAX);
}

export function writeRecent(
	storage: Pick<Storage, 'setItem' | 'removeItem'> | null,
	key: string,
	list: readonly string[]
): void {
	try {
		if (list.length) storage?.setItem(key, JSON.stringify(list));
		else storage?.removeItem(key);
	} catch {
		/* storage blocked: the list lives for this page only */
	}
}

/** Results grouped by kind, in order, for the listbox groups */
export function groupResults(
	results: readonly SearchResult[]
): { kind: ResultKind; items: { r: SearchResult; index: number }[] }[] {
	const groups: { kind: ResultKind; items: { r: SearchResult; index: number }[] }[] = [];
	results.forEach((r, index) => {
		let g = groups.at(-1);
		if (!g || g.kind !== r.kind) {
			g = { kind: r.kind, items: [] };
			groups.push(g);
		}
		g.items.push({ r, index });
	});
	return groups;
}

/** Ctrl+K / Cmd+K anywhere; "/" when focus is not in a field and no modifier is held */
export function isSearchShortcut(
	e: Pick<KeyboardEvent, 'key' | 'ctrlKey' | 'metaKey' | 'altKey' | 'target'>
): boolean {
	if ((e.ctrlKey || e.metaKey) && (e.key === 'k' || e.key === 'K')) return true;
	if (e.key !== '/' || e.ctrlKey || e.metaKey || e.altKey) return false;
	const el = e.target instanceof Element ? e.target : null;
	return !el?.closest('input, textarea, select, [contenteditable=""], [contenteditable="true"]');
}

/**
 * The shortcut shown in the search field: "⌘ K" on a Mac, else Ctrl as the
 * keyboard of the console language names it ("Strg K" in German, as the
 * German texts of the flow builder write it)
 */
export function shortcutHint(lang: string, mac: boolean): string {
	if (mac) return '⌘ K';
	return `${lang === 'de' ? 'Strg' : 'Ctrl'} K`;
}
