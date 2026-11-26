/**
 * Where a server setting's value comes from, in the words of a settings
 * row (PATTERN.md rule 2), from GET /api/panel/settings/sources
 * (getSettingSources): "Built-in default", "Set here", "Set in the
 * environment file (ENROLLMENT_MODE), change it there". A value the
 * environment locks can't be changed on the page and has no Reset.
 *
 *   const info = sourceInfo(sources['smtp.port']);
 *   <SettingRow ... {...sourceRow(draft, 'port', info)} disabled={info.lockedBy !== ''} />
 *
 * Settings the server reports no source for (LDAP, single sign-on,
 * MeshCentral) use storedSource: "Set here" while a value is stored,
 * nothing otherwise, and no made-up default.
 */

import type { SettingSource } from '#lib/api/settings.ts';
import { tf } from '#lib/i18n/i18n.svelte.ts';
import type { SettingsDraft, Values } from '#lib/ui/settings/draft.svelte.ts';
import type { ValueSource } from '#lib/ui/settings/SettingRow.svelte';

export interface SourceInfo {
	/** The row's source line; null when the server said nothing */
	source: ValueSource | null;
	/** The variable that locks the value ('' when the page may change it) */
	lockedBy: string;
	hasDefault: boolean;
	/** The built-in default as the server applies it */
	default: unknown;
}

const NONE: SourceInfo = { source: null, lockedBy: '', hasDefault: false, default: undefined };

export function sourceInfo(src: SettingSource | undefined): SourceInfo {
	if (!src) return NONE;
	const name = src.env_var ?? '';
	const hasDefault = src.default !== undefined && src.default !== null;
	const base = { hasDefault, default: hasDefault ? src.default : undefined };
	if (src.value_source === 'environment') {
		const locked = src.locked;
		const text = locked
			? name
				? tf('settings_ui.source_env_locked', 'Set in the environment file ({name}), change it there', {
						name
					})
				: tf('settings_ui.source_env_locked_unnamed', 'Set in the environment file, change it there')
			: name
				? tf('settings_ui.source_env', 'Set in the environment file ({name}). Saving here replaces it.', {
						name
					})
				: tf('settings_ui.source_env_unnamed', 'Set in the environment file. Saving here replaces it.');
		return { ...base, source: { kind: 'environment', text }, lockedBy: locked ? name || '?' : '' };
	}
	if (src.value_source === 'stored') return { ...base, source: here(), lockedBy: '' };
	return {
		...base,
		source: { kind: 'default', text: tf('settings_ui.source_default', 'Built-in default') },
		lockedBy: ''
	};
}

function here(): ValueSource {
	return { kind: 'here', text: tf('settings_ui.source_here', 'Set here') };
}

/** For settings without source data: "Set here" while a value is stored */
export function storedSource(stored: boolean): ValueSource | null {
	return stored ? here() : null;
}

const same = (a: unknown, b: unknown) => a === b || JSON.stringify(a) === JSON.stringify(b);

export interface SourceRow {
	changed: boolean;
	source: ValueSource | null;
	/** Back to the built-in default; left out while the value is the default, locked or has none */
	onreset: (() => void) | undefined;
	resetLabel: string;
}

/**
 * The props of a row for one draft field: changed mark, source line and
 * Reset to the default. `toForm` turns the server's default into the value
 * the form holds (a list for a comma-separated text).
 */
export function sourceRow<T extends Values>(
	draft: SettingsDraft<T>,
	field: keyof T & string,
	info: SourceInfo,
	toForm: (value: unknown) => unknown = (v) => v
): SourceRow {
	const locked = info.lockedBy !== '';
	const changed = draft.isChanged(field);
	// While the built-in default is in force, the value as loaded is the
	// default as the form holds it (an empty From address the server fills
	// in): no Reset then, and Reset after a change goes back to it
	const fromDefault = info.source?.kind === 'default';
	const def = info.hasDefault ? toForm(info.default) : undefined;
	const resettable = fromDefault
		? info.hasDefault && !locked && changed
		: info.hasDefault && !locked && !same(draft.values[field], def);
	const reset = fromDefault ? () => draft.reset(field) : () => draft.set(field, def as T[typeof field]);
	return {
		changed,
		source: info.source,
		onreset: resettable ? reset : undefined,
		resetLabel: tf('settings_ui.reset_default', 'Reset to default')
	};
}
