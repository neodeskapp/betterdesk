/**
 * Where a server setting's value comes from, in the words of the settings
 * rows (lib/settings/sources.ts), from GET /api/panel/settings/sources.
 */
import { describe, expect, test } from 'vitest';
import { SettingsDraft } from '../../../src/lib/ui/settings/draft.svelte.ts';
import { sourceInfo, sourceRow, storedSource } from '../../../src/lib/settings/sources.ts';

describe('the source of a value', () => {
	test('stored, default and environment in words', () => {
		expect(sourceInfo({ value_source: 'stored', locked: false }).source).toEqual({
			kind: 'here',
			text: 'Set here'
		});
		expect(sourceInfo({ value_source: 'default', locked: false, default: 'open' }).source).toEqual({
			kind: 'default',
			text: 'Built-in default'
		});
		expect(
			sourceInfo({ value_source: 'environment', env_var: 'ENROLLMENT_MODE', locked: true }).source?.text
		).toBe('Set in the environment file (ENROLLMENT_MODE), change it there');
		expect(
			sourceInfo({ value_source: 'environment', env_var: 'SMTP_HOST', locked: false }).source?.text
		).toBe('Set in the environment file (SMTP_HOST). Saving here replaces it.');
	});

	test('nothing known is no line, and only a locked environment value locks the field', () => {
		expect(sourceInfo(undefined)).toEqual({
			source: null,
			lockedBy: '',
			hasDefault: false,
			default: undefined
		});
		expect(sourceInfo({ value_source: 'environment', env_var: 'X', locked: true }).lockedBy).toBe('X');
		expect(sourceInfo({ value_source: 'environment', env_var: 'X', locked: false }).lockedBy).toBe('');
	});

	test('a default of false or 0 is a default', () => {
		expect(sourceInfo({ value_source: 'stored', locked: false, default: false }).hasDefault).toBe(true);
		expect(sourceInfo({ value_source: 'stored', locked: false, default: 0 }).hasDefault).toBe(true);
		expect(sourceInfo({ value_source: 'stored', locked: false }).hasDefault).toBe(false);
	});

	test('without source data a row says "Set here" only for a stored value', () => {
		expect(storedSource(true)).toEqual({ kind: 'here', text: 'Set here' });
		expect(storedSource(false)).toBeNull();
	});
});

describe('a row with its source', () => {
	const draft = () => new SettingsDraft({ days: 14, mode: 'open' });

	test('Reset puts the default in the draft, and shows only while the value differs', () => {
		const d = draft();
		const info = sourceInfo({ value_source: 'stored', locked: false, default: 7 });
		const row = sourceRow(d, 'days', info);
		expect(row.resetLabel).toBe('Reset to default');
		row.onreset?.();
		expect(d.values.days).toBe(7);
		expect(d.isChanged('days')).toBe(true);
		expect(sourceRow(d, 'days', info).onreset).toBeUndefined();
	});

	test('a locked value has no Reset, and neither has a value without a default', () => {
		const d = draft();
		const locked = sourceInfo({ value_source: 'environment', env_var: 'M', locked: true, default: 'x' });
		expect(sourceRow(d, 'mode', locked).onreset).toBeUndefined();
		expect(locked.lockedBy).toBe('M');
		expect(
			sourceRow(d, 'days', sourceInfo({ value_source: 'stored', locked: false })).onreset
		).toBeUndefined();
	});

	test('the default is turned into the form value when the form holds it differently', () => {
		const d = new SettingsDraft({ servers: ['a.example'] });
		const info = sourceInfo({
			value_source: 'stored',
			locked: false,
			default: 'pool.ntp.org,time.google.com'
		});
		sourceRow(d, 'servers', info, (v) => String(v).split(',')).onreset?.();
		expect(d.values.servers).toEqual(['pool.ntp.org', 'time.google.com']);
	});

	test('a value the built-in default decides has no Reset; changed, Reset brings back what was loaded', () => {
		// Email, From address with nothing saved: the field is empty and the
		// server sends from its default, so the empty field is the default
		const d = new SettingsDraft({ from: '' });
		const info = sourceInfo({ value_source: 'default', locked: false, default: 'neodesk@localhost' });
		expect(sourceRow(d, 'from', info).onreset).toBeUndefined();
		d.set('from', 'help@example.org');
		sourceRow(d, 'from', info).onreset?.();
		expect(d.values.from).toBe('');
		expect(d.isChanged('from')).toBe(false);
	});

	test('the row says it is changed while the draft holds another value', () => {
		const d = draft();
		d.set('days', 3);
		expect(sourceRow(d, 'days', sourceInfo(undefined)).changed).toBe(true);
	});
});
