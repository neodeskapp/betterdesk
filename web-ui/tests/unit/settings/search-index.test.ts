/**
 * The settings in the search of the top bar (Ctrl K): every setting of the
 * one settings list (search-index.ts) is found, the account's permissions
 * filter it, and German and English find the same setting.
 */
import fs from 'node:fs';
import path from 'node:path';
import { describe, expect, test } from 'vitest';
import { SEARCH_INDEX } from '../../../src/lib/settings/search-index.ts';
import { TAB_TERMS, TERMS } from '../../../src/lib/settings/search-terms.ts';
import { settingEntries } from '../../../src/lib/settings/search.ts';
import { TABS, tabAllowed, isPlace, placeAllowed } from '../../../src/lib/settings/tabs.ts';
import { searchGrouped, MAX_SETTINGS, type SearchSetting } from '../../../src/lib/shell/search.ts';

const langDir = path.resolve(__dirname, '../../../../neodesk-server/console/static/files/lang');
type Strings = { [k: string]: string | Strings };
const load = (code: string): Strings =>
	JSON.parse(fs.readFileSync(path.join(langDir, `${code}.json`), 'utf8'));
const lookup = (s: Strings, key: string): string | undefined => {
	let cur: string | Strings | undefined = s;
	for (const p of key.split('.')) cur = typeof cur === 'object' ? cur[p] : undefined;
	return typeof cur === 'string' ? cur : undefined;
};
const en = load('en');
const de = load('de');
const tOf = (s: Strings) => (key: string) => lookup(s, key) ?? lookup(en, key) ?? key;

const everyone = () => true;
const viewer = (p: string) => p === 'device.view';

const find = (query: string, settings: SearchSetting[]) =>
	searchGrouped(query, { pages: [], settings }).results.filter((r) => r.kind === 'setting');

describe('the settings list', () => {
	test('every label, section and hint key has an English and a German text', () => {
		const keys = new Set(SEARCH_INDEX.flatMap(([, , label, section, hints]) => [label, section, ...hints]));
		keys.delete('');
		const missing = [...keys].filter((k) => !lookup(en, k) || !lookup(de, k));
		expect(missing).toEqual([]);
	});

	test('other words belong to a tab or a setting of the list', () => {
		const ids = new Set(SEARCH_INDEX.map((r) => r[1]));
		expect(Object.keys(TERMS).filter((id) => !ids.has(id))).toEqual([]);
		expect(Object.keys(TAB_TERMS).filter((tab) => !isPlace(tab))).toEqual([]);
	});
});

describe('settingEntries', () => {
	test('a super admin gets every setting of the list, once', () => {
		const t = tOf(en);
		const entries = settingEntries({ t, can: everyone, admin: true, superAdmin: true });
		const expected = new Set(
			SEARCH_INDEX.filter((r) => isPlace(r[0])).map(
				(r) => `${r[0]}#${r[1]}#${t(r[2]).replace(/:$/, '').trim()}`
			)
		);
		expect(entries.length).toBe(expected.size);
		for (const e of entries) expect(expected.has(`${e.place}#${e.id}#${e.label}`)).toBe(true);
	});

	test('each setting says where it lives and links to its element on its tab', () => {
		const entries = settingEntries({ t: tOf(en), can: everyone });
		const host = entries.find((e) => e.id === 'ldap-host');
		expect(host).toMatchObject({
			label: 'Host',
			where: 'Settings, Directory (LDAP)',
			href: '/settings?tab=ldap#ldap-host'
		});
		const name = entries.find((e) => e.id === 'brand-name');
		expect(name?.where).toBe('Settings, Branding, Name and logo');
		expect(name?.href).toBe('/settings?tab=brand#brand-name');
		// General has no ?tab= in its address
		expect(entries.find((e) => e.id === 'public-server-id')?.href).toBe('/settings#public-server-id');
		// The server's own settings are on the pages of the Server section
		const scheduled = entries.find((e) => e.id === 'scheduled-backup-enabled');
		expect(scheduled?.where).toBe('Server, Backups, Scheduled backups');
		expect(scheduled?.href).toBe('/server/backups#scheduled-backup-enabled');
		expect(entries.find((e) => e.id === 'backup-retention-input')).toMatchObject({
			label: 'Keep update backups',
			href: '/server/backups#backup-retention-input'
		});
		expect(entries.find((e) => e.id === 'update-channel-panel')?.href).toBe(
			'/server/updates#update-channel-panel'
		);
	});

	test('your password and two-factor sign-in are on Your account, for every account', () => {
		for (const can of [everyone, viewer]) {
			const entries = settingEntries({ t: tOf(en), can });
			expect(entries.find((e) => e.id === 'new-password')).toMatchObject({
				place: 'account',
				where: 'Your account, Password',
				href: '/account#new-password'
			});
			expect(entries.find((e) => e.id === 'two-factor')).toMatchObject({
				place: 'account',
				label: 'Two-factor sign-in',
				where: 'Your account',
				href: '/account#two-factor'
			});
		}
		// Settings › Sign-in keeps what applies to everyone
		expect(
			settingEntries({ t: tOf(en), can: everyone }).find((e) => e.id === 'require-two-factor')?.href
		).toBe('/settings?tab=auth#require-two-factor');
		// The security notice "Password changed" is a mail rule for everyone, not your own password
		expect(
			SEARCH_INDEX.filter(
				(r) => r[0] === 'auth' && !r[1].startsWith('security-notice-') && /password|totp/.test(r[1])
			)
		).toEqual([]);
	});

	test('no setting the account cannot see: tabs it may not open and rows that need a permission', () => {
		const entries = settingEntries({ t: tOf(en), can: viewer });
		expect(entries.length).toBeGreaterThan(0);
		for (const e of entries) {
			expect(placeAllowed(e.place, false)).toBe(true);
			const row = SEARCH_INDEX.find((r) => r[0] === e.place && r[1] === e.id);
			expect(row?.[5]).toBe('');
		}
		const ids = entries.map((e) => e.id);
		expect(ids).toContain('new-password');
		expect(ids).not.toContain('ldap-host');
		expect(ids).not.toContain('email-smtp-host');
		expect(ids).not.toContain('branding-profiles');
		// The client app's branding needs branding.edit
		expect(ids).not.toContain('client-app-company');
		// Each tab of the list that a viewer may open still has its settings
		const tabs = new Set<string>(entries.map((e) => e.place));
		for (const tab of TABS.filter((x) => tabAllowed(x, false)))
			if (SEARCH_INDEX.some((r) => r[0] === tab && r[5] === '')) expect(tabs.has(tab)).toBe(true);
	});

	test('policy settings are found by the admin roles only, on the Policies page (task 5: "sound")', () => {
		const t = tOf(en);
		const admin = settingEntries({ t, can: viewer, admin: true });
		const sound = find('sound', admin);
		expect(sound.map((r) => r.href)).toContain('/policies#pol-features-allow-audio');
		const audio = admin.find((e) => e.id === 'pol-features-allow-audio');
		expect(audio).toMatchObject({ label: 'Allow sound from the device', where: 'Rules, Policies, Features' });
		expect(find('audio', admin).length).toBeGreaterThan(0);
		expect(find('ton', settingEntries({ t: tOf(de), can: viewer, admin: true })).length).toBeGreaterThan(0);
		// Not for an operator, even with every permission
		expect(settingEntries({ t, can: everyone }).some((e) => e.place === 'policies')).toBe(false);
		expect(placeAllowed('policies', true)).toBe(false);
		expect(placeAllowed('policies', false, { admin: true })).toBe(true);
		expect(placeAllowed('permissions', false, { admin: true })).toBe(false);
		expect(placeAllowed('permissions', false, { superAdmin: true })).toBe(true);
	});

	test('a search for a setting it may not see finds nothing', () => {
		expect(find('ldap', settingEntries({ t: tOf(en), can: viewer }))).toEqual([]);
		expect(find('ldap', settingEntries({ t: tOf(en), can: everyone })).length).toBeGreaterThan(0);
	});
});

