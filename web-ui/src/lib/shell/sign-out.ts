/**
 * Sign out after asking, from the account menu or the search: this tab
 * forgets everything and goes to the sign-in page, and the other tabs
 * leave too.
 */

import { t } from '#lib/i18n/i18n.svelte.ts';
import { clearSession } from '#lib/state/session.svelte.ts';
import { confirm } from '#lib/ui/dialogs.svelte.ts';
import { toast } from '#lib/ui/toasts.svelte.ts';
import { logout } from './api.ts';
import { broadcastExpired, stopGuard } from './session-guard.svelte.ts';

export async function signOut(): Promise<void> {
	const label = t('user_menu.sign_out');
	const ok = await confirm({ title: label, message: t('auth.logout_confirm'), confirmLabel: label });
	if (!ok) return;
	try {
		await logout();
	} catch {
		toast.error(t('errors.logout_failed'));
		return;
	}
	stopGuard();
	broadcastExpired('logout');
	clearSession();
	location.assign('/login');
}
