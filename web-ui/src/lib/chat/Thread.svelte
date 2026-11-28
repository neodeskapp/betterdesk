<!--
  One conversation (A242): its header (given by the caller), the messages
  by day and run, who is typing, and the composer. It loads the newest
  page when it opens and older ones from the top ("Earlier messages"),
  marks what is in view read, keeps the scroll at the bottom while it is
  there, and takes files dropped anywhere on it. Used by the chat page,
  the chat panel and a remote session's chat sheet.

  For screen readers the log holds only the messages; "Earlier messages"
  and the state lines sit before it. The log is not a live region itself
  (it would read every button of a new message): a new message from
  someone else is announced once as "<sender>: <text>". While a page of
  history loads the log is aria-busy, and older messages say nothing.
  The typing line is plain text.
-->
<script lang="ts">
	import { tick, untrack, type Snippet } from 'svelte';
	import { formatDate } from '#lib/i18n/format.ts';
	import { t, tf } from '#lib/i18n/i18n.svelte.ts';
	import { can } from '#lib/state/session.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import Dialog from '#lib/ui/Dialog.svelte';
	import Menu from '#lib/ui/Menu.svelte';
	import MenuItem from '#lib/ui/MenuItem.svelte';
	import Time from '#lib/ui/Time.svelte';
	import { announce } from '#lib/ui/announce.svelte.ts';
	import { confirm } from '#lib/ui/dialogs.svelte.ts';
	import { loadTrail, type TrailStep } from './api.ts';
	import ChatEmoji from './ChatEmoji.svelte';
	import { chat } from './client.svelte.ts';
	import Composer from './Composer.svelte';
	import { EMOJI_GROUPS, QUICK } from './emoji.ts';
	import { dayKey, eventText, isMine, mayEdit, relativeDay, seen, startsRun, unreadIn } from './logic.ts';
	import MessageBubble from './MessageBubble.svelte';
	import type { Message } from './types.ts';

	/** How long a conversation may take to load before it says so */
	const SLOW_MS = 400;

	interface Props {
		conv: string;
		/** The name of who reads it: a computer or people */
		target: string;
		/** Writing is allowed (read-only accounts read) */
		writable?: boolean;
		header?: Snippet;
		/** Shown above the composer: who the message reaches */
		notice?: Snippet;
		empty?: Snippet;
		onmanagecanned: () => void;
		/** Focus the field when the thread opens */
		autofocus?: boolean;
		/** A message to show and mark: older pages load until it is there */
		focus?: string | null;
	}

	let {
		conv,
		target,
		writable = true,
		header,
		notice,
		empty,
		onmanagecanned,
		autofocus = false,
		focus = null
	}: Props = $props();

	const thread = $derived(chat.thread(conv));
	const messages = $derived(thread.messages);
	const me = $derived(chat.me);
	const meKey = $derived(me ? `u:${me.id}` : '');
	const mayTrail = $derived(can('audit.view'));
	/** My newest message that the server has: only it says "Seen" */
	const lastMineId = $derived(messages.findLast((m) => !m.pending && isMine(m, me))?.id ?? '');

	let scroller = $state<HTMLElement | null>(null);
	let field = $state<HTMLTextAreaElement | null>(null);
	let atBottom = true;
	let replyTo = $state<Message | null>(null);
	let editing = $state<Message | null>(null);
	let marked = $state<string | null>(null);
	let dropped = $state.raw<File[]>([]);
	let dragging = $state(false);

	// A conversation opens: its newest page, in view, at the bottom
	$effect(() => {
		const c = conv;
		untrack(() => {
			replyTo = null;
			editing = null;
			atBottom = true;
			chat.ensureHistory(c);
			chat.setVisible(c, true);
		});
		if (!untrack(() => focus)) void scrollToEnd();
		if (autofocus) void tick().then(() => field?.focus());
		return () => chat.setVisible(c, false);
	});

	// "Loading" only when it takes a while: a quick switch shows no line that comes and goes
	let slow = $state(false);
	$effect(() => {
		if (thread.loaded) return;
		const timer = setTimeout(() => (slow = true), SLOW_MS);
		return () => clearTimeout(timer);
	});

	// A found message: older pages until it is loaded (40 at most), then in view
	let seeking = $state<string | null>(null);
	let seekPages = 0;
	$effect(() => {
		const f = focus;
		untrack(() => {
			seeking = f;
			seekPages = 0;
			if (f) atBottom = false;
		});
	});
	$effect(() => {
		const want = seeking;
		void messages.length;
		void thread.loaded;
		if (!want) return;
		untrack(() => {
			if (!thread.loaded) return;
			if (messages.some((m) => m.id === want)) {
				seeking = null;
				void tick().then(() => showQuoted(want, 4000));
			} else if (thread.more && seekPages < 40) {
				seekPages++;
				earlier();
			} else {
				seeking = null;
			}
		});
	});

	// New messages: stay at the bottom when there, and mark them read
	$effect(() => {
		void messages.length;
		untrack(() => {
			if (atBottom) void scrollToEnd();
			markIfRead();
		});
	});

	function markIfRead() {
		if (!atBottom || document.visibilityState !== 'visible') return;
		if (unreadIn(chat.thread(conv), chat.me) > 0 || chat.unread(conv) > 0) chat.markRead(conv);
	}

	async function scrollToEnd() {
		await tick();
		requestAnimationFrame(() => {
			if (scroller) scroller.scrollTop = scroller.scrollHeight;
		});
	}

	function onscroll() {
		if (!scroller) return;
		atBottom = scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight < 40;
		if (atBottom) markIfRead();
	}

	/** A page of older messages was asked for and hasn't arrived */
	let loadingEarlier = $state(false);

	function earlier() {
		const first = messages.find((m) => !m.pending);
		if (!first) return;
		loadingEarlier = true;
		chat.history(conv, first.id);
	}

	// The page arrived (or there is nothing more): the log is no longer busy
	$effect(() => {
		void messages.length;
		void thread.more;
		untrack(() => (loadingEarlier = false));
	});

	/* New messages at the end, from someone else, are announced once.
	   Messages that came before the newest one we knew (history) are not. */
	let newestKnown = '';
	$effect(() => {
		const list = messages;
		const loaded = thread.loaded;
		void conv;
		untrack(() => {
			const newest = list.findLast((m) => !m.pending)?.id ?? '';
			if (!loaded) {
				newestKnown = '';
				return;
			}
			const from = newestKnown ? list.findIndex((m) => m.id === newestKnown) : -1;
			if (from >= 0) {
				const said = list
					.slice(from + 1)
					.filter((m) => !m.pending && !m.deleted && !isMine(m, me))
					.map(spoken)
					.filter(Boolean);
				if (said.length) announce(said.join(' '));
			}
			newestKnown = newest;
		});
	});

	/** What a screen reader hears for a new message */
	function spoken(m: Message): string {
		if (m.event) return eventText(m.event, t);
		const body = m.text.trim() || m.attachments.map((a) => a.name).join(', ');
		return body ? tf('chat.said', '{name}: {text}', { name: m.from.name || '?', text: body }) : '';
	}

	async function showQuoted(id: string, ms = 1600) {
		const el = scroller?.querySelector<HTMLElement>(`[data-id="${CSS.escape(id)}"]`);
		if (!el || !scroller) return;
		// Only the thread scrolls, not the page around it
		const box = scroller.getBoundingClientRect();
		const at = el.getBoundingClientRect();
		scroller.scrollTop += at.top - box.top - (box.height - at.height) / 2;
		marked = id;
		setTimeout(() => {
			if (marked === id) marked = null;
		}, ms);
	}

	function dayLabel(ts: number): string {
		const rel = relativeDay(ts);
		if (rel === 'today') return t('chat.today');
		if (rel === 'yesterday') return t('chat.yesterday');
		return formatDate(ts, { hour: undefined, minute: undefined });
	}

	/* ── Message actions ── */

	let picker = $state<{ msg: Message; anchor: HTMLElement } | null>(null);

	async function remove(msg: Message) {
		const ok = await confirm({
			title: t('chat.delete_title'),
			message: t('chat.delete_message'),
			confirmLabel: t('chat.delete'),
			danger: true
		});
		if (ok) chat.remove(msg.id);
	}

	let trail = $state<{ msg: Message; steps: TrailStep[] | null } | null>(null);
	async function showTrail(msg: Message) {
		trail = { msg, steps: null };
		try {
			const steps = await loadTrail(msg.id);
			if (trail?.msg.id === msg.id) trail = { msg, steps };
		} catch {
			if (trail?.msg.id === msg.id) trail = { msg, steps: [] };
		}
	}

	function retry(msg: Message) {
		chat.sendMessage(conv, msg.text);
		chat.threads = {
			...chat.threads,
			[conv]: { ...thread, messages: thread.messages.filter((m) => m.id !== msg.id) }
		};
	}

	/* ── Typing ── */

	let now = $state(Date.now());
	$effect(() => {
		const timer = setInterval(() => (now = Date.now()), 1000);
		return () => clearInterval(timer);
	});
	const typing = $derived.by(() => {
		const tp = chat.typing[conv];
		return tp && tp.until > now ? tp.from.name : '';
	});

	/* ── Dropping files ── */

	function ondragover(e: DragEvent) {
		if (!writable || !e.dataTransfer?.types.includes('Files')) return;
		e.preventDefault();
		dragging = true;
	}
	function ondrop(e: DragEvent) {
		dragging = false;
		if (!writable || !e.dataTransfer?.files.length) return;
		e.preventDefault();
		dropped = Array.from(e.dataTransfer.files);
	}
