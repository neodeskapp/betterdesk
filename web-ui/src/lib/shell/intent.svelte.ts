/**
 * Something the search asked a page to do once it shows: open Add a
 * computer on All devices, the new token dialog on API tokens. The search
 * sets it and goes to the page; the page takes it, so it happens once,
 * also when the page is open already.
 *
 *   $effect(() => { if (takeIntent('add-computer')) addComputer = true; });
 */

export type Intent = 'add-computer' | 'new-device-token' | 'add-user' | 'new-api-token' | 'chat-settings';

class PendingIntent {
	name = $state<Intent | null>(null);
}

export const pendingIntent = new PendingIntent();

export function requestIntent(name: Intent): void {
	pendingIntent.name = name;
}

/** True once when this intent is waiting; reading it in an effect follows it */
export function takeIntent(name: Intent): boolean {
	if (pendingIntent.name !== name) return false;
	pendingIntent.name = null;
	return true;
}
