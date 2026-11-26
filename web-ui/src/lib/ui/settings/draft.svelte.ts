/**
 * The unsaved state of a settings page (PATTERN.md rule 1: edit, then
 * save). It holds the values as loaded and as edited, flat by field id
 * ("device.view", "connection.mode"), and lists what changed in words for
 * the ChangesBar and the toast after saving:
 *
 *   const draft = new SettingsDraft(values, { keep: { label: t('...'), format: (v) => ... } });
 *   $effect(() => draft.track());
 *   draft.set('keep', 7);
 *   await draft.save((changes) => api.save(changes));
 *
 * track() registers the draft with lib/settings/dirty.ts, so the page frame
 * asks before the page is left while it has changes.
 */

import { tf } from '#lib/i18n/i18n.svelte.ts';
import { trackDirty } from '#lib/settings/dirty.ts';

export type Values = Record<string, unknown>;

export interface FieldSpec {
	/** The setting's name as the page shows it */
	label: string;
	/** The value in words; booleans default to on and off, lists to their entries */
	format?: (value: unknown) => string;
}

export interface Change {
	field: string;
	label: string;
	from: string;
	to: string;
	fromValue: unknown;
	toValue: unknown;
}

const same = (a: unknown, b: unknown): boolean => a === b || JSON.stringify(a) === JSON.stringify(b);

const copy = <T>(v: T): T => (v === undefined ? v : (JSON.parse(JSON.stringify(v)) as T));

/** A value in words for the list of changes */
export function inWords(value: unknown): string {
	if (value === true) return tf('settings_ui.on', 'on');
	if (value === false) return tf('settings_ui.off', 'off');
	if (value === null || value === undefined || value === '') return tf('settings_ui.empty', 'empty');
	if (Array.isArray(value)) return value.length ? value.map(String).join(', ') : tf('settings_ui.none', 'none');
	return String(value);
}

export class SettingsDraft<T extends Values = Values> {
	loaded = $state.raw<T>({} as T);
	values = $state<T>({} as T);
	busy = $state(false);
	/** Why the last save failed; '' when it worked or none ran */
	error = $state('');
	/** What the last successful save changed (for the toast and Undo) */
	lastSaved = $state.raw<Change[]>([]);

	#specs: Partial<Record<string, FieldSpec>>;

	constructor(initial: T, specs: Partial<Record<keyof T & string, FieldSpec>> = {}) {
		this.#specs = specs;
		this.load(initial);
	}

	/** Replace what is shown with fresh data; unsaved changes are dropped */
	load(values: T): void {
		this.loaded = copy(values);
		this.values = copy(values);
		this.error = '';
	}

	/** Add or change the name and wording of fields (for fields known only after loading) */
	describe(specs: Partial<Record<string, FieldSpec>>): void {
		this.#specs = { ...this.#specs, ...specs };
	}

	label(field: string): string {
		return this.#specs[field]?.label ?? field;
	}

	format(field: string, value: unknown): string {
		const f = this.#specs[field]?.format;
		return f ? f(value) : inWords(value);
	}

	set<K extends keyof T & string>(field: K, value: T[K]): void {
		this.values[field] = value;
		this.error = '';
	}

	isChanged(field: string): boolean {
		return !same(this.values[field], this.loaded[field]);
	}

	reset(field: string): void {
		(this.values as Values)[field] = copy(this.loaded[field]);
	}

	discard(): void {
		this.values = copy(this.loaded);
		this.error = '';
	}

	get changes(): Change[] {
		const fields = new Set([...Object.keys(this.loaded), ...Object.keys(this.values)]);
		const out: Change[] = [];
		for (const field of fields) {
			const fromValue = this.loaded[field];
			const toValue = this.values[field];
			if (same(fromValue, toValue)) continue;
			out.push({
				field,
				label: this.label(field),
				from: this.format(field, fromValue),
				to: this.format(field, toValue),
				fromValue: copy(fromValue),
				toValue: copy(toValue)
			});
		}
		return out;
	}

	get dirty(): boolean {
		return this.changes.length > 0;
	}

	/**
	 * Save the changes with `run`. On success the edited values become the
	 * loaded ones and `lastSaved` lists what changed; on failure the changes
	 * stay and `error` says why. Returns whether it worked.
	 */
	async save(run: (changes: Change[]) => Promise<unknown>): Promise<boolean> {
		const changes = this.changes;
		if (!changes.length || this.busy) return false;
		this.busy = true;
		this.error = '';
		this.lastSaved = changes;
		try {
			await run(changes);
			const saved = copy(this.values);
			this.loaded = saved;
			return true;
		} catch (err) {
			this.lastSaved = [];
			this.error = err instanceof Error && err.message ? err.message : tf('settings_ui.save_failed', 'Saving did not work.');
			return false;
		} finally {
			this.busy = false;
		}
	}

	/** Make the page ask before it is left while this draft has changes. Returns the undo (fits an $effect) */
	track(): () => void {
		return trackDirty(
			() => this.dirty,
			() => this.busy
		);
	}
}
