/**
 * API tokens without a DOM: who may make them, the expiry choices, the
 * allow list as typed, a token's status and the filter. Tested in
 * tests/unit/people/api-tokens.test.ts.
 */

import type { ApiToken, Made } from '#lib/api/apiTokens.ts';
import { plural } from '#lib/i18n/i18n.svelte.ts';

/** The roles that make and revoke API tokens (the server's admin roles) */
export const TOKEN_ROLES: readonly string[] = ['super_admin', 'admin', 'global_admin', 'server_admin'];

export function mayMakeTokens(role: string | null | undefined): boolean {
	return !!role && TOKEN_ROLES.includes(role);
}

/** Days a new token lasts; the server takes at most 366 */
export const EXPIRY_DAYS = [7, 30, 90, 365] as const;

/** The date `days` from now, as the server takes it (the token ends at the end of that day, UTC) */
export function expiryDate(days: number, now: Date = new Date()): string {
	const d = new Date(now.getTime() + days * 86_400_000);
	return d.toISOString().slice(0, 10);
}

/** The last date the server takes for an expiry */
export function latestExpiry(now: Date = new Date()): string {
	return expiryDate(365, now);
}

/** Entries of the allow list as typed: one per line or separated by commas or spaces */
export function parseAllowList(text: string): string[] {
	return text
		.split(/[\s,;]+/)
		.map((s) => s.trim())
		.filter(Boolean);
}

const IPV4 = /^(\d{1,3})(\.\d{1,3}){3}$/;

/**
 * Entries the server will refuse, so the form can say so before sending:
 * not an IPv4 or IPv6 address, with or without a network size.
 */
export function badAllowEntries(entries: readonly string[]): string[] {
	return entries.filter((e) => {
		const [addr = '', bits, extra] = e.split('/');
		if (extra !== undefined) return true;
		if (IPV4.test(addr)) {
			const ok = addr.split('.').every((n) => Number(n) <= 255);
			return !ok || (bits !== undefined && !(/^\d{1,2}$/.test(bits) && Number(bits) <= 32));
		}
		if (addr.includes(':') && /^[0-9a-fA-F:.]+$/.test(addr)) {
			return bits !== undefined && !(/^\d{1,3}$/.test(bits) && Number(bits) <= 128);
		}
		return true;
	});
}

export type ApiTokenFilter = 'active' | 'all';

/** Active tokens first: the newest first within each state, as the server sends them */
export function filterApiTokens(
	list: readonly ApiToken[],
	filter: ApiTokenFilter,
	search: string
): ApiToken[] {
	const q = search.trim().toLowerCase();
	return list.filter((tok) => {
		if (filter === 'active' && tok.state !== 'active') return false;
		if (!q) return true;
		return [tok.name, tok.hint, tok.username, ...tok.permissions].some((v) => v.toLowerCase().includes(q));
	});
}

export function stateTone(state: ApiToken['state']): 'good' | 'neutral' {
	return state === 'active' ? 'good' : 'neutral';
}

/** The commands that sign the command line in on this console, without the token */
export function loginCommand(origin: string): string {
	return `neodesk login --url ${origin}`;
}

type Translate = (key: string, params?: Record<string, string | number>) => string;

/** The kinds of what a token makes, in the order the console names them */
const MADE_KINDS: readonly (keyof Made)[] = [
	'guest_links',
	'device_tokens',
	'support_sessions',
	'invites',
	'pairing_codes',
	'share_links',
	'organization_invitations'
];

/** What a token made, as "2 guest links, 1 device token"; "" for nothing */
export function madeList(made: Made, t: Translate): string {
	return MADE_KINDS.filter((kind) => made[kind] > 0)
		.map((kind) =>
			plural(made[kind], { one: t(`api_tokens.made_${kind}_one`), other: t(`api_tokens.made_${kind}_many`) })
		)
		.join(', ');
}

/** The revoke dialog's text: what revoking does, and what ends with the token when it made something */
export function revokeMessage(tok: ApiToken, t: Translate): string {
	const message = t('api_tokens.revoke_confirm', { name: tok.name });
	const list = madeList(tok.ends_with_it, t);
	return list ? `${message} ${t('api_tokens.ends_with_it', { list })}` : message;
}
