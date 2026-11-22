/**
 * Disabling an account on the users page (L107): the list says which
 * accounts are disabled, and the action is offered where the server takes
 * it: an account the person may change, never their own.
 */
import * as v from 'valibot';
import { describe, expect, test } from 'vitest';
import { ConsoleUser } from '../../../src/lib/api/users.ts';
import { mayDisableUser } from '../../../src/lib/people/users.ts';

const user = (over: Record<string, unknown> = {}) =>
	v.parse(ConsoleUser, { id: 5, username: 'anna', role: 'operator', email: '', ...over });

describe('disabled accounts', () => {
	test('the list carries the flag; an older server that leaves it out means enabled', () => {
		expect(user({ disabled: true }).disabled).toBe(true);
		expect(user({ disabled: false }).disabled).toBe(false);
		expect(user().disabled).toBe(false);
	});

	test('who may disable or enable whom: whom they may change, never themselves', () => {
		expect(mayDisableUser('super_admin', '1', user({ id: 8, role: 'super_admin' }))).toBe(true);
		expect(mayDisableUser('global_admin', '1', user({ id: 6, role: 'operator' }))).toBe(true);
		expect(mayDisableUser('global_admin', '1', user({ id: 7, role: 'admin' }))).toBe(false);
		expect(mayDisableUser('server_admin', '1', user({ id: 9, role: 'viewer' }))).toBe(false);
		expect(mayDisableUser('super_admin', '5', user({ id: 5, role: 'super_admin' }))).toBe(false);
	});
});
