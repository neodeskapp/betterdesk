/**
 * The setup guide's status for the signed-in account, shared by the
 * overview (which shows it) and the places that bring it back (Help,
 * Settings). Only accounts with server.config ask; the server refuses
 * everyone else.
 */

import { goto } from '$app/navigation';
import { getSetupGuide, setSetupGuide, type GuideStatus } from '#lib/api/setupGuide.ts';
import { link } from '#lib/nav.ts';
import { can, session } from '#lib/state/session.svelte.ts';

class GuideState {
	/** The last answer, and the account it is for */
	#status = $state.raw<GuideStatus | null>(null);
	#user = $state<string | null>(null);

	/** null until the first answer for the account signed in now */
	get status(): GuideStatus | null {
		return this.#user !== null && this.#user === signedIn() ? this.#status : null;
	}

	set status(status: GuideStatus | null) {
		this.#status = status;
		this.#user = signedIn();
	}
}

/** The account signed in now, as text: the server sends its id as a number or a string */
function signedIn(): string | null {
	const id = session.user?.id;
	return id === undefined || id === null ? null : String(id);
}

export const guide = new GuideState();

/** Whether this account may use the guide at all */
export function guideAvailable(): boolean {
	return can('server.config');
}

export async function loadGuide(signal?: AbortSignal): Promise<GuideStatus | null> {
	if (!guideAvailable()) return null;
	const status = await getSetupGuide(signal);
	guide.status = status;
	return status;
}

/** Shows or hides the guide for this account; resolves with the new status */
export async function setGuideOpen(open: boolean): Promise<GuideStatus> {
	const status = await setSetupGuide(open);
	guide.status = status;
	return status;
}

/** Brings the guide back and opens the overview, where it is */
export async function bringGuideBack(): Promise<void> {
	await setGuideOpen(true);
	// eslint-disable-next-line svelte/no-navigation-without-resolve -- link() adds the base
	await goto(link('/'));
}
