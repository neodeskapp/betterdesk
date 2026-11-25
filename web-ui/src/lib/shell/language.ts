/**
 * Changing the console language of the signed-in account: saved on the
 * account (POST /api/i18n/set), then the strings, the direction and the
 * navigation switch without a reload. The language menu, the account
 * menu's Preferences and Your account › Preferences use it.
 */

import { getBootstrap, saveLanguage } from '#lib/api/console.ts';
import { i18n, switchLanguage, t } from '#lib/i18n/i18n.svelte.ts';
import { applyBootstrap } from '#lib/state/session.svelte.ts';
import { toast } from '#lib/ui/toasts.svelte.ts';

let busy = false;

export async function chooseLanguage(code: string): Promise<void> {
	if (busy || !code || code === i18n.lang) return;
	busy = true;
	try {
		await switchLanguage(code, saveLanguage);
		// Page names in the navigation come translated from the server
		applyBootstrap(await getBootstrap());
	} catch {
		toast.error(t('errors.language_change_failed'));
	} finally {
		busy = false;
	}
}

/** The language shown now, in its own name */
export function currentLanguageName(): string {
	const l = i18n.available.find((x) => x.code === i18n.lang);
	return l?.native || l?.name || i18n.lang;
}
