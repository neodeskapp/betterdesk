/**
 * Console users and user groups (routes/users.routes.js). Only what other
 * pages need so far; the users page adds the rest when it is ported.
 */

import * as v from 'valibot';
import { api, request, wrapped, Ok, Text } from './client.ts';

export const UserGroup = v.looseObject({
	guid: v.string(),
	name: Text,
	note: Text
});
export type UserGroup = v.InferOutput<typeof UserGroup>;

/** Needs user.view or device.edit */
export async function listUserGroups(signal?: AbortSignal): Promise<UserGroup[]> {
	const out = await api(wrapped(v.looseObject({ groups: v.array(UserGroup) })), '/api/panel/user-groups', {
		signal
	});
	return out.groups;
}

export const UserRow = v.looseObject({
	id: v.union([v.number(), v.string()]),
	username: Text,
	role: Text
});
export type UserRow = v.InferOutput<typeof UserRow>;

/** Needs user.view */
export async function listUsers(signal?: AbortSignal): Promise<UserRow[]> {
	const out = await api(wrapped(v.looseObject({ users: v.array(UserRow) })), '/api/users', { signal });
	return out.users;
}

/* ── The users page (routes/users.routes.js) ── */

/** Text that is always a string ('' for missing or null) */
const Str = v.pipe(
	v.optional(v.nullable(v.union([v.string(), v.number()])), ''),
	v.transform((s) => (s === null || s === undefined ? '' : String(s)))
);

const StrArray = v.pipe(
	v.optional(v.nullable(v.array(v.union([v.string(), v.number()]))), null),
	v.transform((a) => (a ?? []).map(String))
);

/** An email address that waits for its confirmation link to be used */
export const PendingEmail = v.looseObject({
	email: Str,
	expires_at: Str,
	expired: v.optional(v.boolean(), false)
});
export type PendingEmail = v.InferOutput<typeof PendingEmail>;

/** A console account as GET /api/users lists it */
export const ConsoleUser = v.looseObject({
	id: v.union([v.number(), v.string()]),
	username: Str,
	role: Str,
	email: Str,
	auth_provider: Str,
	created_at: v.optional(v.nullable(v.string())),
	last_login: v.optional(v.nullable(v.string())),
	user_groups: StrArray,
	folder_ids: StrArray,
	peer_grants: StrArray,
	strategy_guid: Str,
	has_avatar: v.optional(v.nullable(v.boolean())),
	avatar_version: v.optional(v.nullable(v.union([v.string(), v.number()]))),
	/** Two-factor sign-in is on (older servers leave it out) */
	totp_enabled: v.optional(v.nullable(v.union([v.boolean(), v.number()]))),
	/** The account may not sign in (older servers leave it out: enabled) */
	disabled: v.optional(v.boolean(), false),
	/* Sign-in security (older servers leave it out) */
	/** Failed sign-ins locked the account until locked_until, at the addresses of locks */
	locked: v.optional(v.boolean(), false),
	locked_until: v.optional(v.nullable(v.string()), null),
	locks: v.optional(v.array(v.looseObject({ ip: Str, until: Str, failures: v.optional(v.number(), 0) })), []),
	/** The next sign-in asks for a new password */
	must_change_password: v.optional(v.boolean(), false),
	/** An admin requires two-factor sign-in of this account */
	two_factor_required: v.optional(v.boolean(), false),
	/** on, off, or required (by the account's rule or the server's switch) and not set up */
	two_factor_state: v.optional(v.picklist(['on', 'off', 'required']), 'off'),
	/** Until when a reset link mailed to the account works */
	password_reset_until: v.optional(v.nullable(v.string()), null),
	/** A new address that waits for its confirmation; the address above stays until then */
	email_pending: v.optional(v.nullable(PendingEmail), null),
	/** The account's end date; expired once it has passed (the job disables it then) */
	expires_at: v.optional(v.nullable(v.string()), null),
	expired: v.optional(v.boolean(), false),
	/** The rule for inactive accounts leaves it alone */
	inactive_exempt: v.optional(v.boolean(), false)
});
export type ConsoleUser = v.InferOutput<typeof ConsoleUser>;

