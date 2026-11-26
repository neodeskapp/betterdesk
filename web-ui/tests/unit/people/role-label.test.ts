/**
 * A role's name as the console shows it: its label where the language has
 * one, else the name as it is. The import preview names the roles of a CSV
 * file this way, so an unknown role stays visible as typed.
 */
import { describe, expect, test } from 'vitest';
import { roleLabel } from '../../../src/lib/people/users.ts';

const texts: Record<string, string> = { 'users.role_operator': 'Remote operator' };
const t = (key: string) => texts[key] ?? key;

describe('roleLabel', () => {
	test('a known role gets its label, an unknown one stays as typed', () => {
		expect(roleLabel('operator', t)).toBe('Remote operator');
		expect(roleLabel('wizard', t)).toBe('wizard');
	});
});
