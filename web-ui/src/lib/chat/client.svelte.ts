/**
 * The console's chat in this tab (A242): one socket of version 2
 * (/ws/chat-operator/panel?v=2) shared by the chat page, the chat panel
 * and the badges, the conversations' messages as they arrive, read marks,
 * typing, presence, unread counts, and the notifications (browser, sound,
 * tab title). The shell starts it for accounts with Support chat or Team
 * chat; the components read it and call its methods.
 *
 * Unread counts come from GET /api/chat/summary: at the start, after the
 * socket opened again and shortly after any message or read mark, so
 * every tab and session of an account shows the same numbers (read marks
 * are per account on the server).
 */

import { socket, type LiveSocket } from '#lib/state/live.ts';
import * as api from './api.ts';
import {
	clientId,
	emptyThread,
	isMine,
	pendingMessage,
	withHistory,
	withMessage,
	withRead,
	type Thread
} from './logic.ts';
import type { AccountSettings, Conversation, Message, Party, ReadMark, Summary } from './types.ts';

export type ChatStatus = 'idle' | 'connecting' | 'open' | 'closed';

/** What the chat calls back into the page that shows it */
export interface ChatHooks {
	/** Words a notification: title and body */
	notification?: (msg: Message) => { title: string; body: string } | null;
	/** Opens a conversation from a notification click */
	open?: (conversation: string) => void;
}

/** A message sent from a session of the remote viewer (session-bridge.ts) */
export type SessionSink = (text: string) => void;

class ChatClient {
	status = $state<ChatStatus>('idle');
	me = $state<{ id: number; name: string; username: string } | null>(null);
	may = $state({ support: false, team: false, assign: false, delete_any: false });
	threads = $state.raw<Record<string, Thread>>({});
	/** Who is typing where, until when (ms) */
	typing = $state.raw<Record<string, { from: Party; until: number }>>({});
	/** Colleagues' status by account id */
	presence = $state.raw<Record<number, string>>({});
	/** Devices whose chat window is connected, as the server last said */
	deviceChat = $state.raw<Record<string, boolean>>({});
	summary = $state.raw<Summary>({});
	settings = $state<AccountSettings>({ browser: true, sound: false, mail: true });
	/** Conversations the person has open and in view now (page or panel) */
	visible = $state.raw<string[]>([]);
	/** Bumped when the lists should load again (conversation changes, new messages) */
	listVersion = $state(0);
	/** A notice for the person: a colleague took one of their conversations */
	notices = $state.raw<{ code: string; conversation_id: string; by: string; at: number }[]>([]);
	/** Conversations' facts as last loaded, so a conversation opens with its header at once */
	details = $state.raw<Record<string, api.ConversationDetail>>({});

	private sock: LiveSocket | null = null;
	private hooks: ChatHooks = {};
	private summaryTimer: ReturnType<typeof setTimeout> | null = null;
	private sinks = new Map<string, Set<SessionSink>>();
	/** Facts on their way, one request per conversation */
	private detailLoads = new Map<string, Promise<api.ConversationDetail>>();
	/** When a conversation's newest page was asked for and hasn't come yet */
	private historyAsked = new Map<string, number>();

	/** Unread support conversations of mine or nobody's, and unread team messages */
	get supportUnread(): number {
		return this.summary.support?.unread_conversations ?? 0;
	}
	get teamUnread(): number {
		return this.summary.team?.unread ?? 0;
	}
	get total(): number {
		return this.supportUnread + this.teamUnread;
	}

	/** Unread messages of one conversation, as the server last counted them */
	unread(conv: string): number {
		return this.summary.support?.unread?.[conv] ?? this.summary.team?.by_conversation?.[conv] ?? 0;
	}

	thread(conv: string): Thread {
		return this.threads[conv] ?? emptyThread();
	}

	private setThread(conv: string, t: Thread) {
		this.threads = { ...this.threads, [conv]: t };
	}

	start(hooks: ChatHooks = {}): void {
		this.hooks = hooks;
		if (this.sock) return;
		this.status = 'connecting';
		this.sock = socket('/ws/chat-operator/panel?v=2', {
			onmessage: (data) => this.handle(data),
			onstatus: (s) => {
				this.status = s;
				if (s === 'open') {
					this.refreshSummary();
					// Back after a break: the conversations in view again
					for (const c of this.visible) this.history(c);
				}
			},
			// Notifications need the socket while the tab is in the background
			hiddenGrace: 24 * 60 * 60 * 1000
		});
		void api.loadMySettings().then(
			(s) => (this.settings = s),
			() => undefined
		);
	}

	stop(): void {
		this.sock?.close();
		this.sock = null;
		this.status = 'idle';
		this.threads = {};
		this.details = {};
		this.detailLoads.clear();
		this.historyAsked.clear();
		this.summary = {};
		this.me = null;
	}

	send(frame: Record<string, unknown>): boolean {
		return this.sock?.send(frame) ?? false;
	}

	/** Loads a conversation's newest page, or the page before a message */
	history(conv: string, before?: string): boolean {
		return this.send({ type: 'history', conversation_id: conv, before: before ?? '0', limit: 50 });
	}

