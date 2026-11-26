/**
 * The user's page (/users/<id>) without a DOM: what the role allows in
 * plain sentences, grouped like the permissions page; how the account
 * reaches devices; and what its sign-in looks like now.
 */

import type { EffectiveScope, UserDetail, UserOrg } from '#lib/api/users.ts';
import { CATEGORIES, categoryLabel, permLabel } from './permissions.ts';
import { providerOf } from './users.ts';

type T = (key: string, params?: Record<string, string | number>) => string;

/** A permission as a sentence: "Can connect to devices"; the permissions page's label where there is none */
export function permissionSentence(perm: string, t: T): string {
	const key = `users.can_${perm.replace(/\./g, '_')}`;
	const text = t(key);
	return text === key ? permLabel(perm, t) : text;
}

export interface SentenceGroup {
	id: string;
	label: string;
	sentences: string[];
}

/**
 * The role's permissions in effect as sentences, in the groups and order
 * of the permissions page; a permission no group names goes under Other,
 * a group without one of the permissions is left out.
 */
export function permissionSentences(perms: readonly string[], t: T): SentenceGroup[] {
	const held = new Set(perms);
	const listed = new Set<string>();
	const out: SentenceGroup[] = [];
	for (const c of CATEGORIES) {
		const mine = c.permissions.filter((p) => held.has(p));
		mine.forEach((p) => listed.add(p));
		if (mine.length)
			out.push({
				id: c.id,
				label: categoryLabel(c.id, t),
				sentences: mine.map((p) => permissionSentence(p, t))
			});
	}
	const rest = perms.filter((p) => !listed.has(p));
	if (rest.length)
		out.push({
			id: 'other',
			label: categoryLabel('other', t),
			sentences: rest.map((p) => permissionSentence(p, t))
		});
	return out;
}

/** How many devices the account reaches, and how it comes about, one sentence each */
export function accessLines(
	u: Pick<UserDetail, 'role' | 'user_groups' | 'folder_ids' | 'peer_grants' | 'strategy_guid'>,
	scope: EffectiveScope | 'error' | undefined,
	groupName: (guid: string) => string,
	t: T
): string[] {
	const out: string[] = [];
	if (scope === undefined) return out;
	if (scope === 'error') out.push(t('users.page_scope_unknown'));
	else if (!scope.restricted) out.push(t('users.page_scope_all'));
	else if (scope.count === 0) out.push(t('users.page_scope_none'));
	else
		out.push(t(scope.count === 1 ? 'users.page_scope_one' : 'users.page_scope_many', { count: scope.count }));
	if (scope !== 'error' && scope && !scope.restricted) {
		out.push(t('users.page_scope_all_why'));
		return out;
	}
	if (u.user_groups.length)
		out.push(t('users.page_scope_groups', { groups: u.user_groups.map(groupName).join(', ') }));
	if (u.folder_ids.length)
		out.push(
			t(u.folder_ids.length === 1 ? 'users.page_scope_folders_one' : 'users.page_scope_folders_many', {
				count: u.folder_ids.length
			})
		);
	if (u.peer_grants.length)
		out.push(
			t(u.peer_grants.length === 1 ? 'users.page_scope_direct_one' : 'users.page_scope_direct_many', {
				count: u.peer_grants.length
			})
		);
	if (!u.user_groups.length && !u.folder_ids.length && !u.peer_grants.length)
		out.push(t('users.page_scope_nothing_given'));
	return out;
}

/** The organizations line: their names, or that there are none */
export function orgsLine(orgs: readonly UserOrg[] | 'error' | undefined, t: T): string {
	if (orgs === undefined) return '';
	if (orgs === 'error') return t('users.page_orgs_unknown');
	if (!orgs.length) return t('users.page_orgs_none');
	return t('users.page_orgs', { orgs: orgs.map((o) => o.name).join(', ') });
}

/** How the account signs in: a password here, the directory (LDAP) or single sign-on */
export function signInMethod(u: Pick<UserDetail, 'auth_provider'>): 'password' | 'ldap' | 'sso' {
	const p = providerOf(u);
	if (p === 'local') return 'password';
	return p === 'ldap' ? 'ldap' : 'sso';
}

export interface SignInFact {
	key: string;
	tone: 'good' | 'neutral' | 'warning' | 'danger' | 'waiting';
	/** A time the sentence names ({date}) */
	date?: string;
	params?: Record<string, string | number>;
}

/**
 * The state of the account's sign-in, one fact each: the method, two-factor
 * sign-in, and what waits or stops it (disabled, locked, a new password
 * due, a reset link, an invite, an end date).
 */
export function signInFacts(
	u: Pick<
		UserDetail,
		| 'auth_provider'
		| 'two_factor_state'
		| 'totp_enabled'
		| 'two_factor_required'
		| 'disabled'
		| 'locked'
		| 'locked_until'
		| 'must_change_password'
		| 'password_reset_until'
		| 'invite'
		| 'last_login'
		| 'expires_at'
		| 'expired'
	>
): SignInFact[] {
	const out: SignInFact[] = [];
	const method = signInMethod(u);
	out.push({ key: `users.page_method_${method}`, tone: 'neutral' });
	const twoFactor = u.two_factor_state === 'off' && u.totp_enabled ? 'on' : u.two_factor_state;
	if (twoFactor === 'on') out.push({ key: 'users.page_2fa_on', tone: 'good' });
	else if (twoFactor === 'required') out.push({ key: 'users.page_2fa_required', tone: 'warning' });
	else if (method === 'password') out.push({ key: 'users.page_2fa_off', tone: 'neutral' });
	if (u.disabled) out.push({ key: 'users.page_disabled', tone: 'warning' });
	if (u.locked) out.push({ key: 'users.page_locked', tone: 'danger', date: u.locked_until ?? '' });
	if (u.must_change_password) out.push({ key: 'users.page_must_change', tone: 'warning' });
	if (u.password_reset_until)
		out.push({ key: 'users.page_reset_waiting', tone: 'waiting', date: u.password_reset_until });
	if (u.invite && !u.last_login)
		out.push(
			u.invite.expired
				? { key: 'users.page_invite_expired', tone: 'warning', date: u.invite.expires_at }
				: { key: 'users.page_invite_waiting', tone: 'waiting', date: u.invite.expires_at }
		);
	if (u.expires_at)
		out.push(
			u.expired
				? { key: 'users.page_expired', tone: 'warning', date: u.expires_at }
				: { key: 'users.page_expires', tone: 'neutral', date: u.expires_at }
		);
	return out;
}

/** The audit page searching for the account (it searches names, actions and details) */
export function auditHref(username: string): string {
	return `/security-audit?tab=audit&q=${encodeURIComponent(username)}`;
}