/** Every account with its groups, folders and direct devices. Needs user.view */
export async function listConsoleUsers(signal?: AbortSignal): Promise<ConsoleUser[]> {
	const out = await api(wrapped(v.looseObject({ users: v.array(ConsoleUser) })), '/api/users', { signal });
	return out.users;
}

/** What a new or edited account gets; the server changes only the keys sent */
export interface UserInput {
	username?: string;
	password?: string;
	role?: string;
	email?: string;
	groupGuids?: string[];
	folderIds?: number[];
	peerIds?: string[];
	strategyGuid?: string;
	/** Disable (true) or enable (false) the account; never your own */
	disabled?: boolean;
	/** false turns off the account's two-factor sign-in; never your own */
	totp_enabled?: false;
	/** The end date (RFC 3339), null for none; never your own */
	expires_at?: string | null;
	/** Keeps the rule for inactive accounts away from it */
	inactive_exempt?: boolean;
}

/** Needs user.create; the role must be one the caller may give */
export function createUser(body: UserInput & { username: string; password: string }) {
	return api(Ok, '/api/users', { method: 'POST', body });
}

/** What POST /api/users/invite answers: the account, and whether the mail went out */
export const InvitedUser = v.looseObject({
	id: v.union([v.number(), v.string()]),
	username: Str,
	invite_sent: v.optional(v.boolean(), false)
});
export type InvitedUser = v.InferOutput<typeof InvitedUser>;

/**
 * Makes an account without a password and mails the address a link to
 * choose one. Without a username the part of the address before the @
 * gives one. Needs user.create and the console's email settings (409
 * without them). The account stays when the mail fails: invite_sent is
 * false then.
 */
export function inviteUser(
	body: Omit<UserInput, 'password' | 'disabled' | 'totp_enabled'> & { email: string }
) {
	return api(wrapped(InvitedUser), '/api/users/invite', { method: 'POST', body });
}

/**
 * Mails the account a link to set a new password; it works once, for 24
 * hours. Needs user.edit, an address on the account (400 without) and the
 * console's email settings (409 without).
 */
export async function sendResetLink(id: number | string): Promise<{ expires_at: string }> {
	// Servers before 3.6 answer without data
	const out = await api(
		v.looseObject({ data: v.optional(v.looseObject({ expires_at: Str }), { expires_at: '' }) }),
		`/api/users/${encodeURIComponent(String(id))}/password-reset-link`,
		{ method: 'POST', body: {} }
	);
	return out.data;
}

/** Makes the reset link the account was mailed stop working. Needs user.edit; 404 when none waits */
export function cancelResetLink(id: number | string) {
	return api(Ok, `/api/users/${encodeURIComponent(String(id))}/password-reset-link`, { method: 'DELETE' });
}

/* ── Sign-in security of other accounts (user.security) ── */

/**
 * Gives the account a temporary password (the one given, or one the server
 * makes up) and answers it, to be shown once. The account chooses a new
 * one at its next sign-in; its sessions end. Not for your own account nor
 * for LDAP or single sign-on accounts.
 */
export async function setTemporaryPassword(id: number | string, password?: string): Promise<string> {
	const out = await api(
		wrapped(v.looseObject({ password: Str })),
		`/api/users/${encodeURIComponent(String(id))}/temporary-password`,
		{ method: 'POST', body: password ? { password } : {} }
	);
	return out.password;
}

export const BulkResult = v.looseObject({
	id: v.union([v.number(), v.string()]),
	username: v.optional(v.string(), ''),
	/** changed, unchanged (it was so already) or refused (error says why) */
	outcome: v.picklist(['changed', 'unchanged', 'refused']),
	error: v.optional(v.string(), '')
});
export type BulkResult = v.InferOutput<typeof BulkResult>;

