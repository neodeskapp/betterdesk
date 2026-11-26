/**
 * The actions on one account, grouped the way the users row menu and the
 * user's page show them (design rules: account, access, sign-in security,
 * and the ones that end something apart at the end). One list of rules
 * for both places, without a DOM: which action an account offers follows
 * the signed-in person's permissions and the server's rules (mayChange,
 * never your own account for disable, delete and the security actions,
 * local accounts only for password rules).
 *
 * A label with dots ("Reset password…") opens a dialog; the others act at
 * once or after a confirmation.
 */

import type { ConsoleUser } from '#lib/api/users.ts';
import { ICONS } from '#lib/util/display.ts';
import { mayChangeUser, mayDisableUser, providerOf } from './users.ts';

export type UserActionId =
	| 'open'
	| 'edit'
	| 'resend_invite'
	| 'enable'
	| 'copy_link'
	| 'message'
	| 'orgs'
	| 'access'
	| 'sign_in_as'
	| 'reset_password'
	| 'require_change'
	| 'require_change_off'
	| 'unlock'
	| 'require_2fa'
	| 'require_2fa_off'
	| 'reset_2fa'
	| 'totp_off'
	| 'invalidate_codes'
	| 'sessions'
	| 'withdraw_invite'
	| 'disable'
	| 'delete';

export interface UserAction {
	id: UserActionId;
	/** The menu item's class, which tests and the e2e suite find it by */
	cls: string;
	icon: string;
	/** A language key, or [key, fallback] for tf */
	label: string | [string, string];
	danger?: boolean;
}

export type UserActionGroupId = 'account' | 'access' | 'security' | 'danger';

export interface UserActionGroup {
	id: UserActionGroupId;
	cls: string;
	label: string;
	actions: UserAction[];
}

/** What the signed-in person may do, as the users page reads it from the session */
export interface ActionRights {
	meId: string;
	myRole: string | null | undefined;
	canEdit: boolean;
	canDelete: boolean;
	canSecurity: boolean;
	/** Withdrawing an invite undoes Add user (user.create) */
	canWithdraw: boolean;
	/** Sign in as (user.impersonate, not while viewing as someone) */
	canViewAs: boolean;
	/** The person may use Team chat (chat.team) */
	canTeamChat: boolean;
}

/** What the place knows about the account besides the list's fields */
export interface ActionFacts {
	/** The console can send mail */
	mailReady: boolean;
	/** An invite waits (open or expired) */
	invited: boolean;
	/** The account can use Team chat; the list doesn't know, the page does (undefined: not offered) */
	teamChat?: boolean;
	/** The row menu leads to the user's page */
	withOpen?: boolean;
}

const A = (
	id: UserActionId,
	cls: string,
	icon: string,
	label: UserAction['label'],
	danger = false
): UserAction => ({ id, cls, icon, label, ...(danger ? { danger } : {}) });