describe('English and German find the same setting', () => {
	const inEn = settingEntries({ t: tOf(en), can: everyone });
	const inDe = settingEntries({ t: tOf(de), can: everyone });
	const first = (q: string, list: SearchSetting[]) => find(q, list)[0]?.id;

	test.each([
		['time zone', 'zeitzone', 'time-billing-timezone'],
		['new password', 'neues passwort', 'new-password'],
		['require two-factor', 'zwei-faktor verlangen', 'require-two-factor'],
		['smtp', 'mailserver', 'email-smtp-host'],
		['application name', 'anwendungsname', 'brand-name'],
		['update channel', 'update-kanal', 'update-channel-panel'],
		['scheduled backups', 'geplante sicherungen', 'scheduled-backup-panel'],
		['relay server', 'relay-server', 'public-relay-server'],
		['backup every night', 'sicherung nachts', 'scheduled-backup-time']
	])('"%s" in English and "%s" in German', (qEn, qDe, id) => {
		expect(first(qEn, inEn)).toBe(id);
		expect(first(qDe, inDe)).toBe(id);
	});

	test('the other language works too, whatever the console shows', () => {
		expect(find('zeitzone', inEn).map((r) => r.id)).toContain('time-billing-timezone');
		expect(find('timezone', inDe).map((r) => r.id)).toContain('time-billing-timezone');
		expect(find('2fa', inDe).map((r) => r.id)).toContain('two-factor');
		expect(find('dunkel', inEn).map((r) => r.id)).toContain('theme-mode-selector');
	});

	test('accents do not matter: "andern" finds "ändern"', () => {
		expect(find('passwort andern', inDe).map((r) => r.id)).toContain('new-password');
	});

	test('the list shows at most eight settings and counts them all', () => {
		const { results, totals } = searchGrouped('ldap', { pages: [], settings: inEn });
		expect(results.filter((r) => r.kind === 'setting')).toHaveLength(MAX_SETTINGS);
		expect(totals.setting).toBeGreaterThan(MAX_SETTINGS);
	});
});

describe('permissions on Roles and permissions', () => {
	const supers = settingEntries({ t: tOf(en), can: everyone, superAdmin: true });

	test('"delete devices" finds the permission and opens its row, for super admins only', () => {
		expect(find('delete devices', supers).map((r) => r.id)).toContain('perm-device-delete');
		expect(supers.find((e) => e.id === 'perm-device-delete')).toMatchObject({
			place: 'permissions',
			label: 'Delete devices',
			where: 'People, Roles and permissions, See and manage devices',
			href: '/permissions#perm-device-delete'
		});
		// Every other account: the page is for super admins
		expect(settingEntries({ t: tOf(en), can: everyone }).some((e) => e.place === 'permissions')).toBe(false);
	});

	test('found by its sentence, its key and in German', () => {
		expect(find('api keys', supers).map((r) => r.id)).toContain('perm-server-keys');
		expect(find('device.delete', supers).map((r) => r.id)).toContain('perm-device-delete');
		const inDe = settingEntries({ t: tOf(de), can: everyone, superAdmin: true });
		expect(find('geräte löschen', inDe).map((r) => r.id)).toContain('perm-device-delete');
	});
});
