/**
 * Backup tab: what a chosen file is, which parts can be restored, and when
 * the next scheduled backup runs (old settings.js initRestoreSection,
 * renderBackupSide). No DOM.
 */

import type { BackupPart, BackupStats, Schedule, ScheduleStatus } from '#lib/api/settings-system.ts';

type Translate = (key: string, params?: Record<string, string | number>) => string;

/*
 * What a backup holds comes from the server's contents answer
 * (GET /api/settings/backup/stats, `parts`), built from the plan the
 * archive follows: the description, the warning and "In a backup now"
 * read the same list. A fixed list in the text named the environment
 * file on Docker, where there is none (BUGS L557).
 */

/** A list in the page's language: "a, b, and c" */
function listText(items: readonly string[], locale?: string): string {
	try {
		return new Intl.ListFormat(locale, { type: 'conjunction' }).format(items);
	} catch {
		return items.join(', ');
	}
}

/** A label of a part; a part this console has no words for keeps its id */
function label(t: Translate, kind: 'part' | 'secret', id: string): string {
	const key = `settings.backup_tab.${kind}_${id}`;
	const text = t(key);
	return text === key ? id : text;
}

function included(stats: BackupStats | null | undefined): BackupPart[] {
	return (stats?.parts ?? []).filter((p) => p.included);
}

/** "Create a backup": what the file holds, as the server counts it now */
export function createNote(stats: BackupStats | null | undefined, t: Translate, locale?: string): string {
	const parts = included(stats);
	if (!parts.length) return t('settings.backup_tab.create_note_plain');
	return t('settings.backup_tab.create_note_parts', {
		parts: listText(
			parts.map((p) => label(t, 'part', p.id)),
			locale
		)
	});
}

/** The warning over "Create a backup": the secrets a backup made now holds */
export function securityWarning(
	stats: BackupStats | null | undefined,
	t: Translate,
	locale?: string
): string {
	const secrets = included(stats).filter((p) => p.secret);
	if (!secrets.length) return t('settings.backup_tab.warning_plain');
	return t('settings.backup_tab.warning_parts', {
		parts: listText(
			secrets.map((p) => label(t, 'secret', p.id)),
			locale
		)
	});
}

/**
 * A part as "In a backup now" shows it: included, or left out and why.
 * Undefined while the answer is not in or the server names no such part.
 */
export function partState(
	stats: BackupStats | null | undefined,
	id: string,
	t: Translate
): { included: boolean; reason: string } | undefined {
	const part = (stats?.parts ?? []).find((p) => p.id === id);
	if (!part) return undefined;
	if (part.included) return { included: true, reason: '' };
	const key = `settings.backup_tab.reason_${part.reason || 'missing'}`;
	const reason = t(key);
	return { included: false, reason: reason === key ? t('settings.backup_tab.reason_missing') : reason };
}

export type FileKind = '' | 'archive' | 'json' | 'invalid';

/** A .tar.gz (or .gz) archive, an old .json settings file, or neither */
export function fileKind(name: string | null | undefined): FileKind {
	if (!name) return '';
	const n = name.toLowerCase();
	if (n.endsWith('.json')) return 'json';
	if (n.endsWith('.gz')) return 'archive';
	return 'invalid';
}

export interface RestorePart {
	/** The form field the server reads */
	field: 'restoreDatabase' | 'restoreUploads' | 'restoreSecrets' | 'restoreEnv' | 'restoreGoDb';
	/** Element id (old page) */
	id: string;
	label: string;
	desc: string;
	/** Restoring it needs a service restart */
	restart: boolean;
}

export const RESTORE_PARTS: readonly RestorePart[] = [
	{
		field: 'restoreDatabase',
		id: 'restore-database',
		label: 'settings.backup_tab.opt_database',
		desc: 'settings.backup_tab.opt_database_desc',
		restart: false
	},
	{
		field: 'restoreUploads',
		id: 'restore-uploads',
		label: 'settings.backup_tab.opt_uploads',
		desc: 'settings.backup_tab.opt_uploads_desc',
		restart: false
	},
	{
		field: 'restoreSecrets',
		id: 'restore-secrets',
		label: 'settings.backup_tab.opt_secrets',
		desc: 'settings.backup_tab.opt_secrets_desc',
		restart: true
	},
	{
		field: 'restoreEnv',
		id: 'restore-env',
		label: 'settings.backup_tab.opt_env',
		desc: 'settings.backup_tab.opt_env_desc',
		restart: true
	},
	{
		field: 'restoreGoDb',
		id: 'restore-godb',
		label: 'settings.backup_tab.opt_godb',
		desc: 'settings.backup_tab.opt_godb_desc',
		restart: true
	}
];

