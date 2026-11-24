/**
 * API tokens without a DOM: who may make them, the expiry date, the allow
 * list as typed and the filter.
 */
import fs from 'node:fs';
import path from 'node:path';
import { beforeAll, describe, expect, test } from 'vitest';
import type { ApiToken } from '../../../src/lib/api/apiTokens.ts';
import { setStrings, t } from '../../../src/lib/i18n/i18n.svelte.ts';
import {
	badAllowEntries,
	expiryDate,
	filterApiTokens,
	latestExpiry,
	loginCommand,
	madeList,
	mayMakeTokens,
	parseAllowList,
	revokeMessage,
	stateTone
} from '../../../src/lib/people/apiTokens.ts';

const langDir = path.resolve(__dirname, '../../../../neodesk-server/console/static/files/lang');
const strings = (code: string) => JSON.parse(fs.readFileSync(path.join(langDir, `${code}.json`), 'utf8'));
const none = {
	guest_links: 0,
	device_tokens: 0,
	support_sessions: 0,
	invites: 0,
	pairing_codes: 0,
	share_links: 0,
	organization_invitations: 0
};

const token = (over: Partial<ApiToken>): ApiToken => ({
	id: 'abcdefghijkm',
	name: 'Backup',
	hint: 'ndt_abcdefghijkm_…',
	user_id: 2,
	username: 'ann',
	permissions: ['device.view'],
	read_only: true,
	allow_list: [],
	state: 'active',
	created_at: '2026-11-23T10:00:00Z',
	expires_at: '2026-12-23T10:00:00Z',
	last_used_at: '',
	last_used_ip: '',
	revoked_at: '',
	revoked_by: '',
	ends_with_it: none,
	...over
});

describe('what ends with a token', () => {
	beforeAll(() => setStrings('en', 'ltr', strings('en')));

	test('the kinds it made that are still open, by count', () => {
		expect(madeList({ ...none, guest_links: 2, device_tokens: 1, organization_invitations: 1 }, t)).toBe(
			'2 guest links, 1 device token, 1 organization invitation'
		);
		expect(madeList({ ...none, support_sessions: 1, invites: 3, pairing_codes: 1, share_links: 2 }, t)).toBe(
			'1 support session, 3 invites, 1 pairing code, 2 guest desktop links'
		);
		expect(madeList(none, t)).toBe('');
	});

	test('the revoke dialog says what ends with the token, and only when something does', () => {
		expect(revokeMessage(token({ ends_with_it: { ...none, guest_links: 1, device_tokens: 2 } }), t)).toBe(
			"Revoke Backup? Scripts that use it stop working right away. This can't be undone. " +
				'It also ends what was made with it: 1 guest link, 2 device tokens.'
		);
		expect(revokeMessage(token({}), t)).toBe(
			"Revoke Backup? Scripts that use it stop working right away. This can't be undone."
		);
	});

	test('in German', () => {
		setStrings('de', 'ltr', strings('de'));
		expect(madeList({ ...none, guest_links: 1, invites: 2 }, t)).toBe('1 Gastlink, 2 Einladungen');
		setStrings('en', 'ltr', strings('en'));
	});
});

describe('who makes tokens', () => {
	test('the four admin roles, nobody else', () => {
		for (const role of ['super_admin', 'admin', 'global_admin', 'server_admin'])
			expect(mayMakeTokens(role)).toBe(true);
		for (const role of ['operator', 'viewer', 'guest_issuer', 'pro', '', null, undefined])
			expect(mayMakeTokens(role)).toBe(false);
	});
});

describe('expiry', () => {
	test('a date some days from now, in UTC', () => {
		const now = new Date('2026-11-23T23:30:00Z');
		expect(expiryDate(30, now)).toBe('2026-12-23');
		expect(expiryDate(7, now)).toBe('2026-11-30');
		expect(latestExpiry(now)).toBe('2027-11-23');
	});
});

describe('allow list', () => {
	test('entries by line, comma or space', () => {
		expect(parseAllowList(' 192.0.2.10\n198.51.100.0/24, 2001:db8::/32  ;10.0.0.1 ')).toEqual([
			'192.0.2.10',
			'198.51.100.0/24',
			'2001:db8::/32',
			'10.0.0.1'
		]);
		expect(parseAllowList('   ')).toEqual([]);
	});
	test('what the server will refuse', () => {
		expect(badAllowEntries(['192.0.2.10', '198.51.100.0/24', '2001:db8::/32', '::1'])).toEqual([]);
		expect(
			badAllowEntries(['192.0.2.300', '10.0.0.0/33', 'host.example', '1.2.3.4/8/9', '2001:db8::/129'])
		).toEqual(['192.0.2.300', '10.0.0.0/33', 'host.example', '1.2.3.4/8/9', '2001:db8::/129']);
	});
});

describe('list', () => {
	const list = [
		token({ id: 'a', name: 'Backup' }),
		token({ id: 'b', name: 'Monitoring', state: 'revoked', permissions: ['audit.view'] }),
		token({ id: 'c', name: 'Old', state: 'expired', username: 'bob' })
	];
	test('active only, or all', () => {
		expect(filterApiTokens(list, 'active', '').map((t) => t.id)).toEqual(['a']);
		expect(filterApiTokens(list, 'all', '').map((t) => t.id)).toEqual(['a', 'b', 'c']);
	});
	test('search by name, account and permission', () => {
		expect(filterApiTokens(list, 'all', 'monitor').map((t) => t.id)).toEqual(['b']);
		expect(filterApiTokens(list, 'all', 'BOB').map((t) => t.id)).toEqual(['c']);
		expect(filterApiTokens(list, 'all', 'audit.view').map((t) => t.id)).toEqual(['b']);
	});
	test('only active tokens look live', () => {
		expect(stateTone('active')).toBe('good');
		expect(stateTone('revoked')).toBe('neutral');
		expect(stateTone('expired')).toBe('neutral');
	});
});

test('the sign-in command names the console and never a token', () => {
	expect(loginCommand('https://desk.example.com')).toBe('neodesk login --url https://desk.example.com');
});