	/** Loads a conversation's newest page unless it is here or on its way */
	ensureHistory(conv: string): void {
		if (this.thread(conv).loaded) return;
		const asked = this.historyAsked.get(conv);
		if (asked && Date.now() - asked < 5000) return;
		if (this.history(conv)) this.historyAsked.set(conv, Date.now());
	}

	/**
	 * Loads a conversation's facts. A request on its way is shared, unless
	 * fresh: after a change, a new one whose answer wins over older ones.
	 */
	loadDetail(conv: string, fresh = false): Promise<api.ConversationDetail> {
		const running = this.detailLoads.get(conv);
		if (running && !fresh) return running;
		const load: Promise<api.ConversationDetail> = api
			.loadConversation(conv)
			.then((d) => {
				if (this.detailLoads.get(conv) === load) this.setDetail(conv, d);
				return d;
			})
			.finally(() => {
				if (this.detailLoads.get(conv) === load) this.detailLoads.delete(conv);
			});
		this.detailLoads.set(conv, load);
		return load;
	}

	setDetail(conv: string, d: api.ConversationDetail): void {
		this.details = { ...this.details, [conv]: d };
	}

	/** A conversation about to open (its row is pointed at or has focus): its messages and facts ahead of the click */
	preload(conv: string): void {
		this.ensureHistory(conv);
		if (!this.details[conv]) this.loadDetail(conv).catch(() => undefined);
	}

	/** Sends a message; it shows at once and is replaced when the server has it */
	sendMessage(conv: string, text: string, opts: { replyTo?: string; attachments?: string[] } = {}): boolean {
		if (!this.me) return false;
		const cid = clientId();
		const ok = this.send({
			type: 'send',
			conversation_id: conv,
			text,
			reply_to: opts.replyTo ?? '',
			attachments: opts.attachments ?? [],
			client_id: cid
		});
		const pending = pendingMessage(conv, this.me, text, cid);
		if (!ok) pending.pending = 'failed';
		this.setThread(conv, withMessage(this.thread(conv), pending));
		return ok;
	}

	edit(id: string, text: string): boolean {
		return this.send({ type: 'edit', message_id: id, text });
	}

	remove(id: string): boolean {
		return this.send({ type: 'delete', message_id: id });
	}

	react(id: string, emoji: string, on: boolean): boolean {
		return this.send({ type: 'react', message_id: id, emoji, on });
	}

	private lastTyping = 0;
	typingIn(conv: string): void {
		const now = Date.now();
		if (now - this.lastTyping < 2500) return;
		this.lastTyping = now;
		this.send({ type: 'typing', conversation_id: conv });
	}

	setPresence(status: string): void {
		this.send({ type: 'presence', status });
	}

	/** Marks a conversation read up to its newest message */
	markRead(conv: string): void {
		this.send({ type: 'read', conversation_id: conv });
	}

	/** A conversation came into view (true) or left it */
	setVisible(conv: string, on: boolean): void {
		const now = this.visible.filter((c) => c !== conv);
		if (on) now.push(conv);
		this.visible = now;
	}

	/** Messages the remote viewer's session with a device should pass into the session */
	addSessionSink(device: string, sink: SessionSink): () => void {
		let set = this.sinks.get(device);
		if (!set) {
			// eslint-disable-next-line svelte/prefer-svelte-reactivity -- callbacks, nothing renders from them
			set = new Set();
			this.sinks.set(device, set);
		}
		set.add(sink);
		return () => set?.delete(sink);
	}

	refreshSummary(now = false): void {
		if (this.summaryTimer) clearTimeout(this.summaryTimer);
		this.summaryTimer = setTimeout(
			() => {
				this.summaryTimer = null;
				api.loadSummary().then(
					(s) => (this.summary = s),
					() => undefined
				);
			},
			now ? 0 : 600
		);
	}