</script>

<section
	class={['thread', dragging && 'is-dragging']}
	aria-label={t('chat.conversation_with', { name: target })}
	{ondragover}
	ondragleave={() => (dragging = false)}
	{ondrop}
>
	{@render header?.()}
	<!-- svelte-ignore a11y_no_noninteractive_tabindex -->
	<div
		class="thread-scroll"
		bind:this={scroller}
		{onscroll}
		tabindex="0"
		role="group"
		aria-label={t('chat.messages')}
	>
		{#if thread.more}
			<div class="thread-more"><Button size="sm" onclick={earlier}>{t('chat.earlier')}</Button></div>
		{/if}
		{#if !thread.loaded && !slow}
			<!-- nothing yet -->
		{:else if !thread.loaded && chat.status !== 'open'}
			<p class="thread-state">{t('chat.connecting')}</p>
		{:else if !thread.loaded}
			<p class="thread-state">{t('common.loading')}</p>
		{:else if !messages.length}
			{#if empty}{@render empty()}{:else}<p class="thread-state">{t('chat.no_messages')}</p>{/if}
		{/if}
		<div class="thread-inner" role="log" aria-live="off" aria-busy={!thread.loaded || loadingEarlier}>
			{#each messages as m, i (m.id)}
				{@const prev = messages[i - 1]}
				{#if !prev || dayKey(prev.created_at) !== dayKey(m.created_at)}
					<p class="thread-day"><span>{dayLabel(m.created_at)}</span></p>
				{/if}
				<MessageBubble
					msg={m}
					mine={isMine(m, me)}
					first={startsRun(prev, m)}
					seen={m.id === lastMineId && seen(thread, m)}
					marked={marked === m.id}
					mayEdit={writable && mayEdit(m, me)}
					mayDelete={writable && (isMine(m, me) || chat.may.delete_any)}
					{mayTrail}
					{meKey}
					onreact={(msg, emoji, on) => chat.react(msg.id, emoji, on)}
					onpicker={(msg, anchor) => (picker = { msg, anchor })}
					onreply={(msg) => {
						editing = null;
						replyTo = msg;
						void tick().then(() => field?.focus());
					}}
					onedit={(msg) => {
						replyTo = null;
						editing = msg;
					}}
					ondelete={(msg) => void remove(msg)}
					ontrail={(msg) => void showTrail(msg)}
					onquote={(id) => void showQuoted(id)}
					onretry={retry}
				/>
			{/each}
		</div>
	</div>
	<p class="thread-typing">{typing ? t('chat.typing_name', { name: typing }) : ''}</p>
	{@render notice?.()}
	{#if writable}
		<Composer
			{conv}
			{target}
			{replyTo}
			{editing}
			{dropped}
			{onmanagecanned}
			bind:element={field}
			oncancel={() => {
				replyTo = null;
				editing = null;
				void tick().then(() => field?.focus());
			}}
		/>
	{:else}
		<p class="thread-readonly">{t('chat.read_only')}</p>
	{/if}
	{#if dragging}<div class="thread-drop" aria-hidden="true">{t('chat.drop_files')}</div>{/if}
</section>

{#if picker}
	<Menu
		open
		anchor={picker.anchor}
		label={t('chat.react')}
		class="chat-emoji-grid"
		onclose={(back) => {
			const a = picker?.anchor;
			picker = null;
			if (back) a?.focus();
		}}
	>
		{#each [...QUICK, ...EMOJI_GROUPS.flatMap((g) => g.emoji).filter((e) => !(QUICK as readonly string[]).includes(e))] as e (e)}
			<MenuItem
				onselect={() => {
					const p = picker;
					picker = null;
					if (p) chat.react(p.msg.id, e, true);
				}}><ChatEmoji emoji={e} size="large" /></MenuItem
			>
		{/each}
	</Menu>
{/if}

{#if trail}
	<Dialog open title={t('chat.edit_history')} onclose={() => (trail = null)}>
		{#if trail.steps === null}
			<p>{t('common.loading')}</p>
		{:else if !trail.steps.length}
			<p>{t('chat.no_history')}</p>
		{:else}
			<ol class="trail">
				{#each trail.steps as s, i (i)}
					<li>
						<p class="trail-head">
							{s.action === 'delete'
								? t('chat.trail_deleted', { name: s.actor.name })
								: t('chat.trail_edited', { name: s.actor.name })}
							<Time value={s.at} />
						</p>
						<p class="trail-text">{s.old_text || '-'}</p>
					</li>
				{/each}
			</ol>
		{/if}
	</Dialog>
{/if}

<style>
	.thread {
		position: relative;
		display: flex;
		flex: 1;
		flex-direction: column;
		min-height: 0;
		height: 100%;
	}
	.thread-scroll {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
		padding: var(--space-2) var(--space-4) var(--space-3);
		overscroll-behavior: contain;
	}
	.thread-scroll:focus-visible {
		outline-offset: -2px;
	}
	.thread-more {
		display: flex;
		justify-content: center;
		padding: var(--space-2) 0;
	}
	.thread-state,
	.thread-readonly {
		margin: var(--space-4) 0;
		color: var(--text-muted);
		text-align: center;
	}
	.thread-readonly {
		margin: 0;
		padding: var(--space-3);
		border-top: 1px solid var(--rule);
	}
	.thread-day {
		display: flex;
		align-items: center;
		gap: var(--space-3);
		margin: var(--space-4) 0 var(--space-2);
		color: var(--text-muted);
		font-size: var(--text-sm);
		font-weight: 600;
	}
	.thread-day::before,
	.thread-day::after {
		flex: 1;
		height: 1px;
		background: var(--rule);
		content: '';
	}
	.thread-typing {
		min-height: 1.4em;
		margin: 0;
		padding: 0 var(--space-4);
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	.thread-drop {
		position: absolute;
		inset: var(--space-2);
		display: flex;
		align-items: center;
		justify-content: center;
		border: 2px dashed var(--accent);
		border-radius: var(--radius);
		background: color-mix(in srgb, var(--accent-tint) 85%, transparent);
		color: var(--text);
		font-weight: 600;
		pointer-events: none;
	}
	.trail {
		margin: 0;
		padding-inline-start: var(--space-4);
	}
	.trail-head {
		display: flex;
		flex-wrap: wrap;
		gap: 0 var(--space-2);
		margin: 0;
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	.trail-text {
		margin: var(--space-1) 0 var(--space-3);
		white-space: pre-wrap;
		overflow-wrap: anywhere;
	}
	@media (max-width: 767px) {
		.thread-scroll {
			padding-inline: var(--space-3);
		}
	}
</style>
