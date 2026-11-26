/**
 * The actions of user-actions.ts that run without a dialog of their own:
 * at once, or after a confirmation. The users list and the user's page
 * call runUserAction and open their own sheets and dialogs for the rest.
 * Every message names the person by display name.
 */

import { base } from '#lib/nav.ts';
import {
	invalidateRecoveryCodes,
	requirePasswordChange,
	resendInvite,
	resetTwoFactor,
	revokeInvite,
	setTwoFactorRequired,
	unlockUser,
	updateUser,
	type ConsoleUser
} from '#lib/api/users.ts';
import { openDirect } from '#lib/chat/api.ts';
import { goChat } from '#lib/chat/nav.ts';
import { t } from '#lib/i18n/i18n.svelte.ts';
import { copyText } from '#lib/ui/clipboard.svelte.ts';
import { confirm } from '#lib/ui/dialogs.svelte.ts';
import { toast } from '#lib/ui/toasts.svelte.ts';
import { displayName, ICONS } from '#lib/util/display.ts';
import { signInLink, type UserActionId } from './user-actions.ts';

/** What changed, so the place loads it again */
export type Changed = 'user' | 'invites' | 'gone' | null;

type Person = Pick<ConsoleUser, 'id' | 'username' | 'email'> & {
	display_name?: string;
	first_name?: string;
	last_name?: string;
};

function failed(err: unknown) {
	toast.error(err instanceof Error && err.message ? err.message : t('errors.server_error'));
}

/**
 * Runs an action that needs no sheet or dialog of the place. Answers what
 * changed ('gone' after a withdrawn invite), or undefined for an action
 * the place opens itself (edit, reset password, sessions, organizations,
 * panel access, Sign in as, delete).
 */
export async function runUserAction(id: UserActionId, u: Person): Promise<Changed | undefined> {
	const name = displayName(u) || u.username;
	const named = { username: name };
	switch (id) {
		case 'copy_link': {
			const ok = await copyText(signInLink(location.origin, base, u.username));
			if (ok) toast.success(t('users.sign_in_link_copied', named));
			else toast.error(t('users.sign_in_link_copy_failed'));
			return null;
		}
		case 'message': {
			try {
				const c = await openDirect(Number(u.id));
				goChat({ s: 'team', c: c.id });
			} catch (err) {
				toast.error(err instanceof Error && err.message ? err.message : t('users.message_failed'));
			}
			return null;
		}
		case 'resend_invite':
			try {
				await resendInvite(u.id);
				toast.success(t('users.invite_resent_named', named));
				return 'invites';
			} catch (err) {
				failed(err);
				return null;
			}
		case 'withdraw_invite': {
			const ok = await confirm({
				title: t('users.revoke_invite_title'),
				message: t('users.revoke_invite_confirm', { username: name, email: u.email }),
				confirmLabel: t('users.revoke_invite_action'),
				confirmIcon: ICONS.delete,
				danger: true
			});
			if (!ok) return null;
			try {
				await revokeInvite(u.id);
				toast.success(t('users.invite_revoked_named', named));
				return 'gone';
			} catch (err) {
				failed(err);
				return null;
			}
		}
		case 'disable':
		case 'enable': {
			const disabled = id === 'disable';
			if (disabled) {
				const ok = await confirm({
					title: t('users.disable_title'),
					message: t('users.disable_confirm', named),
					confirmLabel: t('users.disable_menu'),
					confirmIcon: ICONS.disable,
					danger: true
				});
				if (!ok) return null;
			}
			try {
				await updateUser(u.id, { disabled });
				toast.success(t(disabled ? 'users.disabled_named' : 'users.enabled_named', named));
				return 'user';
			} catch (err) {
				failed(err);
				return null;
			}
		}
		case 'require_change':
		case 'require_change_off': {
			const required = id === 'require_change';
			try {
				const [result] = await requirePasswordChange([u.id], required);
				if (result?.outcome === 'refused') {
					toast.error(result.error || t('errors.server_error'));
					return null;
				}
				toast.success(t(required ? 'users.require_change_done' : 'users.require_change_off_done', named));
				return 'user';
			} catch (err) {
				failed(err);
				return null;
			}
		}
		case 'unlock':
			try {
				await unlockUser(u.id);
				toast.success(t('users.unlocked_named', named));
				return 'user';
			} catch (err) {
				failed(err);
				return null;
			}
		case 'require_2fa':
		case 'require_2fa_off': {
			const required = id === 'require_2fa';
			try {
				await setTwoFactorRequired(u.id, required);
				toast.success(t(required ? 'users.require_2fa_done' : 'users.require_2fa_off_done', named));
				return 'user';
			} catch (err) {
				failed(err);
				return null;
			}
		}
		case 'reset_2fa': {
			const ok = await confirm({
				title: t('users.reset_2fa_title'),
				message: t('users.reset_2fa_confirm', named),
				confirmLabel: t('users.reset_2fa_button'),
				danger: true
			});
			if (!ok) return null;
			try {
				await resetTwoFactor(u.id);
				toast.success(t('users.reset_2fa_done', named));
				return 'user';
			} catch (err) {
				failed(err);
				return null;
			}
		}
		case 'totp_off': {
			const ok = await confirm({
				title: t('users.totp_off_title'),
				message: t('users.totp_off_confirm', named),
				confirmLabel: t('users.totp_off_menu'),
				danger: true
			});
			if (!ok) return null;
			try {
				await updateUser(u.id, { totp_enabled: false });
				toast.success(t('users.totp_off_named', named));
				return 'user';
			} catch (err) {
				failed(err);
				return null;
			}
		}
		case 'invalidate_codes': {
			const ok = await confirm({
				title: t('users.invalidate_codes_title', named),
				message: t('users.invalidate_codes_confirm', named),
				confirmLabel: t('users.invalidate_codes_button'),
				danger: true
			});
			if (!ok) return null;
			try {
				await invalidateRecoveryCodes(u.id);
				toast.success(t('users.codes_invalidated_named', named));
				return null;
			} catch (err) {
				failed(err);
				return null;
			}
		}
		default:
			return undefined;
	}
}
