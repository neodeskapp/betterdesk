/**
 * The guided first join (Add a computer): once a computer joins with the
 * invite, the invite sheet walks the admin through four steps. The sheet
 * follows the join by asking the server every few seconds (there is no
 * push of a join to the console).
 */

import type { InviteDetail, InviteDevice } from '#lib/api/onboard.ts';

export const GUIDE_STEPS = ['name', 'folder', 'reach', 'test'] as const;
export type GuideStep = (typeof GUIDE_STEPS)[number];

/** How often the sheet asks whether the computer joined, and whether it is online */
export const FOLLOW_MS = 3000;

/**
 * The ways to reach a joined computer that exist today, as the reach step
 * explains them (language keys below onboard.guide). Connecting without a
 * device password and a password the console sets come with later server
 * and client versions; they add their keys here, and with them their
 * choices.
 */
export const REACH_OPTIONS = ['onboard.guide.reach_accept', 'onboard.guide.reach_password'] as const;

/** The next computer of the invite to walk through: the first not set up yet in this sheet */
export function nextDevice(detail: InviteDetail | null, done: ReadonlySet<string>): InviteDevice | null {
	return detail?.devices.find((d) => !done.has(d.id)) ?? null;
}

/** The name a joined computer shows: its name, else its host name, else its ID */
export function deviceLabel(d: Pick<InviteDevice, 'id' | 'name' | 'hostname'>): string {
	return d.name || d.hostname || d.id;
}
