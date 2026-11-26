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
 *
 * A page that saves its changes to several endpoints in turn throws
 * PartlySaved from the save when only some of them took: those fields
 * count as saved, the others stay as changes, and the message says which.
 */

import { tf } from '#lib/i18n/i18n.svelte.ts';
import { trackDirty } from '#lib/settings/dirty.ts';
import { toast } from '../toasts.svelte.ts';

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

/** A save that stored some fields and not others; `message` says which and why */
export class PartlySaved extends Error {
	constructor(
		message: string,
		readonly saved: readonly string[]
	) {
		super(message);
	}
}

const same = (a: unknown, b: unknown): boolean => a === b || JSON.stringify(a) === JSON.stringify(b);

const copy = <T>(v: T): T => (v === undefined ? v : (JSON.parse(JSON.stringify(v)) as T));

/** A value in words for the list of changes */
export function inWords(value: unknown): string {
	if (value === true) return tf('settings_ui.on', 'on');
	if (value === false) return tf('settings_ui.off', 'off');
	if (value === null || value === undefined || value === '') return tf('settings_ui.empty', 'empty');
	if (Array.isArray(value))
		return value.length ? value.map(String).join(', ') : tf('settings_ui.none', 'none');
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

	/** Take the current values of these fields (all when none are named) as the saved ones */
	commit(fields?: readonly string[]): void {
		const next = copy(this.loaded) as Values;
		for (const f of fields ?? Object.keys(this.values)) next[f] = copy((this.values as Values)[f]);
		this.loaded = next as T;
	}

	discard(): void {
		this.values = copy(this.loaded);
		this.error = '';
	}

	get changes(): Change[] {
		const fields = [
			...Object.keys(this.loaded),
			...Object.keys(this.values).filter((k) => !(k in this.loaded))
		];
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
			const kept = err instanceof PartlySaved ? err.saved : null;
			this.lastSaved = kept ? changes.filter((c) => kept.includes(c.field)) : [];
			if (kept) this.commit([...kept]);
			this.error =
				err instanceof Error && err.message
					? err.message
					: tf('settings_ui.save_failed', 'Saving did not work.');
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

/** "Saved: View devices on, Delete devices off and 2 more" for the toast */
export function savedMessage(changes: readonly Change[], shown = 3): string {
	const parts = changes.slice(0, shown).map((c) => `${c.label} ${c.to}`);
	const rest = changes.length - parts.length;
	const list = parts.join(', ');
	return rest > 0
		? tf('settings_ui.saved_list_more', 'Saved: {list} and {count} more.', { list, count: rest })
		: tf('settings_ui.saved_list', 'Saved: {list}.', { list });
}

export interface ConfirmOptions {
	/**
	 * Puts the saved changes back (each change's fromValue). With it the
	 * toast offers Undo: for permissions, sign-in rules and policies.
	 */
	undo?: (changes: Change[]) => Promise<unknown>;
	/** After a save or an undo: read the data again */
	reload?: () => unknown;
}

/**
 * Save the draft with `run`, then confirm in a toast that names what
 * changed (PATTERN.md rule 11), with Undo when `undo` is given. Returns
 * whether it saved.
 */
export async function saveAndConfirm<T extends Values>(
	draft: SettingsDraft<T>,
	run: (changes: Change[]) => Promise<unknown>,
	{ undo, reload }: ConfirmOptions = {}
): Promise<boolean> {
	const ok = await draft.save(run);
	if (!ok) return false;
	const changes = draft.lastSaved;
	toast.show({
		type: 'success',
		message: savedMessage(changes),
		action: undo
			? {
					label: tf('settings_ui.undo', 'Undo'),
					undo: true,
					onclick: () => {
						void undo(changes)
							.then(async () => {
								await reload?.();
								toast.success(tf('settings_ui.undone', 'The changes are undone.'));
							})
							.catch((err: unknown) =>
								toast.error(
									`${tf('settings_ui.undo_failed', 'Undoing did not work.')} ${err instanceof Error ? err.message : ''}`.trim()
								)
							);
					}
				}
			: undefined
	});
	await reload?.();
	return true;
}
