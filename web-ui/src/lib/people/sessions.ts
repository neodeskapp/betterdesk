/**
 * Signed-in devices without a DOM: a session in words and the limit the
 * list states.
 */

import type { SessionLimit } from '#lib/api/sessions.ts';

type T = (key: string, vars?: Record<string, string | number>) => string;

/** "Firefox on Windows", from what the server read of the user agent */
export function deviceLabel(d: { browser?: string; system?: string }, t: T): string {
	if (d.browser && d.system) return t('sessions.browser_on_system', { browser: d.browser, system: d.system });
	if (d.browser) return d.browser;
	if (d.system) return t('sessions.browser_on', { system: d.system });
	return t('sessions.unknown_browser');
}

/** What the server's limit means for the list's reader */
export function limitText(limit: Pick<SessionLimit, 'max' | 'source'>, t: T): string {
	if (limit.max === 1) return t('sessions.limit_one');
	if (limit.max <= 0) return t('sessions.limit_none');
	return t('sessions.limit_many', { max: limit.max });
}
