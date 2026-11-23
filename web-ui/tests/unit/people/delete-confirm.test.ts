/**
 * What Delete says on the Users page. A single sign-on or directory
 * account is back at its next sign-in while Auto-create users is on, so
 * "They can no longer sign in" is not true for it (L340).
 */
import { describe, expect, test } from 'vitest';
import { deleteConfirmKey } from '../../../src/lib/people/users.ts';

describe('the delete confirmation', () => {
	test('a local account can no longer sign in', () => {
		expect(deleteConfirmKey({ auth_provider: 'local' })).toBe('users.delete_confirm');
		expect(deleteConfirmKey({ auth_provider: '' })).toBe('users.delete_confirm');
	});

	test('a single sign-on or directory account is told it comes back', () => {
		expect(deleteConfirmKey({ auth_provider: 'oidc' })).toBe('users.delete_confirm_external');
		expect(deleteConfirmKey({ auth_provider: 'LDAP' })).toBe('users.delete_confirm_external');
	});
});