/** The account's actions by group; groups without an action are left out */
export function userActionGroups(
	u: Pick<
		ConsoleUser,
		| 'id'
		| 'role'
		| 'email'
		| 'auth_provider'
		| 'last_login'
		| 'disabled'
		| 'locked'
		| 'must_change_password'
		| 'two_factor_required'
		| 'two_factor_state'
		| 'two_factor_due'
		| 'totp_enabled'
	>,
	r: ActionRights,
	f: ActionFacts
): UserActionGroup[] {
	const self = String(u.id) === r.meId;
	const local = providerOf(u) === 'local';
	const changeable = mayChangeUser(r.myRole, r.meId, u);
	const edit = r.canEdit && changeable;
	const disableable = r.canEdit && mayDisableUser(r.myRole, r.meId, u);
	const deletable = r.canDelete && changeable && !self;
	const resend = edit && local && f.mailReady && !!u.email && !u.last_login;
	const secure = r.canSecurity && changeable && !self;
	const totpOn = u.two_factor_state === 'on' || !!u.totp_enabled;
	const withdraw = r.canWithdraw && changeable && !self && !u.last_login && f.invited;

	const account: UserAction[] = [];
	if (f.withOpen) account.push(A('open', 'act-open', ICONS.user, 'users.open_page'));
	if (edit) account.push(A('edit', 'act-edit', ICONS.edit, 'users.edit_menu'));
	if (resend) account.push(A('resend_invite', 'act-resend-invite', ICONS.send, 'users.resend_invite'));
	if (disableable && u.disabled) account.push(A('enable', 'act-disable', ICONS.enable, 'users.enable_menu'));
	if (r.canTeamChat && f.teamChat && !self && !u.disabled)
		account.push(A('message', 'act-message', 'chat', 'users.send_message'));
	account.push(A('copy_link', 'act-copy-link', ICONS.link, 'users.copy_sign_in_link'));

	const access: UserAction[] = [
		A('orgs', 'act-orgs', ICONS.organization, 'users.organizations_menu'),
		A('access', 'act-access', ICONS.access_policy, ['users.access_menu', 'Panel access…'])
	];
	if (r.canViewAs && !self && !u.disabled)
		access.push(A('sign_in_as', 'act-sign-in-as', ICONS.view, 'users.sign_in_as_menu'));

	const security: UserAction[] = [];
	if (edit) security.push(A('reset_password', 'act-reset', ICONS.password, 'users.reset_password_menu'));
	if (secure && local)
		security.push(
			u.must_change_password
				? A('require_change_off', 'act-require-change-off', ICONS.password, 'users.require_change_off_menu')
				: A('require_change', 'act-require-change', ICONS.password, 'users.require_change_menu')
		);
	if (r.canSecurity && changeable && u.locked)
		security.push(A('unlock', 'act-unlock', ICONS.enable, 'users.unlock_menu'));
	if (secure && local)
		security.push(
			u.two_factor_required
				? A('require_2fa_off', 'act-require-2fa-off', ICONS.password, 'users.require_2fa_off_menu')
				: A('require_2fa', 'act-require-2fa', ICONS.password, 'users.require_2fa_menu')
		);
	if (secure && totpOn) {
		security.push(
			u.two_factor_due
				? A('reset_2fa', 'act-totp-reset', ICONS.password, 'users.reset_2fa_menu')
				: A('totp_off', 'act-totp-off', ICONS.password, 'users.totp_off_menu_ask')
		);
		security.push(
			A('invalidate_codes', 'act-codes-invalidate', ICONS.password, 'users.invalidate_codes_menu')
		);
	}
	if (edit) security.push(A('sessions', 'act-sessions', 'devices', 'sessions.user_menu'));

	const danger: UserAction[] = [];
	if (withdraw)
		danger.push(A('withdraw_invite', 'act-withdraw-invite', ICONS.delete, 'users.revoke_invite_menu', true));
	if (disableable && !u.disabled)
		danger.push(A('disable', 'act-disable', ICONS.disable, 'users.disable_menu_ask'));
	if (deletable) danger.push(A('delete', 'act-delete', ICONS.delete, 'users.delete_menu', true));

	const groups: UserActionGroup[] = [
		{ id: 'account', cls: 'group-account', label: 'users.menu_group_account', actions: account },
		{ id: 'access', cls: 'group-access', label: 'users.menu_group_access', actions: access },
		{ id: 'security', cls: 'group-security', label: 'users.menu_group_security', actions: security },
		{ id: 'danger', cls: 'group-danger', label: 'users.menu_group_danger', actions: danger }
	];
	return groups.filter((g) => g.actions.length);
}

/** The text of an action's label */
export function actionLabel(
	a: UserAction,
	t: (key: string) => string,
	tf: (key: string, fallback: string) => string
): string {
	return Array.isArray(a.label) ? tf(a.label[0], a.label[1]) : t(a.label);
}

/**
 * The address that signs the person in: the console's sign-in page with
 * the username filled in. origin is the console's address (location.origin),
 * base the app's base path.
 */
export function signInLink(origin: string, base: string, username: string): string {
	const root = `${origin.replace(/\/+$/, '')}${base.replace(/\/+$/, '')}`;
	return `${root}/login?username=${encodeURIComponent(username)}`;
}
