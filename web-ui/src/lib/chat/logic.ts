/**
 * Chat rules without state (A242): keeping a conversation's messages in
 * order as frames arrive, who has read what, grouping messages into runs
 * and days, and the words for lines of the system. client.svelte.ts and
 * the components use these; tests/unit/chat cover them.
 */

import type { LastMessage, Message, Party, ReadMark, SystemEvent } from './types.ts';

/** One conversation as the browser holds it */
export interface Thread {
	messages: Message[];
	/** The newest page arrived */
	loaded: boolean;
	/** Older messages are on the server */
	more: boolean;
	/** How far each reader read: "u:7" or "d:<id>" to a message id */
	reads: Record<string, number>;
}

export const emptyThread = (): Thread => ({ messages: [], loaded: false, more: false, reads: {} });

/** Most messages kept per conversation */
export const MAX_KEPT = 1000;

const num = (id: string): number => {
	const n = Number(id);
	return Number.isFinite(n) ? n : Number.MAX_SAFE_INTEGER;
};

/** Messages in the order they were stored: by id, pending ones last */
export function byOrder(a: Message, b: Message): number {
	if (!!a.pending !== !!b.pending) return a.pending ? 1 : -1;
	if (a.pending) return a.created_at - b.created_at;
	return num(a.id) - num(b.id);
}

/**
 * A message that arrived (new or changed) put into a thread: it replaces
 * the one with its id, or the pending one with its client id; else it is
 * added.
 */
export function withMessage(thread: Thread, msg: Message): Thread {
	const list = thread.messages.slice();
	const at = list.findIndex(
		(m) => m.id === msg.id || (!!msg.client_id && m.pending !== undefined && m.client_id === msg.client_id)
	);
	if (at >= 0) list[at] = msg;
	else list.push(msg);
	list.sort(byOrder);
	if (list.length > MAX_KEPT) list.splice(0, list.length - MAX_KEPT);
	return { ...thread, messages: list };
}

/** A page of history put into a thread: older pages go in front, the newest replaces what is known */
export function withHistory(
	thread: Thread,
	messages: Message[],
	reads: ReadMark[],
	more: boolean,
	before: string
): Thread {
	const known = new Map(thread.messages.map((m) => [m.id, m]));
	for (const m of messages) known.set(m.id, m);
	// Pending messages stay until the server has them
	const list = [...known.values()].filter(
		(m) => !m.pending || !messages.some((x) => x.client_id && x.client_id === m.client_id)
	);
	list.sort(byOrder);
	const marks = { ...thread.reads };
	for (const r of reads) marks[readerKey(r.reader)] = Math.max(marks[readerKey(r.reader)] ?? 0, num(r.up_to));
	// An older page says whether there is more before it; the newest page
	// whether there is anything before it
	void before;
	return { messages: list, loaded: true, more, reads: marks };
}

export const readerKey = (p: { kind: string; id: string }): string => `${p.kind[0]}:${p.id}`;

/** A read mark moved forward */
export function withRead(thread: Thread, reader: { kind: string; id: string }, upTo: string): Thread {
	const key = readerKey(reader);
	const n = num(upTo);
	if ((thread.reads[key] ?? 0) >= n) return thread;
	return { ...thread, reads: { ...thread.reads, [key]: n } };
}

/** The message of a sender who is me */
export function isMine(m: Pick<Message, 'from'>, me: { id: number } | null): boolean {
	return !!me && m.from.kind === 'user' && m.from.id === String(me.id);
}

/**
 * Whether someone other than the sender has read a message: the device
 * for a message to a computer, any other person in a conversation of
 * people.
 */
export function seen(thread: Thread, msg: Message): boolean {
	if (msg.pending) return false;
	const id = num(msg.id);
	const sender = readerKey(msg.from);
	return Object.entries(thread.reads).some(([who, upTo]) => who !== sender && upTo >= id);
}

/** Messages from others after my read mark: what is unread for me */
export function unreadIn(thread: Thread, me: { id: number } | null): number {
	if (!me) return 0;
	const mark = thread.reads[`u:${me.id}`] ?? 0;
	return thread.messages.filter(
		(m) => !m.pending && !m.deleted && m.from.kind !== 'system' && !isMine(m, me) && num(m.id) > mark
	).length;
}

/** The newest message id of a thread that the server has stored */
export function newestId(thread: Thread): string | null {
	for (let i = thread.messages.length - 1; i >= 0; i--) {
		const m = thread.messages[i];
		if (m && !m.pending) return m.id;
	}
	return null;
}

/** How long messages of one sender stay one run (ms) */
export const RUN_GAP = 5 * 60 * 1000;

/** Whether a message starts a new run: another sender, a line of the system, a new day or a pause */
export function startsRun(prev: Message | undefined, m: Message): boolean {
	if (!prev) return true;
	if (prev.from.kind === 'system' || m.from.kind === 'system') return true;
	if (prev.from.kind !== m.from.kind || prev.from.id !== m.from.id || prev.from.name !== m.from.name)
		return true;
	if (dayKey(prev.created_at) !== dayKey(m.created_at)) return true;
	return m.created_at - prev.created_at > RUN_GAP;
}

