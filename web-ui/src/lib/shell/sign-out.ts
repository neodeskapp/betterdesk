/**
 * Sign out after asking, from the account menu or the search: this tab
 * ends its remote sessions (the question says how many are open),
 * forgets everything and goes to the sign-in page, and the other tabs
 * leave too.
 */

import { t, tf } from '#lib/i18n/i18n.svelte.ts';
import { consoleSessions } from '#lib/remote/console-sessions.svelte.ts';
import { clearSession } from '#lib/state/session.svelte.ts';
import { confirm } from '#lib/ui/dialogs.svelte.ts';
import { toast } from '#lib/ui/toasts.svelte.ts';
import { logout } from './api.ts';
import { broadcastExpired, stopGuard } from './session-guard.svelte.ts';

export async function signOut(): Promise<void> {
	const label = t('user_menu.sign_out');
	const open = consoleSessions.count;
	const message = open
		? `${t('auth.logout_confirm')} ${tf('remote.sign_out_ends_sessions', 'Your open remote sessions end too ({count}).', { count: open })}`
		: t('auth.logout_confirm');
	const ok = await confirm({ title: label, message, confirmLabel: label });
	if (!ok) return;
	try {
		await logout();
	} catch {
		toast.error(t('errors.logout_failed'));
		return;
	}
	consoleSessions.teardown();
	stopGuard();
	broadcastExpired('logout');
	clearSession();
	location.assign('/login');
}
