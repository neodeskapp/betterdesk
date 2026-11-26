/**
 * A list that loads again when the server pushes a change (a new help
 * request, a registration decided elsewhere). While live updates are
 * paused (the accessibility setting, WCAG 2.2.2) a push does not change
 * the list under the reader: the list only notes that changes wait, says
 * so once through the polite live region, and NewEntries.svelte shows
 * "New entries, show them" above it. Showing them loads the list; so does
 * the end of the pause.
 *
 *   const held = new HeldPushes(load);
 *   async function load() { const from = held.begin(); ...; held.loaded(from); }
 *   onPanelEvents((p) => { if (p.type === 'help_request') held.push(); });
 *   <NewEntries {held} focus={() => listElement} />
 */

import { t } from '#lib/i18n/i18n.svelte.ts';
import { announce } from '#lib/ui/announce.svelte.ts';
import { liveUpdates } from './live-updates.svelte.ts';

export class HeldPushes {
	/** Changes were pushed while paused and are not on screen yet */
	waiting = $state(false);
	/** Pushes held so far; a load covers those before it began */
	#held = 0;
	readonly #load: () => Promise<void>;

	constructor(load: () => Promise<void>) {
		this.#load = load;
	}

	/** The server pushed a change: load now, or hold it while paused */
	push(): void {
		if (!liveUpdates.paused) {
			void this.#load().catch(() => undefined);
			return;
		}
		this.#held++;
		if (this.waiting) return;
		this.waiting = true;
		announce(t('live.new_entries_waiting'));
	}

	/** A load starts: what it will show (for loaded) */
	begin(): number {
		return this.#held;
	}

	/**
	 * A load that began at `from` is on screen (the button, Refresh, a
	 * timed load): the pushes before it are shown. One that came while it
	 * ran still waits.
	 */
	loaded(from: number): void {
		if (from >= this.#held) this.waiting = false;
	}

	/** Load the held changes (the button, or the end of the pause) */
	async show(): Promise<void> {
		const from = this.begin();
		await this.#load();
		this.loaded(from);
	}
}
