/**
 * Devices › Add computers (/devices/add) without a DOM: its tabs, which
 * ones an account gets, and which one ?tab= opens. Each tab asks what its
 * page asked before it moved here (/tokens, /pairing, /agents, /keys).
 * Tested in tests/unit/devices-area/devices-add.test.ts.
 */

import { isAdminRole } from '#lib/clients/agents.ts';

export const ADD_TABS = ['invites', 'tokens', 'pairing', 'agents', 'receiver', 'manual'] as const;
export type AddTab = (typeof ADD_TABS)[number];

/** The tab's name */
export const ADD_TAB_TITLE: Readonly<Record<AddTab, string>> = {
	invites: 'devices_add.tab_invites',
	tokens: 'nav.tokens',
	pairing: 'nav.pairing',
	agents: 'nav.agents',
	receiver: 'receiver_app.tab',
	manual: 'devices_add.tab_manual'
};

/** The sentence under the page title for each tab */
export const ADD_TAB_DESC: Readonly<Record<AddTab, string>> = {
	invites: 'devices_add.invites_desc',
	tokens: 'tokens.page_description',
	pairing: 'pairing.subtitle',
	agents: 'agents.page_description',
	receiver: 'receiver_app.page_description',
	manual: 'keys.page_description'
};

export interface AddTabContext {
	can: (permission: string) => boolean;
	role: string | null | undefined;
}

const NEEDS: Readonly<Record<AddTab, (c: AddTabContext) => boolean>> = {
	// Add a computer and its invites
	invites: (c) => c.can('enrollment.approve'),
	tokens: (c) => c.can('enrollment.manage'),
	pairing: (c) => c.can('enrollment.approve'),
	// requireAdmin on the server
	agents: (c) => isAdminRole(c.role),
	// The download names a device token: what Device tokens asks
	receiver: (c) => c.can('enrollment.manage'),
	// The values a client needs: a session is enough
	manual: () => true
};

/** The tabs this account gets, in order */
export function addTabs(ctx: AddTabContext): AddTab[] {
	return ADD_TABS.filter((tab) => NEEDS[tab](ctx));
}

/** ?tab= as one of the account's tabs (any letter case); anything else is the first */
export function pickAddTab(raw: string | null | undefined, tabs: readonly AddTab[]): AddTab {
	const name = String(raw ?? '').toLowerCase();
	return tabs.find((tab) => tab === name) ?? tabs[0] ?? 'manual';
}
