/**
 * Things to do and recent searches in the search of the top bar.
 */
import { describe, expect, test } from 'vitest';
import { searchActions, type ActionContext } from '../../../src/lib/shell/actions.ts';
import {
	addRecent,
	readRecent,
	recentKey,
	searchGrouped,
	writeRecent,
	RECENT_MAX
} from '../../../src/lib/shell/search.ts';

const t = (k: string) => k;
const ctx = (perms: string[], extra: Partial<ActionContext> = {}): ActionContext => ({
	can: (p) => perms.includes(p),
	mayMakeTokens: false,
	dark: false,
	t,
	...extra
});

describe('searchActions', () => {
	test('offers only what the account may do', () => {
		const none = searchActions(ctx([])).map((a) => a.id);
		expect(none).toEqual(['accessibility', 'theme', 'sign-out']);
		const all = searchActions(
			ctx(['device.connect', 'enrollment.approve', 'enrollment.manage', 'user.create'], {
				mayMakeTokens: true
			})
		).map((a) => a.id);
		expect(all).toEqual([
			'quick-support',
			'add-computer',
			'new-device-token',
			'add-user',
			'new-api-token',
			'accessibility',
			'theme',
			'sign-out'
		]);
	});

	test('the theme action names the theme it switches to', () => {
		expect(searchActions(ctx([])).find((a) => a.id === 'theme')?.title).toBe('search.action_theme_dark');
		expect(searchActions(ctx([], { dark: true })).find((a) => a.id === 'theme')?.title).toBe(
			'search.action_theme_light'
		);
	});

	test('found by other words in English and German', () => {
		const actions = searchActions(ctx(['enrollment.approve', 'device.connect']));
		const ids = (q: string) =>
			searchGrouped(q, { pages: [], actions })
				.results.filter((r) => r.kind === 'action')
				.map((r) => r.id);
		expect(ids('invite')).toEqual(['add-computer']);
		expect(ids('einladen')).toEqual(['add-computer']);
		expect(ids('abmelden')).toEqual(['sign-out']);
		expect(ids('log out')).toEqual(['sign-out']);
		expect(ids('fernwartung')).toEqual(['quick-support']);
	});

	test('the chat settings for the server: for chat users who may change the server, found by chat with computers and the mail', () => {
		expect(searchActions(ctx(['chat.support'])).map((a) => a.id)).not.toContain('chat-settings');
		expect(searchActions(ctx(['server.config'])).map((a) => a.id)).not.toContain('chat-settings');
		const actions = searchActions(ctx(['server.config', 'chat.team']));
		expect(actions.find((a) => a.id === 'chat-settings')?.title).toBe('chat.server_settings');
		const ids = (q: string) =>
			searchGrouped(q, { pages: [], actions })
				.results.filter((r) => r.kind === 'action')
				.map((r) => r.id);
		expect(ids('chat with computers')).toEqual(['chat-settings']);
		expect(ids('chat mail')).toEqual(['chat-settings']);
		expect(ids('chat mit computern')).toEqual(['chat-settings']);
	});
});

describe('recent searches', () => {
	test('newest first, no repeats in any case, at most six', () => {
		let list: string[] = [];
		for (const q of ['one', 'two', 'One ', 'three', 'four', 'five', 'six', 'seven'])
			list = addRecent(list, q);
		expect(list).toHaveLength(RECENT_MAX);
		expect(list[0]).toBe('seven');
		expect(list.filter((x) => x.toLowerCase() === 'one')).toHaveLength(1);
		expect(addRecent(list, '   ')).toEqual(list);
	});

	test('kept per account; anything unreadable reads as empty', () => {
		const store = new Map<string, string>();
		const storage = {
			getItem: (k: string) => store.get(k) ?? null,
			setItem: (k: string, v: string) => void store.set(k, v),
			removeItem: (k: string) => void store.delete(k)
		};
		writeRecent(storage, recentKey(1), ['ldap']);
		expect(readRecent(storage, recentKey(1))).toEqual(['ldap']);
		expect(readRecent(storage, recentKey(2))).toEqual([]);
		store.set(recentKey(3), '{bad');
		expect(readRecent(storage, recentKey(3))).toEqual([]);
		writeRecent(storage, recentKey(1), []);
		expect(store.has(recentKey(1))).toBe(false);
	});
});
