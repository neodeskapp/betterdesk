/**
 * Server management has no terminal (A194): its old address answers the
 * console's not found page instead of opening another tab.
 */
import { isHttpError } from '@sveltejs/kit';
import { describe, expect, test } from 'vitest';
import { load } from '../../../src/routes/(console)/server-management/+page.ts';

const status = (search: string): number | null => {
	try {
		load({ url: new URL('http://localhost/server-management' + search) } as Parameters<typeof load>[0]);
		return null;
	} catch (err) {
		if (isHttpError(err)) return err.status;
		throw err;
	}
};

describe('server management page', () => {
	test('the old terminal tab is not found', () => {
		expect(status('?tab=terminal')).toBe(404);
		expect(status('?tab=Terminal')).toBe(404);
	});

	test('the other tabs and a missing tab open', () => {
		expect(status('')).toBeNull();
		expect(status('?tab=overview')).toBeNull();
		expect(status('?tab=files')).toBeNull();
		expect(status('?tab=services')).toBeNull();
		expect(status('?tab=nope')).toBeNull();
	});
});
