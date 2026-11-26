/**
 * Server › Backups: what a backup holds, the page's settings as one draft
 * (the schedule and "Keep update backups"), the restore's steps, and when
 * the next scheduled backup runs (old settings.js initRestoreSection,
 * renderBackupSide). No DOM.
 */

import type {
	BackupPart,
	BackupStats,
	Schedule,
	ScheduleInput,
	ScheduleStatus
} from '#lib/api/settings-system.ts';

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

export interface PartRow {
	id: string;
	label: string;
	included: boolean;
	/** Why it is left out; '' when included */
	reason: string;
	secret: boolean;
}

/** Every part the server names, in its order, for the summary "What a backup holds now" */
export function partRows(stats: BackupStats | null | undefined, t: Translate): PartRow[] {
	return (stats?.parts ?? []).map((p) => ({
		id: p.id,
		label: label(t, 'part', p.id),
		included: !!p.included,
		reason: partState(stats, p.id, t)?.reason ?? '',
		secret: !!p.secret
	}));
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
 * When the next scheduled backup runs: the time the server names
 * (`serverNext`, which knows a time of day), else counted from the last
 * attempt as a server without that answer does. The server checks once a
 * minute whether one is due, so anything due within a minute is "soon".
 */
export function nextBackup(
	schedule: Schedule | null | undefined,
	status: ScheduleStatus | null | undefined,
	now: number = Date.now(),
	serverNext?: string | null
): NextBackup {
	if (!schedule) return { kind: 'unknown' };
	if (!schedule.enabled) return { kind: 'off' };
	const named = Date.parse(serverNext ?? '');
	const last = Date.parse(status?.lastAttemptAt ?? '');
	const due = Number.isFinite(named)
		? named
		: Number.isFinite(last)
			? last + (Number(schedule.intervalHours) || 24) * 3600 * 1000
			: now;
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

// ── The page's settings as one draft ──

/** "Keep all" or a number, for "Keep update backups" (the server stores 0 for all) */
export interface KeepValue {
	all: boolean;
	count: number;
}

/** The number shown when "Keep all update backups" is switched off for the first time */
export const UPDATE_KEEP_DEFAULT = 5;

export function updateKeepFromStored(n: number | null | undefined): KeepValue {
	const count = Number(n);
	return Number.isInteger(count) && count > 0
		? { all: false, count }
		: { all: true, count: UPDATE_KEEP_DEFAULT };
}

export function updateKeepToStored(k: KeepValue): number {
	return k.all ? 0 : k.count;
}

/** Every setting on the page, flat, for the SettingsDraft */
export interface BackupValues {
	[field: string]: unknown;
	enabled: boolean;
	intervalHours: number;
	timeOfDay: string;
	retentionCount: number | null;
	destinationType: 'local' | 'path';
	destinationPath: string;
	updateKeep: KeepValue;
}

export function backupValues(
	schedule: (Partial<Schedule> & { timeOfDay?: string }) | null | undefined,
	updateKeep: number | null | undefined
): BackupValues {
	return {
		enabled: !!schedule?.enabled,
		intervalHours: Number(schedule?.intervalHours) || 24,
		timeOfDay: schedule?.timeOfDay ?? '',
		retentionCount: Number(schedule?.retentionCount) || 7,
		destinationType: schedule?.destinationType === 'path' ? 'path' : 'local',
		destinationPath: schedule?.destinationPath ?? '',
		updateKeep: updateKeepFromStored(updateKeep)
	};
}

/** The fields the schedule's save carries */
export const SCHEDULE_FIELDS = [
	'enabled',
	'intervalHours',
	'timeOfDay',
	'retentionCount',
	'destinationType',
	'destinationPath'
] as const;

/** Which of the two saves the changed fields need */
export function savePlan(changed: readonly string[]): { schedule: boolean; updateKeep: boolean } {
	return {
		schedule: changed.some((f) => (SCHEDULE_FIELDS as readonly string[]).includes(f)),
		updateKeep: changed.includes('updateKeep')
	};
}

/** Daily, weekly and every other whole number of days can run at a time of day */
export function takesTimeOfDay(intervalHours: number): boolean {
	return Number.isInteger(intervalHours) && intervalHours > 0 && intervalHours % 24 === 0;
}

/** A new interval; an interval of hours has no time of day */
export function withInterval(
	timeOfDay: string,
	intervalHours: number
): { intervalHours: number; timeOfDay: string } {
	return { intervalHours, timeOfDay: takesTimeOfDay(intervalHours) ? timeOfDay : '' };
}

const CLOCK = /^([01]\d|2[0-3]):[0-5]\d$/;

/** The times of day offered: every full hour, and a stored time between them */
export function timeOfDayOptions(current: string): string[] {
	const hours = Array.from({ length: 24 }, (_, h) => `${String(h).padStart(2, '0')}:00`);
	if (CLOCK.test(current) && !hours.includes(current)) hours.push(current);
	return hours.sort();
}

export function scheduleBody(v: BackupValues): ScheduleInput {
	return {
		enabled: v.enabled,
		intervalHours: v.intervalHours,
		timeOfDay: takesTimeOfDay(v.intervalHours) ? v.timeOfDay : '',
		retentionCount: Number(v.retentionCount),
		destinationType: v.destinationType,
		destinationPath: v.destinationPath
	};
}

type Say = (key: string, fallback: string, params?: Record<string, string | number>) => string;

const whole = (n: unknown, min: number, max: number): boolean =>
	typeof n === 'number' && Number.isInteger(n) && n >= min && n <= max;

export type BackupErrors = Partial<Record<'retentionCount' | 'destinationPath' | 'updateKeep', string>>;

/** Why a value is refused, by field; a schedule that is off is not checked */
export function backupErrors(v: BackupValues, say: Say): BackupErrors {
	const out: BackupErrors = {};
	if (v.enabled && !whole(v.retentionCount, 1, 365))
		out.retentionCount = say('settings.backup_tab.retention_error', 'Enter a whole number from 1 to 365.');
	if (v.enabled && v.destinationType === 'path' && !v.destinationPath.trim())
		out.destinationPath = say('backups_page.path_required', 'Enter the folder on the server.');
	if (!v.updateKeep.all && !whole(v.updateKeep.count, 1, 1000))
		out.updateKeep = say('backups_page.update_keep_error', 'Enter a whole number from 1 to 1000.');
	return out;
}

/** How many update backups "Delete older update backups now" removes, with the saved number */
export function pruneCount(total: number, keep: KeepValue): number {
	return keep.all ? 0 : Math.max(0, total - keep.count);
}

// ── Restore in three steps ──

export type RestoreStep = 'file' | 'parts' | 'confirm';

/** 1 choose a file, 2 see what is in it and pick, 3 confirm (the only red step) */
export function restoreStep(kind: FileKind, confirming: boolean): RestoreStep {
	if (kind !== 'archive' && kind !== 'json') return 'file';
	return confirming ? 'confirm' : 'parts';
}
