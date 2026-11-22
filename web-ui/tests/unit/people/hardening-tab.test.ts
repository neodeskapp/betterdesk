/**
 * The Hardening tab shows what this server's settings decide, each On or
 * Off, and asks for no dependency scan, which no scan stood behind
 * (BUGS L104).
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import HardeningTab from '../../../src/routes/(console)/security-audit/HardeningTab.svelte';

let requests: string[] = [];

beforeEach(() => {
	requests = [];
	vi.stubGlobal('fetch', async (input: string) => {
		const url = new URL(input, location.origin);
		requests.push(url.pathname);
		const data =
			url.pathname === '/api/panel/security-audit/hardening'
				? {
						priorities: [
							{
								id: 'session_timeout',
								name: 'Session timeout',
								severity: 'medium',
								category: 'auth',
								implemented: true,
								description: 'Ends after 30 minutes'
							},
							{
								id: 'single_session',
								name: 'Session limit',
								severity: 'high',
								category: 'auth',
								implemented: false,
								description: 'currently unlimited'
							}
						]
					}
				: { success: false };
		return {
			ok: true,
			status: 200,
			redirected: false,
			url: url.href,
			headers: new Headers({ 'content-type': 'application/json' }),
			json: async () => data,
			text: async () => JSON.stringify(data)
		} as unknown as Response;
	});
});

afterEach(() => {
	cleanup();
	vi.unstubAllGlobals();
});

describe('Hardening tab', () => {
	test("this server's settings, On or Off, and no dependency scan", async () => {
		render(HardeningTab);
		expect(await screen.findByText('Session timeout')).toBeTruthy();
		expect(screen.getByText('Session limit')).toBeTruthy();
		expect(screen.getByText('On')).toBeTruthy();
		expect(screen.getByText('Off')).toBeTruthy();
		expect(screen.queryByText('security_audit.done')).toBeNull();
		expect(screen.queryByText('security_audit.vuln_title')).toBeNull();
		expect(requests).toEqual(['/api/panel/security-audit/hardening']);
	});
});
