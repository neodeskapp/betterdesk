/**
 * The choices of the Add a computer sheet and the body it sends.
 */

import type { InviteRequest } from '#lib/api/onboard.ts';

/** How long an invite works, in hours: 1 day, 7 days (default), 30 days */
export const LIFETIMES = [24, 168, 720] as const;

/** How often an invite can be used: once (default), 5 or 25 times, or until it expires (the server's most) */
export const USES = [1, 5, 25, 1000] as const;

/** The uses that stand for "until it expires" */
export const UNLIMITED_USES = 1000;

export interface InviteForm {
	name: string;
	/** 0 for no folder */
	folder: number;
	email: string;
	hours: number;
	uses: number;
}

/** The body of POST /api/onboard/invites. A filled-in address is mailed at once */
export function inviteBody(f: InviteForm): InviteRequest {
	const email = f.email.trim();
	const body: InviteRequest = { lifetime_hours: f.hours, uses: f.uses };
	if (f.name.trim()) body.name = f.name.trim();
	if (f.folder > 0) body.folder_id = f.folder;
	if (email) {
		body.email = email;
		body.send_email = true;
	}
	return body;
}
