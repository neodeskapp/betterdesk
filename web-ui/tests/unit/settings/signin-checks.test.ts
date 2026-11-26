/**
 * Settings › Directory (LDAP) refuses numbers it can't use instead of
 * replacing them (L418): port and timeout go to the server as typed. The
 * client session days are checked on their field (signin-tab.test.ts).
 */
import { expect, test } from 'vitest';
import { ldapFromServer, ldapPayload } from '../../../src/lib/settings/signin.ts';

test('LDAP numbers go to the server as typed, an empty one as no value', () => {
	const base = ldapFromServer({ enabled: true, host: 'ldap.example.org' });
	expect(ldapPayload({ ...base, port: 2.6, conn_timeout_sec: 0 })).toMatchObject({
		port: 2.6,
		conn_timeout_sec: 0
	});
	expect(ldapPayload({ ...base, port: 99999 })).toMatchObject({ port: 99999 });
	expect(ldapPayload({ ...base, port: null, conn_timeout_sec: null })).toMatchObject({
		port: null,
		conn_timeout_sec: null
	});
});
