/**
 * The organization pages' rules without a DOM: the slug a name suggests,
 * the shared address book (parse, edit as rows, check the JSON), open
 * invitations and the support packages that belong to an organization.
 */

import type { Contract, OrgInvitation } from '#lib/api/orgs.ts';
import { parseDate } from '#lib/i18n/format.ts';

/** "Harbor Logistics GmbH" → "harbor-logistics-gmbh" (at most 64 characters) */
export function slugify(name: string): string {
	return name
		.toLowerCase()
		.replace(/[^a-z0-9]+/g, '-')
		.replace(/^-|-$/g, '')
		.substring(0, 64);
}

/* ── Invitations ── */

export type InvitationState = 'open' | 'used' | 'expired';

/** Used, expired (at or after its time), or open */
export function invitationState(i: OrgInvitation, now = Date.now()): InvitationState {
	if (i.used_at && parseDate(i.used_at)) return 'used';
	const exp = parseDate(i.expires_at);
	return exp && exp.getTime() <= now ? 'expired' : 'open';
}

/** Invitations nobody used yet that have not expired */
export function openInvitations(list: readonly OrgInvitation[], now = Date.now()): number {
	return list.filter((i) => invitationState(i, now) === 'open').length;
}

/**
 * The link an invitation is sent as: the console's /invite page with the
 * code after #, so the code is not sent to the server with the page, nor
 * written into its logs or a Referer.
 */
export function invitationLink(origin: string, token: string): string {
	return `${origin}/invite#${encodeURIComponent(token)}`;
}

/**
 * The code in what someone pasted or opened: the part after # of an
 * invitation link, or the code itself; "" when there is none.
 */
export function invitationCode(text: string): string {
	let raw = text.trim();
	const hash = raw.indexOf('#');
	if (hash >= 0) raw = raw.slice(hash + 1);
	try {
		raw = decodeURIComponent(raw);
	} catch {
		return '';
	}
	return /^[0-9a-f]{64}$/i.test(raw) ? raw.toLowerCase() : '';
}

/* ── Support packages ── */

/** Contracts for this organization or one of its device groups */
export function orgContracts(
	all: readonly Contract[],
	orgId: string,
	groupGuids: readonly string[]
): Contract[] {
	const groups = new Set(groupGuids);
	return all.filter(
		(c) =>
			(c.target_type === 'org' && (c.target_key === orgId || c.org_id === orgId)) ||
			(c.target_type === 'device_group' && groups.has(c.target_key))
	);
}

/* ── Shared address book ── */

export interface BookPeer {
	id: string;
	alias: string;
	tags: string[];
	/** Fields of the contact the table does not show, written back as they came */
	extra?: Record<string, unknown>;
}

export interface Book {
	peers: BookPeer[];
	tags: string[];
	/** Fields of the book besides peers and tags, written back as they came */
	extra?: Record<string, unknown>;
}

/** The members of `obj` other than `known`; undefined when there are none */
function others(obj: Record<string, unknown>, known: readonly string[]): Record<string, unknown> | undefined {
	const rest = Object.fromEntries(Object.entries(obj).filter(([k]) => !known.includes(k)));
	return Object.keys(rest).length ? rest : undefined;
}

const splitTags = (text: string) =>
	text
		.split(',')
		.map((s) => s.trim())
		.filter(Boolean);

export { splitTags };

/** The stored book (JSON text, an object or nothing) as rows; throws on text that is not JSON */
export function parseBook(raw: unknown): Book {
	let data: unknown = raw;
	if (raw === null || raw === undefined || raw === '' || raw === '{}') return { peers: [], tags: [] };
	if (typeof raw === 'string') data = JSON.parse(raw);
	const obj =
		data && typeof data === 'object' && !Array.isArray(data) ? (data as Record<string, unknown>) : {};
	const peers = Array.isArray(obj.peers)
		? obj.peers.map((p) => {
				const peer = (p && typeof p === 'object' ? p : {}) as Record<string, unknown>;
				const out: BookPeer = {
					id: String(peer.id ?? '').trim(),
					alias: String(peer.alias ?? '').trim(),
					tags: Array.isArray(peer.tags) ? peer.tags.map(String) : []
				};
				const extra = others(peer, ['id', 'alias', 'tags']);
				if (extra) out.extra = extra;
				return out;
			})
		: [];
	const tags = Array.isArray(obj.tags) ? obj.tags.map(String) : [];
	const extra = others(obj, ['peers', 'tags']);
	return extra ? { peers, tags, extra } : { peers, tags };
}

/**
 * What the server stores: peers without an ID are dropped, empty alias and
 * tags left out, the fields the table does not show kept as they came
 */
export function bookPayload(
	book: Book
): Record<string, unknown> & { peers: Record<string, unknown>[]; tags: string[] } {
	const peers = book.peers
		.map((p) => {
			const entry: Record<string, unknown> = { ...p.extra, id: p.id.trim() };
			if (p.alias.trim()) entry.alias = p.alias.trim();
			if (p.tags.length) entry.tags = p.tags;
			return entry;
		})
		.filter((p) => p.id);
	return { ...book.extra, peers, tags: book.tags };
}

export type BookError = 'invalid_json' | 'peers_array' | 'tags_array';

/** The JSON editor's text as a book, or why it can't be one */
export function checkBookJson(text: string): { book: Book; payload: unknown } | { error: BookError } {
	let parsed: unknown;
	try {
		parsed = JSON.parse(text.trim() || '{}');
	} catch {
		return { error: 'invalid_json' };
	}
	if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return { error: 'invalid_json' };
	const obj = parsed as Record<string, unknown>;
	if (obj.peers !== undefined && !Array.isArray(obj.peers)) return { error: 'peers_array' };
	if (obj.tags !== undefined && !Array.isArray(obj.tags)) return { error: 'tags_array' };
	return { book: parseBook(obj), payload: obj };
}

export const BOOK_ERROR_KEYS: Record<BookError, string> = {
	invalid_json: 'organizations.address_book_invalid_json',
	peers_array: 'organizations.address_book_peers_array_required',
	tags_array: 'organizations.address_book_tags_array_required'
};

/** Pretty JSON of a book for the editor */
export function bookJson(book: Book): string {
	return JSON.stringify(bookPayload(book), null, 2);
}

/** Add the organization's devices that are not in the book yet; returns the new book and how many were added */
export function importDevices(
	book: Book,
	devices: readonly { device_id: string; department?: string; location?: string; building?: string }[]
): { book: Book; added: number } {
	const seen = new Set(book.peers.map((p) => p.id.trim()).filter(Boolean));
	const peers = [...book.peers];
	let added = 0;
	for (const d of devices) {
		const id = String(d.device_id || '').trim();
		if (!id || seen.has(id)) continue;
		peers.push({ id, alias: d.department || d.location || d.building || id, tags: [] });
		seen.add(id);
		added++;
	}
	return { book: { ...book, peers }, added };
}
