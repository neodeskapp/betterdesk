/**
 * GET /api/panel/settings/sources: for each server setting, where the
 * value in force comes from (saved, the environment, the built-in
 * default), whether the environment locks it, and the default the server
 * applies. The pages show it next to the setting (settings pattern rule 2).
 */
import { afterEach, describe, expect, test, vi } from 'vitest';
import { getSettingSources } from '../../../src/lib/api/settings.ts';

const requests: string[] = [];

function answer(body: unknown) {
	vi.stubGlobal(
		'fetch',
		vi.fn(async (input: RequestInfo | URL) => {
			requests.push(String(input));
			return new Response(JSON.stringify(body), {
				status: 200,
				headers: { 'Content-Type': 'application/json' }
			});
		})
	);
}

afterEach(() => {
	vi.unstubAllGlobals();
	requests.length = 0;
});

describe('getSettingSources', () => {
	test('reads each entry as the server writes it', async () => {
		answer({
			success: true,
			data: {
				'enrollment.mode': {
					value_source: 'environment',
					env_var: 'ENROLLMENT_MODE',
					locked: true,
					default: 'open'
				},
				'two_factor.required': { value_source: 'default', locked: false, default: false },
				'smtp.host': { value_source: 'stored', env_var: 'SMTP_HOST', locked: false }
			}
		});
		const sources = await getSettingSources();
		expect(requests).toEqual(['/api/panel/settings/sources']);
		expect(sources['enrollment.mode']).toEqual({
			value_source: 'environment',
			env_var: 'ENROLLMENT_MODE',
			locked: true,
			default: 'open'
		});
		// a default of false is a default, not a missing one
		expect(sources['two_factor.required']).toEqual({
			value_source: 'default',
			locked: false,
			default: false
		});
		expect('default' in sources['smtp.host']!).toBe(false);
	});

	test('leaves out an entry it cannot read instead of failing the page', async () => {
		answer({
			success: true,
			data: {
				'device_scope.mode': {
					value_source: 'default',
					env_var: 'DEVICE_SCOPE_DEFAULT',
					locked: false,
					default: 'open'
				},
				'future.setting': { value_source: 'somewhere-new', locked: false },
				'broken.entry': 'stored'
			}
		});
		const sources = await getSettingSources();
		expect(Object.keys(sources)).toEqual(['device_scope.mode']);
	});
});