/** What an old .json settings file restores (the server's defaults for it) */
export const JSON_RESTORE_ITEMS = [
	'backup.opt_settings',
	'settings.tab_branding',
	'backup.opt_folders',
	'backup.opt_groups',
	'backup.opt_addressbooks'
] as const;

/** The step hint under the file field: what is still missing ('' when nothing) */
export function restoreHint(kind: FileKind, picked: number): string {
	if (!kind) return 'settings.backup_tab.restore_start';
	if (kind === 'archive' && picked === 0) return 'settings.backup_tab.restore_pick_parts';
	return '';
}

/** Restore can start: an old settings file, or an archive with at least one part ticked */
export function canRestore(kind: FileKind, picked: number): boolean {
	return kind === 'json' || (kind === 'archive' && picked > 0);
}

/** Every part goes to the server, ticked or not ('true'/'false', as the old page sent them) */
export function restoreFields(picked: ReadonlySet<RestorePart['field']>): Record<string, boolean> {
	return Object.fromEntries(RESTORE_PARTS.map((p) => [p.field, picked.has(p.field)]));
}

/** The name the server gives each part of a full backup in its result */
const PART_IDS: Record<RestorePart['field'], string> = {
	restoreDatabase: 'database',
	restoreUploads: 'uploads',
	restoreSecrets: 'secrets/keys',
	restoreEnv: '.env',
	restoreGoDb: 'goserver-db'
};

/**
 * What a restore brought back: everything that was chosen (ok), only some
 * of it or with warnings (partial), or nothing at all (none). A green
 * "Restored." for a restore that skipped every part misled (BUGS L408).
 */
export function restoreOutcome(
	result: {
		type?: string | undefined;
		restored: readonly string[];
		skipped: readonly string[];
		warnings: readonly string[];
	},
	picked: ReadonlySet<RestorePart['field']>
): 'ok' | 'partial' | 'none' {
	if (!result.restored.length) return 'none';
	if (result.warnings.length) return 'partial';
	if (result.type !== 'full') return 'ok';
	const skipped = [...picked].some((field) => result.skipped.includes(PART_IDS[field]));
	return skipped ? 'partial' : 'ok';
}

/** A part of the server's result as the choices name it; others as the server wrote them */
export function restoredPartLabel(part: string, t: (key: string) => string): string {
	const id = part.replace(/ \(.*\)$/, '');
	const found = RESTORE_PARTS.find((p) => PART_IDS[p.field] === id);
	return found ? t(found.label) : part;
}

export function restoreNeedsRestart(kind: FileKind, picked: ReadonlySet<RestorePart['field']>): boolean {
	return kind === 'archive' && RESTORE_PARTS.some((p) => p.restart && picked.has(p.field));
}

export type NextBackup = { kind: 'unknown' } | { kind: 'off' } | { kind: 'soon' } | { kind: 'at'; at: Date };

/**
 * When the next scheduled backup runs. The server checks once a minute
 * whether one is due, so anything due within a minute is "soon".
 */
export function nextBackup(
	schedule: Schedule | null | undefined,
	status: ScheduleStatus | null | undefined,
	now: number = Date.now()
): NextBackup {
	if (!schedule) return { kind: 'unknown' };
	if (!schedule.enabled) return { kind: 'off' };
	const last = Date.parse(status?.lastAttemptAt ?? '');
	const due = Number.isFinite(last) ? last + (Number(schedule.intervalHours) || 24) * 3600 * 1000 : now;
	return due <= now + 60 * 1000 ? { kind: 'soon' } : { kind: 'at', at: new Date(due) };
}

/** Schedule choices the old page offered (hours) */
export const INTERVALS = [
	{ value: 1, label: 'backup.schedule_interval_1h' },
	{ value: 6, label: 'backup.schedule_interval_6h' },
	{ value: 12, label: 'backup.schedule_interval_12h' },
	{ value: 24, label: 'backup.schedule_interval_24h' },
	{ value: 168, label: 'backup.schedule_interval_weekly' }
] as const;
