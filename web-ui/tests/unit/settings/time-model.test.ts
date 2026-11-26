/**
 * Settings › Time as a draft (lib/settings-b/time.ts): the time servers
 * as a list with a message per wrong entry, the skew in range, and what
 * a save sends.
 */
import { describe, expect, test } from 'vitest';
import {
	clockPayload,
	ntpServerProblem,
	splitServers,
	timeProblems
} from '../../../src/lib/settings-b/time.ts';

const ok = {
	zone: 'Europe/Berlin',
	custom: '',
	ntp_servers: ['pool.ntp.org', '192.0.2.1'],
	max_skew_ms: 2000,
	require_synced_clock: true,
	trust_os_ntp: true
};

describe('the time servers', () => {
	test('a stored list becomes rows; the rows go back separated by commas', () => {
		expect(splitServers('pool.ntp.org, time.google.com,,time.cloudflare.com')).toEqual([
			'pool.ntp.org',
			'time.google.com',
			'time.cloudflare.com'
		]);
		expect(clockPayload(ok).ntp_servers).toBe('pool.ntp.org,192.0.2.1');
	});

	test('each entry is a host name or an IP address, as the server checks it', () => {
		for (const good of ['pool.ntp.org', 'ntp1', '192.0.2.1', '2001:db8::1'])
			expect(ntpServerProblem(good), good).toBe('');
		for (const bad of ['ntp.example.com:123', 'https://time.example.com', '-ntp.example.com', 'a b'])
			expect(ntpServerProblem(bad), bad).toBeTruthy();
	});
});

describe('the checks of a save', () => {
	test('at least one server, a skew from 1 to 600000 ms, the custom zone wins', () => {
		expect(timeProblems(ok)).toEqual({});
		expect(timeProblems({ ...ok, ntp_servers: [] }).ntp_servers).toBeTruthy();
		expect(timeProblems({ ...ok, ntp_servers: ['ntp:123'] }).ntp_servers).toBeTruthy();
		expect(timeProblems({ ...ok, max_skew_ms: 0 }).max_skew_ms).toBeTruthy();
		expect(timeProblems({ ...ok, max_skew_ms: null }).max_skew_ms).toBeTruthy();
		expect(clockPayload({ ...ok, custom: ' America/Bogota ' }).billing_timezone).toBe('America/Bogota');
		expect(clockPayload(ok).billing_timezone).toBe('Europe/Berlin');
	});
});