	private handle(data: unknown): void {
		if (!data || typeof data !== 'object') return;
		const f = data as Record<string, unknown>;
		switch (f.type) {
			case 'welcome': {
				const me = f.me as { id: number; name: string; username: string } | undefined;
				if (me) this.me = me;
				const may = f.may as Partial<ChatClient['may']> | undefined;
				if (may)
					this.may = {
						support: !!may.support,
						team: !!may.team,
						assign: !!may.assign,
						delete_any: !!may.delete_any
					};
				return;
			}
			case 'history': {
				const conv = String(f.conversation_id ?? '');
				const messages = (Array.isArray(f.messages) ? f.messages : []) as Message[];
				const reads = (Array.isArray(f.reads) ? f.reads : []) as ReadMark[];
				if (String(f.before ?? '0') === '0') this.historyAsked.delete(conv);
				this.setThread(
					conv,
					withHistory(this.thread(conv), messages, reads, f.more === true, String(f.before ?? '0'))
				);
				return;
			}
			case 'message.new': {
				const msg = f.message as Message;
				if (!msg?.conversation_id) return;
				const conv = msg.conversation_id;
				this.setThread(conv, withMessage(this.thread(conv), msg));
				const typing = this.typing[conv];
				if (typing && typing.from.id === msg.from.id) {
					this.typing = Object.fromEntries(Object.entries(this.typing).filter(([c]) => c !== conv));
				}
				if (!isMine(msg, this.me) && msg.from.kind !== 'system') this.arrived(msg);
				if (isMine(msg, this.me) && this.sinks.has(conv)) {
					// My message to a device in a session: into the session too, as text
					const lines = [msg.text, ...msg.attachments.map((a) => fileInSession(a.name))].filter(Boolean);
					for (const sink of this.sinks.get(conv) ?? []) for (const line of lines) sink(line);
				}
				this.listVersion++;
				this.refreshSummary();
				return;
			}
			case 'message.updated': {
				const msg = f.message as Message;
				if (msg?.conversation_id)
					this.setThread(msg.conversation_id, withMessage(this.thread(msg.conversation_id), msg));
				return;
			}
			case 'read': {
				const conv = String(f.conversation_id ?? '');
				const reader = f.reader as { kind: string; id: string } | undefined;
				if (!conv || !reader) return;
				this.setThread(conv, withRead(this.thread(conv), reader, String(f.up_to ?? '0')));
				if (reader.kind === 'user' && this.me && reader.id === String(this.me.id)) {
					// I read it here or in another tab: the lists and badges count again
					this.listVersion++;
					this.refreshSummary();
				}
				return;
			}
			case 'typing': {
				const conv = String(f.conversation_id ?? '');
				const from = f.from as Party | undefined;
				if (!conv || !from) return;
				this.typing = { ...this.typing, [conv]: { from, until: Date.now() + 4000 } };
				return;
			}
			case 'presence': {
				const id = Number(f.user_id);
				if (id) this.presence = { ...this.presence, [id]: String(f.status ?? 'offline') };
				return;
			}
			case 'device': {
				const id = String(f.device_id ?? '');
				if (id) this.deviceChat = { ...this.deviceChat, [id]: f.chat_connected === true };
				return;
			}
			case 'conversation.updated': {
				const c = f.conversation as Conversation | undefined;
				if (c) this.conversationChanged(c);
				this.listVersion++;
				this.refreshSummary();
				return;
			}
			case 'notice': {
				this.notices = [
					...this.notices,
					{
						code: String(f.code ?? ''),
						conversation_id: String(f.conversation_id ?? ''),
						by: String(f.by ?? ''),
						at: Date.now()
					}
				].slice(-5);
				return;
			}
			case 'error': {
				const cid = String(f.client_id ?? '');
				if (!cid) return;
				for (const [conv, t] of Object.entries(this.threads)) {
					const at = t.messages.findIndex((m) => m.client_id === cid && m.pending);
					if (at < 0) continue;
					const list = t.messages.slice();
					const m = list[at];
					if (m) list[at] = { ...m, pending: 'failed' };
					this.setThread(conv, { ...t, messages: list });
				}
				return;
			}
		}
	}

	/** Listeners for a conversation's state (the open thread's header) */
	private convListeners = new Set<(c: Conversation) => void>();
	onConversation(fn: (c: Conversation) => void): () => void {
		this.convListeners.add(fn);
		return () => this.convListeners.delete(fn);
	}
	private conversationChanged(c: Conversation) {
		for (const fn of this.convListeners) fn(c);
	}

	/** A message from someone else: notify unless it is in view in a visible tab */
	private arrived(msg: Message): void {
		const inView =
			this.visible.includes(msg.conversation_id) &&
			document.visibilityState === 'visible' &&
			document.hasFocus();
		if (inView) return;
		if (this.settings.sound) beep();
		if (
			!this.settings.browser ||
			typeof Notification === 'undefined' ||
			Notification.permission !== 'granted'
		)
			return;
		const words = this.hooks.notification?.(msg);
		if (!words) return;
		try {
			const n = new Notification(words.title, {
				body: words.body,
				icon: '/branding/favicon.svg',
				tag: `chat-${msg.conversation_id}`
			});
			n.onclick = () => {
				window.focus();
				this.hooks.open?.(msg.conversation_id);
				n.close();
			};
		} catch {
			/* notifications blocked */
		}
	}
}

/** The line a file becomes in a session, whose client takes text only (A242 f) */
export function fileInSession(name: string): string {
	return `File: ${name}`;
}

/** A short, quiet two-tone sound, made in the page (no file to load) */
function beep(): void {
	try {
		const Ctx =
			window.AudioContext ??
			(window as unknown as { webkitAudioContext?: typeof AudioContext }).webkitAudioContext;
		if (!Ctx) return;
		const ctx = new Ctx();
		const gain = ctx.createGain();
		gain.gain.value = 0.06;
		gain.connect(ctx.destination);
		[880, 660].forEach((freq, i) => {
			const o = ctx.createOscillator();
			o.type = 'sine';
			o.frequency.value = freq;
			o.connect(gain);
			o.start(ctx.currentTime + i * 0.12);
			o.stop(ctx.currentTime + i * 0.12 + 0.1);
		});
		setTimeout(() => void ctx.close(), 600);
	} catch {
		/* no audio */
	}
}

export const chat = new ChatClient();
