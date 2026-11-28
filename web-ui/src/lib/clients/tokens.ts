/**
 * Device tokens without a DOM: one status per token, the filter, the count
 * line's numbers and the checks of the two forms. Tested in
 * tests/unit/clients/tokens.test.ts.
 */

import type { DeviceToken } from '#lib/api/tokens.ts';

export type TokenStatusKey = 'pending' | 'active' | 'expired' | 'revoked';
export type TokenTone = 'waiting' | 'good' | 'neutral';

export interface TokenStatus {
	/** The filter group */
	key: TokenStatusKey;
	tone: TokenTone;
	/** tokens.status_* */
	label: 'status_pending' | 'status_used' | 'status_used_up' | 'status_expired' | 'status_revoked';
}

/** A token for many computers (A278) */
export function isRollout(t: Pick<DeviceToken, 'kind'>): boolean {
	return t.kind === 'rollout';
}

/** How many more computers a rollout token lets join */
export function rolloutLeft(t: Pick<DeviceToken, 'max_uses' | 'use_count'>): number {
	return Math.max(0, t.max_uses - t.use_count);
}

function expiredByDate(t: DeviceToken, now: number): boolean {
	// A rollout token is "active" once a computer joined and still ends on its date
	if ((t.status !== 'pending' && !(isRollout(t) && t.status === 'active')) || !t.expires_at) return false;
	const at = Date.parse(t.expires_at);
	return Number.isFinite(at) && at < now;
}

/**
 * Not used yet waits for a device; used still lets devices join; used up,
 * expired and revoked have ended (GUIDE "Data display").
 */
export function tokenStatus(t: DeviceToken, now: number = Date.now()): TokenStatus {
	if (t.status === 'revoked') return { key: 'revoked', tone: 'neutral', label: 'status_revoked' };
	if (t.status === 'expired' || expiredByDate(t, now))
		return { key: 'expired', tone: 'neutral', label: 'status_expired' };
	if (t.status === 'active' && t.max_uses > 0 && t.use_count >= t.max_uses)
		return { key: 'active', tone: 'neutral', label: 'status_used_up' };
	if (t.status === 'active') return { key: 'active', tone: 'good', label: 'status_used' };
	return { key: 'pending', tone: 'waiting', label: 'status_pending' };
}

export type TokenFilter = 'all' | 'pending' | 'active' | 'revoked';

/** Expired tokens were never used, so they sit with "not used yet" */
export function filterTokens(
	list: readonly DeviceToken[],
	filter: TokenFilter,
	search: string,
	now: number = Date.now()
): DeviceToken[] {
	const q = search.trim().toLowerCase();
	return list.filter((t) => {
		if (filter !== 'all') {
			const key = tokenStatus(t, now).key;
			if (filter === 'pending' ? key !== 'pending' && key !== 'expired' : key !== filter) return false;
		}
		if (!q) return true;
		return [t.name, t.token_prefix, t.peer_id, t.note].some((v) => v.toLowerCase().includes(q));
	});
}

export function countTokens(
	list: readonly DeviceToken[],
	now: number = Date.now()
): Record<TokenStatusKey, number> {
	const out: Record<TokenStatusKey, number> = { pending: 0, active: 0, expired: 0, revoked: 0 };
	for (const t of list) out[tokenStatus(t, now).key]++;
	return out;
}

/** Edit and revoke only while the token can still be revoked */
export function tokenMenu(t: DeviceToken): boolean {
	return t.status !== 'revoked';
}

/** The expiry choices, in seconds (0: never) */
export const EXPIRY_CHOICES = [0, 3600, 86400, 604800, 2592000] as const;
export const EXPIRY_LABELS: Record<number, string> = {
	0: 'no_expiration',
	3600: 'expires_1h',
	86400: 'expires_24h',
	604800: 'expires_7d',
	2592000: 'expires_30d'
};

/** A rollout token's expiry choices, in seconds: it always ends. 30 days first */
export const ROLLOUT_EXPIRY_CHOICES = [2592000, 604800, 86400, 7776000, 31536000] as const;
export const ROLLOUT_EXPIRY_LABELS: Record<number, string> = {
	86400: 'expires_24h',
	604800: 'expires_7d',
	2592000: 'expires_30d',
	7776000: 'expires_90d',
	31536000: 'expires_1y'
};
/** A rollout token's computers: the default and the most the server takes */
export const ROLLOUT_DEFAULT_COMPUTERS = 100;
export const ROLLOUT_MAX_COMPUTERS = 100000;

/** A rollout token's "Computers allowed": a whole number from 1 to 100,000 */
export function rolloutLimitOk(raw: string | number | null | undefined): boolean {
	return usesOk(raw) && usesValue(raw) >= 1 && usesValue(raw) <= ROLLOUT_MAX_COMPUTERS;
}

/** "Uses allowed" as typed: a whole number of 0 or more (0 means no limit) */
export function usesOk(raw: string | number | null | undefined): boolean {
	const text = String(raw ?? '').trim();
	return /^\d+$/.test(text) && Number(text) <= Number.MAX_SAFE_INTEGER;
}

/** Bulk "Uses allowed": a whole number of 1 or more */
export function bulkUsesOk(raw: string | number | null | undefined): boolean {
	return usesOk(raw) && usesValue(raw) >= 1;
}

/** The number of a field usesOk accepted */
export function usesValue(raw: string | number | null | undefined): number {
	return parseInt(String(raw ?? '').trim(), 10);
}

/** Bulk: between 1 and 100 tokens */
export function bulkCountOk(raw: string | number): boolean {
	const n = typeof raw === 'number' ? raw : parseInt(String(raw), 10);
	return Number.isInteger(n) && n >= 1 && n <= 100;
}

/** "Copy all": one name and token per line, separated by a tab */
export function copyAllText(list: readonly { name: string; token: string }[]): string {
	return list.map((t) => `${t.name}\t${t.token}`).join('\n');
}