/** The next sign-in of each account asks for a new password (or no longer, with required false) */
export async function requirePasswordChange(
	ids: (number | string)[],
	required = true
): Promise<BulkResult[]> {
	const out = await api(
		wrapped(v.looseObject({ results: v.array(BulkResult) })),
		'/api/users/require-password-change',
		{ method: 'POST', body: { ids, required } }
	);
	return out.results;
}

/** Ends the lock after failed sign-ins */
export function unlockUser(id: number | string) {
	return api(Ok, `/api/users/${encodeURIComponent(String(id))}/unlock`, { method: 'POST', body: {} });
}

/** Requires two-factor sign-in of one local account, or leaves it to the server's switch */
export function setTwoFactorRequired(id: number | string, required: boolean) {
	return api(Ok, `/api/users/${encodeURIComponent(String(id))}/two-factor-required`, {
		method: 'PUT',
		body: { required }
	});
}

/**
 * Clears the second factor and ends the account's sessions. set_up_again:
 * a policy requires it, so the person sets it up at the next sign-in; else
 * it is off.
 */
export function resetTwoFactor(id: number | string): Promise<{ set_up_again: boolean }> {
	return api(
		wrapped(v.looseObject({ set_up_again: v.optional(v.boolean(), false) })),
		`/api/users/${encodeURIComponent(String(id))}/two-factor/reset`,
		{ method: 'POST', body: {} }
	);
}

/** The recovery codes stop working; the person makes new ones on Your account */
export function invalidateRecoveryCodes(id: number | string) {
	return api(Ok, `/api/users/${encodeURIComponent(String(id))}/recovery-codes/invalidate`, {
		method: 'POST',
		body: {}
	});
}

/** A new invite link for an account that hasn't signed in yet; the old link stops working. Needs user.edit */
export function resendInvite(id: number | string) {
	return api(Ok, `/api/users/${encodeURIComponent(String(id))}/invite`, { method: 'POST', body: {} });
}

/**
 * Withdraws the invite of an account that never signed in: the link stops
 * working and the account is deleted. Needs user.create; 409 for an
 * account without an open or expired invite.
 */
export function revokeInvite(id: number | string) {
	return api(Ok, `/api/users/${encodeURIComponent(String(id))}/invite`, { method: 'DELETE' });
}

export const PendingInvite = v.looseObject({
	user_id: v.union([v.number(), v.string()]),
	expires_at: Str,
	expired: v.optional(v.boolean(), false)
});
export type PendingInvite = v.InferOutput<typeof PendingInvite>;

export const InviteList = v.looseObject({
	invites: v.array(PendingInvite),
	/** The console has email settings, so invites can go out */
	mail_ready: v.optional(v.boolean(), false)
});
export type InviteList = v.InferOutput<typeof InviteList>;

/** The invites not used yet (expired ones for 30 days). Needs user.view */
export function listInvites(signal?: AbortSignal): Promise<InviteList> {
	return api(wrapped(InviteList), '/api/users/invites', { signal });
}

/** Needs user.edit */
export function updateUser(id: number | string, body: UserInput) {
	return api(Ok, `/api/users/${encodeURIComponent(String(id))}`, { method: 'PATCH', body });
}

/**
 * Needs user.delete; never yourself. handoverTo gives the account's API
 * tokens, organization memberships, saved reports and open tickets to
 * that account first (one the caller may change).
 */
export function deleteUser(id: number | string, handoverTo?: number | string) {
	const query = handoverTo !== undefined ? `?handover_to=${encodeURIComponent(String(handoverTo))}` : '';
	return api(Ok, `/api/users/${encodeURIComponent(String(id))}${query}`, { method: 'DELETE' });
}

const Count = v.pipe(
	v.optional(v.nullable(v.number()), 0),
	v.transform((n) => n ?? 0)
);

/** What a delete removes and keeps, counted, and who may take over */
export const DeletePreview = v.looseObject({
	id: v.union([v.number(), v.string()]),
	username: Str,
	counts: v.looseObject({
		sessions: Count,
		client_sign_ins: Count,
		api_tokens: Count,
		organizations: Count,
		user_groups: Count,
		device_grants: Count,
		saved_reports: Count,
		open_tickets: Count,
		audit_entries: Count,
		tickets_opened: Count,
		ticket_comments: Count,
		chat_messages: Count
	}),
	candidates: v.array(v.looseObject({ id: v.union([v.number(), v.string()]), username: Str, role: Str })),
	organizations_move: v.optional(v.boolean(), false)
});
export type DeletePreview = v.InferOutput<typeof DeletePreview>;

