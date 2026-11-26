/**
 * Server › Backups as one settings page: the schedule and "Keep update
 * backups" in one draft saved by one bar, the update backups' "keep all"
 * as a switch instead of 0, a time of day for daily and weekly backups,
 * and the restore as three steps.
 */
import { describe, expect, test } from 'vitest';

import {
	backupErrors,
	backupValues,
	nextBackup,
	pruneCount,
	restoreStep,
	savePlan,
	scheduleBody,
	takesTimeOfDay,
	timeOfDayOptions,
	updateKeepFromStored,
	updateKeepToStored,
	withInterval
} from '../../../src/lib/settings-b/backup.ts';

const say = (key: string) => key;

const stored = {
	enabled: true,
	intervalHours: 24,
	retentionCount: 14,
	destinationType: 'path' as const,
	destinationPath: '/mnt/backups',
	timeOfDay: '02:00'
};

describe('keep update backups', () => {
	test('the stored 0 is "keep all", a number is that number', () => {
		expect(updateKeepFromStored(0)).toEqual({ all: true, count: 5 });
		expect(updateKeepFromStored(null)).toEqual({ all: true, count: 5 });
		expect(updateKeepFromStored(12)).toEqual({ all: false, count: 12 });
		expect(updateKeepToStored({ all: true, count: 12 })).toBe(0);
		expect(updateKeepToStored({ all: false, count: 12 })).toBe(12);
	});

	test('a number to keep is 1 to 1000; keep all needs none', () => {
		const v = backupValues(stored, 0);
		expect(backupErrors(v, say)).toEqual({});
		expect(backupErrors({ ...v, updateKeep: { all: false, count: 0 } }, say).updateKeep).toBeTruthy();
		expect(backupErrors({ ...v, updateKeep: { all: false, count: 1001 } }, say).updateKeep).toBeTruthy();
		expect(backupErrors({ ...v, updateKeep: { all: false, count: 2.5 } }, say).updateKeep).toBeTruthy();
		expect(backupErrors({ ...v, updateKeep: { all: true, count: Number.NaN } }, say)).toEqual({});
	});

	test('"Delete older update backups now" deletes what is over the saved number', () => {
		expect(pruneCount(9, { all: false, count: 5 })).toBe(4);
		expect(pruneCount(3, { all: false, count: 5 })).toBe(0);
		expect(pruneCount(9, { all: true, count: 5 })).toBe(0);
	});
});

describe('one draft for the page', () => {
	test('loads the schedule and the update backups count as one set of values', () => {
		expect(backupValues(stored, 3)).toEqual({
			enabled: true,
			intervalHours: 24,
			timeOfDay: '02:00',
			retentionCount: 14,
			destinationType: 'path',
			destinationPath: '/mnt/backups',
			updateKeep: { all: false, count: 3 }
		});
		// Nothing stored yet: off, every 24 hours, no time of day, keep 7, keep all update backups
		expect(backupValues(null, undefined)).toEqual({
			enabled: false,
			intervalHours: 24,
			timeOfDay: '',
			retentionCount: 7,
			destinationType: 'local',
			destinationPath: '',
			updateKeep: { all: true, count: 5 }
		});
	});

	test('saves only what changed: the schedule, the update backups count, or both', () => {
		expect(savePlan(['retentionCount'])).toEqual({ schedule: true, updateKeep: false });
		expect(savePlan(['updateKeep'])).toEqual({ schedule: false, updateKeep: true });
		expect(savePlan(['timeOfDay', 'updateKeep'])).toEqual({ schedule: true, updateKeep: true });
	});

	test('the schedule body carries the time of day only for whole days', () => {
		const v = backupValues(stored, 0);
		expect(scheduleBody(v)).toEqual({
			enabled: true,
			intervalHours: 24,
			timeOfDay: '02:00',
			retentionCount: 14,
			destinationType: 'path',
			destinationPath: '/mnt/backups'
		});
		expect(scheduleBody({ ...v, intervalHours: 6 }).timeOfDay).toBe('');
	});

	test('scheduled backups to keep is 1 to 365; a folder needs its path', () => {
		const v = backupValues(stored, 0);
		expect(backupErrors({ ...v, retentionCount: 0 }, say).retentionCount).toBeTruthy();
		expect(backupErrors({ ...v, retentionCount: 366 }, say).retentionCount).toBeTruthy();
		expect(backupErrors({ ...v, retentionCount: null }, say).retentionCount).toBeTruthy();
		expect(backupErrors({ ...v, destinationPath: '  ' }, say).destinationPath).toBeTruthy();
		expect(backupErrors({ ...v, destinationType: 'local', destinationPath: '' }, say)).toEqual({});
		// A schedule that is off is not checked
		expect(backupErrors({ ...v, enabled: false, retentionCount: 0 }, say)).toEqual({});
	});
});

describe('time of day', () => {
	test('only daily and weekly backups have one', () => {
		expect(takesTimeOfDay(24)).toBe(true);
		expect(takesTimeOfDay(168)).toBe(true);
		expect(takesTimeOfDay(6)).toBe(false);
		expect(takesTimeOfDay(1)).toBe(false);
	});

	test('choosing an interval without days clears the time; with days it stays', () => {
		expect(withInterval('02:00', 6)).toEqual({ intervalHours: 6, timeOfDay: '' });
		expect(withInterval('02:00', 168)).toEqual({ intervalHours: 168, timeOfDay: '02:00' });
	});

	test('every hour of the day, and a stored time that is not on the hour', () => {
		const hours = timeOfDayOptions('');
		expect(hours).toHaveLength(24);
		expect(hours[0]).toBe('00:00');
		expect(hours[2]).toBe('02:00');
		const odd = timeOfDayOptions('02:30');
		expect(odd).toHaveLength(25);
		expect(odd.indexOf('02:30')).toBe(3);
	});

	test('the next backup is the time the server names', () => {
		const now = Date.parse('2026-11-10T10:00:00Z');
		const s = { ...stored, destinationPath: '' };
		expect(nextBackup(s, null, now, '2026-11-11T02:00:00.000Z')).toEqual({
			kind: 'at',
			at: new Date('2026-11-11T02:00:00.000Z')
		});
		expect(nextBackup(s, null, now, '2026-11-10T10:00:20.000Z')).toEqual({ kind: 'soon' });
		expect(nextBackup({ ...s, enabled: false }, null, now, null)).toEqual({ kind: 'off' });
	});
});

describe('restore steps', () => {
	test('choose a file, then what to restore, then confirm', () => {
		expect(restoreStep('', false)).toBe('file');
		expect(restoreStep('invalid', false)).toBe('file');
		expect(restoreStep('archive', false)).toBe('parts');
		expect(restoreStep('json', false)).toBe('parts');
		expect(restoreStep('archive', true)).toBe('confirm');
		// No confirmation without a file the server reads
		expect(restoreStep('invalid', true)).toBe('file');
	});
});
