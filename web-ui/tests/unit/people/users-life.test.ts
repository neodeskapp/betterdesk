/**
 * End dates and the inactive filter of the users list, without a DOM.
 */
import * as v from 'valibot';
import { describe, expect, test } from 'vitest';
import { ConsoleUser } from '../../../src/lib/api/users.ts';
import { expiryBody, filterInactive, inactiveFilter, localDate } from '../../../src/lib/people/users.ts';

const user = (id: number, last_login: string | null) =>
	v.parse(ConsoleUser, { id, username: `u${id}`, role: 'viewer', last_login });

describe('end date field', () => {
	test('the day the account has sends nothing, another day its start, empty takes it away', () => {
		const current = new Date('2026-12-31T00:00:00').toISOString();
		expect(localDate(current)).toBe('2026-12-31');
		expect(expiryBody('2026-12-31', current)).toEqual({});
		expect(expiryBody('', current)).toEqual({ expires_at: null });
		expect(expiryBody('', null)).toEqual({});
		expect(expiryBody('2027-01-15', current)).toEqual({
			expires_at: new Date('2027-01-15T00:00:00').toISOString()
		});
	});
});

describe('inactive filter (?inactive=)', () => {
	const now = Date.parse('2026-11-26T12:00:00Z');
	const users = [
		user(1, '2026-11-20 12:00:00'),
		user(2, '2026-09-20T12:00:00.000Z'),
		user(3, '2026-07-01 08:00:00'),
		user(4, null)
	];
	test('only the URL values count', () => {
		expect(inactiveFilter('90')).toBe('90');
		expect(inactiveFilter('never')).toBe('never');
		expect(inactiveFilter('45')).toBe('');
		expect(inactiveFilter(null)).toBe('');
	});
	test('keeps accounts whose last sign-in is that old, and those that never signed in', () => {
		const ids = (f: Parameters<typeof filterInactive>[1]) => filterInactive(users, f, now).map((u) => u.id);
		expect(ids('')).toEqual([1, 2, 3, 4]);
		expect(ids('30')).toEqual([2, 3, 4]);
		expect(ids('60')).toEqual([2, 3, 4]);
		expect(ids('90')).toEqual([3, 4]);
		expect(ids('never')).toEqual([4]);
	});
});