/** Needs user.delete */
export function getDeletePreview(id: number | string, signal?: AbortSignal): Promise<DeletePreview> {
	return api(wrapped(DeletePreview), `/api/users/${encodeURIComponent(String(id))}/delete-preview`, {
		signal
	});
}

/** Needs user.edit (local accounts) */
export function resetUserPassword(id: number | string, newPassword: string) {
	return api(Ok, `/api/users/${encodeURIComponent(String(id))}/reset-password`, {
		method: 'POST',
		body: { newPassword }
	});
}

export const EffectiveScope = v.object({
	count: v.pipe(
		v.optional(v.nullable(v.number()), 0),
		v.transform((n) => n ?? 0)
	),
	restricted: v.pipe(
		v.optional(v.nullable(v.boolean()), false),
		v.transform((b) => !!b)
	)
});
export type EffectiveScope = v.InferOutput<typeof EffectiveScope>;

/** How many devices the account can see. Needs user.view */
export function getEffectiveScope(id: number | string, signal?: AbortSignal): Promise<EffectiveScope> {
	return api(wrapped(EffectiveScope), `/api/users/${encodeURIComponent(String(id))}/effective-scope`, {
		signal
	});
}

/** An organization the account belongs to, with its role there */
export const UserOrg = v.pipe(
	v.looseObject({
		id: Str,
		org_id: Str,
		organization_id: Str,
		name: Str,
		org_name: Str,
		role: Str
	}),
	v.transform((o) => {
		const id = o.org_id || o.id || o.organization_id;
		const name = o.org_name || o.name;
		return { id, name, role: o.role };
	})
);
export type UserOrg = v.InferOutput<typeof UserOrg>;

/** Needs user.view (Go answers without `success`) */
export async function listUserOrgs(id: number | string, signal?: AbortSignal): Promise<UserOrg[]> {
	const out = await api(
		v.looseObject({ organizations: v.optional(v.nullable(v.array(UserOrg)), null) }),
		`/api/users/${encodeURIComponent(String(id))}/organizations`,
		{ signal }
	);
	return (out.organizations ?? []).filter((o) => o.id);
}

/** Needs org.manage_users */
export function addUserToOrg(id: number | string, orgId: string) {
	return request(`/api/users/${encodeURIComponent(String(id))}/organizations`, {
		method: 'POST',
		body: { org_id: orgId }
	});
}

/** Needs org.manage_users; userId is the console user id */
export function removeUserFromOrg(orgId: string, userId: number | string) {
	return request(
		`/api/panel/org/${encodeURIComponent(orgId)}/members/${encodeURIComponent(String(userId))}`,
		{ method: 'DELETE' }
	);
}

/* ── User groups ── */

export const UserGroupRow = v.looseObject({
	...UserGroup.entries,
	name: Str,
	note: Str,
	team_id: Str,
	member_count: v.pipe(
		v.optional(v.nullable(v.number()), 0),
		v.transform((n) => n ?? 0)
	)
});
export type UserGroupRow = v.InferOutput<typeof UserGroupRow>;

/** With member counts. Needs user.view or device.edit */
export async function listUserGroupRows(signal?: AbortSignal): Promise<UserGroupRow[]> {
	const out = await api(wrapped(v.looseObject({ groups: v.array(UserGroupRow) })), '/api/panel/user-groups', {
		signal
	});
	return out.groups;
}

/** Needs user.edit. PATCH replaces name and note (name is required) */
export function saveUserGroup(guid: string | null, body: { name: string; note: string }) {
	return guid
		? api(Ok, `/api/panel/user-groups/${encodeURIComponent(guid)}`, { method: 'PATCH', body })
		: api(Ok, '/api/panel/user-groups', { method: 'POST', body });
}