export function dayKey(ts: number): string {
	const d = new Date(ts);
	return `${d.getFullYear()}-${d.getMonth()}-${d.getDate()}`;
}

/** "today", "yesterday" or null for an older day */
export function relativeDay(ts: number, now = Date.now()): 'today' | 'yesterday' | null {
	if (dayKey(ts) === dayKey(now)) return 'today';
	const y = new Date(now);
	y.setDate(y.getDate() - 1);
	if (dayKey(ts) === dayKey(y.getTime())) return 'yesterday';
	return null;
}

type Translate = (key: string, vars?: Record<string, string | number>) => string;

/** The words of a line of the system */
export function eventText(e: SystemEvent | undefined, t: Translate): string {
	if (!e) return '';
	const by = e.by || '?';
	switch (e.event) {
		case 'taken':
			return t('chat.event_taken', { by });
		case 'assigned':
			return t('chat.event_assigned', { by, to: e.to || '?' });
		case 'unassigned':
			return t('chat.event_unassigned', { by });
		case 'closed':
			return e.note ? t('chat.event_closed_note', { by, note: e.note }) : t('chat.event_closed', { by });
		case 'reopened':
			return t('chat.event_reopened', { by });
	}
	return '';
}

/** The line a conversation's row shows of its newest message */
export function lastLine(last: LastMessage | null, t: Translate): string {
	if (!last) return '';
	if (last.deleted) return t('chat.message_deleted');
	if (last.event) {
		try {
			return eventText(JSON.parse(last.event) as SystemEvent, t);
		} catch {
			return '';
		}
	}
	const text = last.text || (last.files ? t('chat.files_count', { count: last.files }) : '');
	return last.from_kind === 'user' && last.from_name ? `${last.from_name}: ${text}` : text;
}

/** Text with http(s) links as separate parts; anything else stays text */
export function linkParts(text: string): { text: string; href?: string }[] {
	const out: { text: string; href?: string }[] = [];
	const re = /(https?:\/\/[^\s<>"']+[^\s<>"'.,;:!?)\]])/gi;
	let last = 0;
	for (const m of text.matchAll(re)) {
		const at = m.index ?? 0;
		if (at > last) out.push({ text: text.slice(last, at) });
		let href: string | undefined;
		try {
			href = new URL(m[0]).href;
		} catch {
			href = undefined;
		}
		out.push(href ? { text: m[0], href } : { text: m[0] });
		last = at + m[0].length;
	}
	if (last < text.length) out.push({ text: text.slice(last) });
	return out;
}

let seq = 0;
/** A client id for a message on its way */
export function clientId(): string {
	seq += 1;
	return `c${Date.now().toString(36)}${seq.toString(36)}${Math.random().toString(36).slice(2, 6)}`;
}

/** A message shown while it is on its way */
export function pendingMessage(
	conv: string,
	me: { id: number; name: string },
	text: string,
	cid: string
): Message {
	return {
		id: cid,
		conversation_id: conv,
		from: { kind: 'user', id: String(me.id), name: me.name },
		text,
		reply_to: null,
		attachments: [],
		created_at: Date.now(),
		edited_at: null,
		deleted: false,
		reactions: {},
		client_id: cid,
		pending: 'sending'
	};
}

/** Whether a message may still be edited by its sender (24 hours, A242 g) */
export const EDIT_WINDOW = 24 * 60 * 60 * 1000;
export function mayEdit(m: Message, me: { id: number } | null, now = Date.now()): boolean {
	return isMine(m, me) && !m.deleted && !m.pending && now - m.created_at < EDIT_WINDOW;
}

/** A picture the page may show inline */
export function isPicture(type: string): boolean {
	return ['image/png', 'image/jpeg', 'image/gif', 'image/webp'].includes(type);
}

/** Names of who reacted, for the tooltip */
export function reactors(who: Party[]): string {
	return who.map((p) => p.name).join(', ');
}

/**
 * The part of a found message around the first match of q (any case):
 * radius characters on each side, with an ellipsis where the text is cut.
 * A text without the match shows its start.
 */
export function snippet(
	text: string,
	q: string,
	radius = 40
): { before: string; match: string; after: string } {
	const at = q ? text.toLowerCase().indexOf(q.toLowerCase()) : -1;
	if (at < 0) {
		return { before: '', match: '', after: text.slice(0, radius) + (text.length > radius ? '…' : '') };
	}
	const end = at + q.length;
	const from = Math.max(0, at - radius);
	const to = Math.min(text.length, end + radius);
	return {
		before: (from > 0 ? '…' : '') + text.slice(from, at),
		match: text.slice(at, end),
		after: text.slice(end, to) + (to < text.length ? '…' : '')
	};
}
