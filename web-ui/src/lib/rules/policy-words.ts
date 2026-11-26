/**
 * Policy settings in words: each setting's name and a stored value as the
 * Policies page shows it ("Session approval", "Attended", "On"). The
 * Policies page and an organization's Settings tab use the same words.
 *
 *   fieldLabel('connection.mode')            // "Session approval"
 *   valueWords('connection.mode', 'attended') // "Attended: ..."
 */

import { t, tf } from '#lib/i18n/i18n.svelte.ts';
import { kindOf } from './policies.ts';

/** The string key of each setting's name, by field id ("connection.mode") */
export const FIELD_LABEL: Readonly<Record<string, string>> = {
	'connection.mode': 'policies.connection_mode',
	'connection.max_session_duration': 'policies.max_session',
	'connection.idle_lock_timeout': 'policies.idle_lock',
	'connection.wallpaper': 'policies.wallpaper_policy',
	'features.allow_file_transfer': 'policies.allow_file_transfer',
	'features.allow_clipboard': 'policies.allow_clipboard',
	'features.allow_audio': 'policies.allow_audio',
	'features.allow_recording': 'policies.allow_recording',
	'security.require_2fa': 'policies.require_2fa',
	'security.password_min_length': 'policies.password_min_length',
	'security.password_require_special': 'policies.password_require_special',
	'security.password_max_age_days': 'policies.password_max_age',
	'security.allowed_operators': 'policies.allowed_operators',
	'network.block_direct_p2p': 'policies.block_direct_p2p',
	'network.allowed_relay_servers': 'policies.allowed_relays',
	'network.ip_allowlist': 'policies.ip_allowlist',
	'update.auto_update': 'policies.auto_update',
	'update.channel': 'policies.update_channel'
};

const CHOICE_LABEL: Readonly<Record<string, string>> = {
	attended: 'policies.mode_attended',
	unattended: 'policies.mode_unattended',
	ask_always: 'policies.mode_ask',
	show: 'policies.wallpaper_show',
	hide_during_session: 'policies.wallpaper_hide_session',
	hide_always: 'policies.wallpaper_hide_always',
	stable: 'policies.channel_stable',
	beta: 'policies.channel_beta',
	dev: 'policies.channel_dev'
};

/** A setting's name; the field id when the console has none for it */
export function fieldLabel(field: string): string {
	const key = FIELD_LABEL[field];
	return key ? t(key) : field;
}

/** A choice in words ("Attended: ..."); the stored word when the console has none */
export function choiceLabel(value: string): string {
	const key = CHOICE_LABEL[value];
	return key ? t(key) : value;
}

export const onWord = () => tf('policies.switch_on', 'On');
export const offWord = () => tf('policies.switch_off', 'Off');

/** A stored value in words: On or Off, a choice's name, a list joined, a number as it is */
export function valueWords(field: string, value: unknown): string {
	const kind = kindOf(field);
	if (kind?.kind === 'switch') return value ? onWord() : offWord();
	if (kind?.kind === 'choice') return choiceLabel(String(value));
	if (Array.isArray(value)) return value.length ? value.join(', ') : tf('settings_ui.none', 'none');
	if (value && typeof value === 'object') return JSON.stringify(value);
	return String(value);
}