/** Needs user.edit */
export function deleteUserGroup(guid: string) {
	return api(Ok, `/api/panel/user-groups/${encodeURIComponent(guid)}`, { method: 'DELETE' });
}

/* ── Your own profile picture ── */

const OwnPicture = wrapped(
	v.looseObject({
		avatar_version: v.pipe(
			v.optional(v.nullable(v.union([v.string(), v.number()])), null),
			v.transform((x) => (x === null || x === undefined || x === '' ? null : String(x)))
		)
	})
);

/** The largest picture the server takes: 512 KB */
export const MAX_AVATAR_BYTES = 512 * 1024;

/**
 * Upload a picture (PNG, JPEG, WebP or GIF, at most 512 KB) as the signed-in
 * person's profile picture. It replaces a picture from single sign-on and
 * stays at the next sign-in. Returns the new picture version.
 */
export async function uploadMyAvatar(file: Blob): Promise<string | null> {
	return (await api(OwnPicture, '/api/users/me/avatar', { method: 'PUT', body: file })).avatar_version;
}

/** Remove the signed-in person's picture; one from single sign-on returns at the next sign-in */
export async function removeMyAvatar(): Promise<void> {
	await api(OwnPicture, '/api/users/me/avatar', { method: 'DELETE' });
}

/* ── Email address changes ── */

/** The signed-in account's address, the one waiting for its confirmation, and whether mail goes out */
export const OwnEmail = v.looseObject({
	email: Str,
	auth_provider: Str,
	email_pending: v.optional(v.nullable(PendingEmail), null),
	mail_ready: v.optional(v.boolean(), false)
});
export type OwnEmail = v.InferOutput<typeof OwnEmail>;

const userPath = (id: number | string) => `/api/users/${encodeURIComponent(String(id))}`;

/** Ends an account's address change that waits for its confirmation. Needs user.edit */
export function cancelEmailChange(id: number | string) {
	return api(Ok, `${userPath(id)}/email-change`, { method: 'DELETE' });
}

/** Mails a new confirmation link for the pending address; the old link stops working. Needs user.edit */
export function resendEmailChange(id: number | string) {
	return api(Ok, `${userPath(id)}/email-change/resend`, { method: 'POST', body: {} });
}

export function getOwnEmail(signal?: AbortSignal): Promise<OwnEmail> {
	return api(wrapped(OwnEmail), '/api/users/me/email', { signal });
}

/**
 * Asks for a new address for the signed-in account: it gets a link that
 * confirms it, and the address changes when the link is used. Without the
 * console's email settings it changes at once. "" takes the address away.
 */
export function changeOwnEmail(email: string): Promise<OwnEmail> {
	return api(wrapped(OwnEmail), '/api/users/me/email', { method: 'POST', body: { email } });
}

export function cancelOwnEmailChange(): Promise<OwnEmail> {
	return api(wrapped(OwnEmail), '/api/users/me/email-change', { method: 'DELETE' });
}

export function resendOwnEmailChange(): Promise<OwnEmail> {
	return api(wrapped(OwnEmail), '/api/users/me/email-change/resend', { method: 'POST', body: {} });
}

/** What the confirmation link is for */
export const EmailChangeLink = v.looseObject({ username: Str, email: Str, expires_at: Str });
export type EmailChangeLink = v.InferOutput<typeof EmailChangeLink>;

/** Which account and address the link confirms; ApiError 404 for one that cannot be used */
export function lookupEmailChange(token: string): Promise<EmailChangeLink> {
	return api(wrapped(EmailChangeLink), '/api/auth/email-change/lookup', {
		method: 'POST',
		body: { token },
		allowAnonymous: true,
		activity: false
	});
}

/** Confirms the address and uses the link up; 404 when it cannot be used (any more) */
export function confirmEmailChange(token: string): Promise<EmailChangeLink> {
	return api(wrapped(EmailChangeLink), '/api/auth/email-change/confirm', {
		method: 'POST',
		body: { token },
		allowAnonymous: true
	});
}
