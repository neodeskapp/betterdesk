/**
 * The settings tabs, the pages of settings outside Settings, and the rules
 * for their URLs (no DOM, unit tested).
 *
 * Each tab is a sub-page: /settings?tab=<name> (General without a tab).
 * An unknown tab is General. Old links used #<tab> and #server; those
 * become ?tab=. #<element-id> points at one setting, also on another tab
 * or page.
 *
 * A place is where a setting of the settings list (search-index.ts) lives:
 * a tab of Settings or a page of its own, such as the Server section's
 * Backups. Both searches and the links into settings go by place.
 */

import { SEARCH_INDEX, TAB_IDS } from './search-index.ts';

export const TABS = [
	'general',
	'auth',
	'ldap',
	'sso',
	'joining',
	'branding',
	'brand',
	'email',
	'time',
	'meshcentral'
] as const;
export type TabId = (typeof TABS)[number];

/**
 * Tabs only people with server.config may open. Sign-in holds only what
 * applies to everyone since your own password and two-factor sign-in moved
 * to Your account.
 */
export const ADMIN_TABS: readonly TabId[] = [
	'auth',
	'ldap',
	'sso',
	'joining',
	'email',
	'time',
	'meshcentral'
];

/** The tab's name: page title, breadcrumb and search results */
export const TAB_TITLE: Readonly<Record<TabId, string>> = {
	general: 'settings.tab_general',
	auth: 'settings.tab_auth',
	ldap: 'settings.ldap_title',
	sso: 'settings.oidc_title',
	joining: 'settings.enrollment_title',
	branding: 'settings.tab_branding',
	brand: 'settings.tab_brand',
	email: 'settings.tab_email',
	time: 'settings.tab_time',
	meshcentral: 'nav.meshcentral'
};

/**
 * Pages of settings outside Settings: where they are, their name, the
 * section of the sidebar they are in ('' for none, as Your account, which
 * the account menu opens) and the permission they need ('' for every
 * account)
 */
export const PAGES = {
	account: { href: '/account', title: 'account.title', section: '', need: '' },
	'server-backups': {
		href: '/server/backups',
		title: 'nav.server_backups',
		section: 'nav.section_server',
		need: 'server.config'
	},
	'server-updates': {
		href: '/server/updates',
		title: 'updates.tab_title',
		section: 'nav.section_server',
		need: 'server.config'
	},
	'server-config-files': {
		href: '/server/config-files',
		title: 'nav.server_config_files',
		section: 'nav.section_server',
		need: 'server.config'
	}
} as const satisfies Record<
	string,
	{ href: string; title: string; section: string; need: '' | 'server.config' }
>;
export type PageId = keyof typeof PAGES;

export type PlaceId = TabId | PageId;

/** The sentence under the title; General and Sign-in say less to people who can't change the server */
export function tabDescKey(tab: TabId, canServerConfig: boolean): string {
	if (tab === 'general')
		return canServerConfig ? 'settings.page.general_desc' : 'settings.page.general_desc_viewer';
	if (tab === 'auth') return canServerConfig ? 'settings.page.auth_desc' : 'settings.page.auth_desc_viewer';
	return `settings.page.${tab}_desc`;
}

export function isTab(name: string | null | undefined): name is TabId {
	return !!name && (TABS as readonly string[]).includes(name);
}

export function isPage(name: string | null | undefined): name is PageId {
	return !!name && Object.hasOwn(PAGES, name);
}

export function isPlace(name: string | null | undefined): name is PlaceId {
	return isTab(name) || isPage(name);
}

/** ?tab= value to a tab: case does not matter, anything else is General */
export function parseTab(raw: string | null | undefined): TabId {
	const name = String(raw ?? '').toLowerCase();
	return isTab(name) ? name : 'general';
}

export function tabAllowed(tab: TabId, canServerConfig: boolean): boolean {
	return canServerConfig || !ADMIN_TABS.includes(tab);
}

/** Whether a person may open a place: the pages of the Server section need server.config, Your account nothing */
export function placeAllowed(place: PlaceId, canServerConfig: boolean): boolean {
	return isPage(place) ? PAGES[place].need === '' || canServerConfig : tabAllowed(place, canServerConfig);
}

/** The place's name: the tab title or the page title */
export function placeTitle(place: PlaceId): string {
	return isPage(place) ? PAGES[place].title : TAB_TITLE[place];
}

/** An old #<tab> or #server link: the tab it means, or null for anything else */
export function hashTab(hash: string): TabId | null {
	const name = decodeHash(hash);
	if (name === 'server') return 'general';
	return isTab(name) ? name : null;
}

/** "#public-endpoints" → "public-endpoints" (decoded; a broken escape stays as it is) */
export function decodeHash(hash: string): string {
	const raw = hash.replace(/^#/, '');
	try {
		return decodeURIComponent(raw);
	} catch {
		return raw;
	}
}

let idPlace: Map<string, PlaceId> | null = null;

/** The tab or page that holds an element id, or null when none has it */
export function placeOfId(id: string): PlaceId | null {
	if (!idPlace) {
		idPlace = new Map();
		for (const [place, ids] of Object.entries(TAB_IDS)) {
			if (!isPlace(place)) continue;
			for (const one of ids.split(' ')) if (one) idPlace.set(one, place);
		}
		for (const row of SEARCH_INDEX) if (isPlace(row[0]) && !idPlace.has(row[1])) idPlace.set(row[1], row[0]);
	}
	return idPlace.get(id) ?? null;
}

/** The query string for a tab: General has none, as in the old links */
export function tabSearch(tab: TabId, rest?: URLSearchParams): string {
	const q = new URLSearchParams(rest);
	if (tab === 'general') q.delete('tab');
	else q.set('tab', tab);
	const s = q.toString();
	return s ? `?${s}` : '';
}

/** The address of a place, with #<id> when given (without the base) */
export function placeHref(place: PlaceId, id?: string): string {
	const hash = id ? `#${encodeURIComponent(id)}` : '';
	return isPage(place) ? `${PAGES[place].href}${hash}` : `/settings${tabSearch(place)}${hash}`;
}
