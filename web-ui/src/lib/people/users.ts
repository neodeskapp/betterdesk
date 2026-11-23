/**
 * The users page's rules without a DOM: which roles someone may give, the
 * password rules the server applies, search, and the texts for a user's
 * device scope. Mirrors routes/users.routes.js (canAssignUserRole) and
 * services/authService.js (validatePasswordStrength).
 */

import type { ConsoleUser, EffectiveScope, UserOrg } from '#lib/api/users.ts';

/**
 * Roles in the order the picker shows them. "admin" is the older name of
 * super_admin (same permissions, see middleware/auth.js).
 */
export const ROLE_ORDER = [
	'viewer',
	'guest_issuer',
	'operator',
	'global_admin',
	'server_admin',
	'super_admin',
	'admin',
	'pro'
] as const;

/** The roles described in the side column, as the old page listed them */
export const DESCRIBED_ROLES = [
	'viewer',
	'guest_issuer',
	'operator',
	'global_admin',
	'server_admin',
	'super_admin',
	'pro'
] as const;

const SUPER_ROLES = new Set(['super_admin', 'admin']);

export function isSuperRole(role: string | null | undefined): boolean {
	return SUPER_ROLES.has(String(role ?? ''));
}

/** Roles the signed-in person may give (canAssignUserRole in routes/users.routes.js) */
export function assignableRoles(myRole: string | null | undefined): string[] {
	if (isSuperRole(myRole)) return [...ROLE_ORDER];
	if (myRole === 'global_admin') return ['viewer', 'guest_issuer', 'operator', 'pro'];
	return [];
}

/**
 * Whether the signed-in person may edit, reset or delete this account, as
 * mayChange on the server: their own, or one whose role they may give.
 */
export function mayChangeUser(
	myRole: string | null | undefined,
	myId: string,
	user: Pick<ConsoleUser, 'id' | 'role'>
): boolean {
	if (String(user.id) === myId) return true;
	return assignableRoles(myRole).includes(user.role);
}

/**
 * Whether the signed-in person may disable or enable this account: one
 * they may change, but never their own (the server refuses that).
 */
export function mayDisableUser(
	myRole: string | null | undefined,
	myId: string,
	user: Pick<ConsoleUser, 'id' | 'role'>
): boolean {
	return String(user.id) !== myId && mayChangeUser(myRole, myId, user);
}

/**
 * The role choices of the user sheet: the roles the editor may give, plus
 * the user's current role (shown, but not choosable) so it stays clear.
 * LDAP accounts get their role from the directory: every choice is locked.
 */
export function roleChoices(
	myRole: string | null | undefined,
	user: Pick<ConsoleUser, 'role' | 'auth_provider'> | null
): { role: string; disabled: boolean }[] {
	const allowed = new Set(assignableRoles(myRole));
	const current = user ? user.role : 'viewer';
	const locked = !!user && providerOf(user) === 'ldap';
	return ROLE_ORDER.filter((r) => allowed.has(r) || r === current).map((role) => ({
		role,
		disabled: locked || !allowed.has(role)
	}));
}

export function providerOf(user: Pick<ConsoleUser, 'auth_provider'>): string {
	return String(user.auth_provider || 'local').toLowerCase();
}

export type Strength = 'weak' | 'medium' | 'strong';

/** A rough strength for the line under the field; the server refuses none for it */
export function passwordStrength(password: string): Strength {
	let score = 0;
	if (password.length >= 8) score++;
	if (password.length >= 12) score++;
	if (/[a-z]/.test(password)) score++;
	if (/[A-Z]/.test(password)) score++;
	if (/[0-9]/.test(password)) score++;
	if (/[^a-zA-Z0-9]/.test(password)) score++;
	return score <= 2 ? 'weak' : score <= 4 ? 'medium' : 'strong';
}

/** Why a password would be refused (a string key), or null when it is fine */
export function passwordProblem(password: string, required: boolean): string | null {
	if (!password) return required ? 'users.password_required' : null;
	if (password.length < 8) return 'users.password_too_short';
	return null;
}

/** The server's rule for an account name: 3 to 64 letters, digits and _ . @ - */
export const USERNAME_RE = /^[A-Za-z0-9_.@-]{3,64}$/;

/** Why a new username would be refused (a string key), or null */
export function usernameProblem(username: string): string | null {
	if (!username) return 'users.username_required';
	if (!USERNAME_RE.test(username)) return 'users.invalid_username';
	return null;
}

/** "a, b; c d" → ['a', 'b', 'c', 'd'] (device IDs for direct access) */
export function splitIds(text: string): string[] {
	return text
		.split(/[,;\s]+/)
		.map((s) => s.trim())
		.filter(Boolean);
}

/** Users whose username, email or role name contains every word of the search */
export function filterUsers(
	users: readonly ConsoleUser[],
	query: string,
	roleLabel: (role: string) => string
): ConsoleUser[] {
	const words = query.toLowerCase().split(/\s+/).filter(Boolean);
	if (!words.length) return users as ConsoleUser[];
	return users.filter((u) => {
		const text = [u.username, u.email, roleLabel(u.role)].filter(Boolean).join(' ').toLowerCase();
		return words.every((w) => text.includes(w));
	});
}

export type ScopeText = { key: string; params?: Record<string, number>; muted: boolean };

/** The Devices cell: still loading, failed, all, none or a count */
export function scopeText(scope: EffectiveScope | 'error' | undefined): ScopeText {
	if (!scope) return { key: '', muted: true };
	if (scope === 'error') return { key: 'users.scope_unknown', muted: true };
	if (!scope.restricted) return { key: 'users.scope_all', muted: false };
	if (scope.count === 0) return { key: 'users.scope_none', muted: false };
	return {
		key: scope.count === 1 ? 'users.scope_count_one' : 'users.scope_count_many',
		params: { count: scope.count },
		muted: false
	};
}

/** Organizations of a user that are not theirs yet */
export function availableOrgs<O extends { id: string }>(all: readonly O[], member: readonly UserOrg[]): O[] {
	const ids = new Set(member.map((o) => o.id));
	return all.filter((o) => !ids.has(String(o.id)));
}

/** Run `fn` over items, at most `limit` at a time (counts per row without flooding the server) */
export async function eachLimited<T>(
	items: readonly T[],
	limit: number,
	fn: (item: T) => Promise<void>
): Promise<void> {
	const queue = [...items];
	const worker = async () => {
		while (queue.length) {
			const item = queue.shift() as T;
			await fn(item);
		}
	};
	await Promise.all(Array.from({ length: Math.min(limit, queue.length) }, worker));
}

/** Notes the server gives its built-in groups, in English, and their keys */
const GROUP_NOTES: Readonly<Record<string, string>> = {
	'Default user group': 'users.note_default_user_group',
	'Default device group': 'users.note_default_device_group',
	'Default group created with organization (commercialization)': 'users.note_org_default_group'
};

/** A group's note: a built-in group's in the console language, anyone else's as written */
export function groupNote(note: string, t: (key: string) => string): string {
	const key = GROUP_NOTES[note];
	const text = key ? t(key) : '';
	return text && text !== key ? text : note;
}
