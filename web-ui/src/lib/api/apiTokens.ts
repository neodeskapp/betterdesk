/**
 * API tokens (Go /api/panel/api-tokens): tokens that scripts, the neodesk
 * command line and its MCP server sign in with. Admins only. A new token's
 * value is in the create answer and nowhere else.
 */

import * as v from 'valibot';
import { api } from './client.ts';

const Text = v.optional(
	v.pipe(
		v.nullable(v.string()),
		v.transform((s) => s ?? '')
	),
	''
);
const List = v.optional(
	v.pipe(
		v.nullable(v.array(v.string())),
		v.transform((l) => l ?? [])
	),
	[]
);

const Count = v.optional(v.number(), 0);

/** What a token made that outlives a request, by kind: still open (the list) or ended (a revoke) */
export const Made = v.object({
	guest_links: Count,
	device_tokens: Count,
	support_sessions: Count,
	invites: Count,
	pairing_codes: Count,
	share_links: Count,
	organization_invitations: Count
});
export type Made = v.InferOutput<typeof Made>;

const NOTHING_MADE: Made = {
	guest_links: 0,
	device_tokens: 0,
	support_sessions: 0,
	invites: 0,
	pairing_codes: 0,
	share_links: 0,
	organization_invitations: 0
};

/** A computer a token let in: joined through its invite or pairing code, or approved with it */
export const LetIn = v.object({
	device_id: v.string(),
	name: Text,
	how: Text,
	at: Text
});
export type LetIn = v.InferOutput<typeof LetIn>;

export const ApiToken = v.object({
	id: v.string(),
	name: Text,
	hint: Text,
	user_id: v.optional(v.number(), 0),
	username: Text,
	permissions: List,
	read_only: v.optional(v.boolean(), true),
	allow_list: List,
	state: v.optional(v.picklist(['active', 'expired', 'revoked']), 'active'),
	created_at: Text,
	expires_at: Text,
	last_used_at: Text,
	last_used_ip: Text,
	revoked_at: Text,
	revoked_by: Text,
	/** What the token made that is still open and ends with it (the list only) */
	ends_with_it: v.optional(Made, NOTHING_MADE),
	/** The computers the token let in, which its end leaves in (the list only) */
	let_in: v.optional(
		v.pipe(
			v.nullable(v.array(LetIn)),
			v.transform((l) => l ?? [])
		),
		[]
	)
});
export type ApiToken = v.InferOutput<typeof ApiToken>;

export async function listApiTokens(all = false, signal?: AbortSignal): Promise<ApiToken[]> {
	const out = await api(
		v.object({
			data: v.optional(v.nullable(v.object({ tokens: v.optional(v.nullable(v.array(ApiToken))) })))
		}),
		'/api/panel/api-tokens',
		{ query: all ? { all: 'true' } : undefined, signal }
	);
	return out.data?.tokens ?? [];
}

/** The permissions a new token may be given (the caller's) and every permission there is */
export async function tokenPermissions(signal?: AbortSignal): Promise<{ held: string[]; all: string[] }> {
	const out = await api(
		v.object({
			data: v.optional(v.nullable(v.object({ permissions: List, all: List })))
		}),
		'/api/panel/api-tokens/permissions',
		{ signal }
	);
	return { held: out.data?.permissions ?? [], all: out.data?.all ?? [] };
}

export interface ApiTokenInput {
	name: string;
	permissions: string[];
	read_only: boolean;
	/** A date (2027-01-31) */
	expires_at: string;
	allow_list: string[];
}

/** The new token: its whole value, shown once, and the stored row */
export async function createApiToken(body: ApiTokenInput): Promise<{ token: string; info: ApiToken }> {
	const out = await api(
		v.object({ data: v.object({ token: v.string(), api_token: ApiToken }) }),
		'/api/panel/api-tokens',
		{ method: 'POST', body }
	);
	return { token: out.data.token, info: out.data.api_token };
}

/** Revokes a token; the answer counts what ended with it */
export async function revokeApiToken(id: string): Promise<Made> {
	const out = await api(
		v.object({ data: v.optional(v.nullable(v.object({ ended: v.optional(Made, NOTHING_MADE) }))) }),
		`/api/panel/api-tokens/${encodeURIComponent(id)}`,
		{ method: 'DELETE' }
	);
	return out.data?.ended ?? NOTHING_MADE;
}

/* ── Another account's tokens, on the user's page ── */

/** The tokens the account made. Needs user.view */
export async function listUserApiTokens(userId: number | string, signal?: AbortSignal): Promise<ApiToken[]> {
	const out = await api(
		v.object({
			data: v.optional(v.nullable(v.object({ tokens: v.optional(v.nullable(v.array(ApiToken))) })))
		}),
		`/api/users/${encodeURIComponent(String(userId))}/api-tokens`,
		{ signal }
	);
	return out.data?.tokens ?? [];
}

/** Revokes one of the account's tokens. Needs user.security and an account the caller may change */
export async function revokeUserApiToken(userId: number | string, id: string): Promise<Made> {
	const out = await api(
		v.object({ data: v.optional(v.nullable(v.object({ ended: v.optional(Made, NOTHING_MADE) }))) }),
		`/api/users/${encodeURIComponent(String(userId))}/api-tokens/${encodeURIComponent(id)}`,
		{ method: 'DELETE' }
	);
	return out.data?.ended ?? NOTHING_MADE;
}
