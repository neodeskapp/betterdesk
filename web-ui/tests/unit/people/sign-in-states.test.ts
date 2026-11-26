/**
 * What the users list says about an account's sign-in: locked, a new
 * password at the next sign-in, a reset link waiting, and the two-factor
 * column's three states.
 */
import * as v from 'valibot';
import { describe, expect, test } from 'vitest';
import { ConsoleUser } from '../../../src/lib/api/users.ts';
import { signInStates, twoFactorStatus } from '../../../src/lib/people/users.ts';

const user = (more: Record<string, unknown> = {}) =>
	v.parse(ConsoleUser, { id: 1, username: 'u', role: 'viewer', ...more });

describe('sign-in states', () => {
	test('an older server without the fields shows none', () => {
		expect(signInStates(user())).toEqual([]);
		expect(twoFactorStatus(user({ totp_enabled: true }))).toEqual({
			tone: 'good',
			key: 'users.two_factor_on'
		});
	});

	test('each state with its tone and the date of its title', () => {
		const states = signInStates(
			user({
				locked: true,
				locked_until: '2026-11-26T10:15:00Z',
				must_change_password: true,
				password_reset_until: '2026-11-27T09:00:00Z'
			})
		);
		expect(states).toEqual([
			{
				key: 'users.state_locked',
				tone: 'danger',
				titleKey: 'users.state_locked_until',
				date: '2026-11-26T10:15:00Z'
			},
			{ key: 'users.state_must_change', tone: 'warning' },
			{
				key: 'users.state_reset_waiting',
				tone: 'waiting',
				titleKey: 'users.state_reset_until',
				date: '2026-11-27T09:00:00Z'
			}
		]);
	});

	test('two-factor: on, off, required and not set up', () => {
		expect(twoFactorStatus(user({ two_factor_state: 'on' })).key).toBe('users.two_factor_on');
		expect(twoFactorStatus(user({ two_factor_state: 'off' }))).toEqual({
			tone: 'neutral',
			key: 'users.two_factor_off'
		});
		expect(twoFactorStatus(user({ two_factor_state: 'required' }))).toEqual({
			tone: 'warning',
			key: 'users.two_factor_required_state'
		});
	});
});
