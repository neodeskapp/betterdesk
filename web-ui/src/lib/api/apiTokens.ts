/**
 * API tokens (Go /api/panel/api-tokens): tokens that scripts, the neodesk
 * command line and its MCP server sign in with. Admins only. A new token's
 * value is in the create answer and nowhere else.
 */

import * as v from 'valibot';
import { api, request } from './client.ts';

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
	revoked_by: Text
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

export async function revokeApiToken(id: string): Promise<void> {
	await request(`/api/panel/api-tokens/${encodeURIComponent(id)}`, { method: 'DELETE' });
}
