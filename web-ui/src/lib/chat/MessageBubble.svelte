<!--
  One message of a conversation (A242). Mine on the end side, everyone
  else's on the start side; a line of the system in the middle, muted.
  The first message of someone else's run (one sender, a few minutes)
  names its sender; every bubble ends in its time. A bubble
  holds the quoted message it answers, its pictures and files, the text
  (links open in a new tab and are shown as they are, never previewed),
  "edited", or "Message deleted" in its place. Under it the reactions,
  and on my newest message whether it was seen. The actions (react,
  reply, edit, delete, copy, the edit history) show on hover and focus,
  and stay on touch screens. Everything from a message is text, never
  markup.
-->
<script lang="ts">
	import { formatBytes } from '#lib/i18n/format.ts';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import Avatar from '#lib/ui/Avatar.svelte';
	import Icon from '#lib/ui/Icon.svelte';
	import IconButton from '#lib/ui/IconButton.svelte';
	import MenuButton from '#lib/ui/MenuButton.svelte';
	import MenuItem from '#lib/ui/MenuItem.svelte';
	import Time from '#lib/ui/Time.svelte';
	import ChatEmoji from './ChatEmoji.svelte';
	import { QUICK } from './emoji.ts';
	import { eventText, isPicture, linkParts, reactors } from './logic.ts';
	import type { Message } from './types.ts';

	interface Props {
		msg: Message;
		mine: boolean;
		/** The first message of a run: shows the sender */
		first: boolean;
		/** Seen by someone else (my newest message only) */
		seen?: boolean;
		/** Marked for a moment (opened from a quote or a search) */
		marked?: boolean;
		mayEdit: boolean;
		mayDelete: boolean;
		mayTrail: boolean;
		/** Who I am, for my own reactions */
		meKey: string;
		onreact: (msg: Message, emoji: string, on: boolean) => void;
		onpicker: (msg: Message, anchor: HTMLElement) => void;
		onreply: (msg: Message) => void;
		onedit: (msg: Message) => void;
		ondelete: (msg: Message) => void;
		ontrail: (msg: Message) => void;
		onquote: (id: string) => void;
		onretry: (msg: Message) => void;
	}

	let {
		msg,
		mine,
		first,
		seen = false,
		marked = false,
		mayEdit,
		mayDelete,
		mayTrail,
		meKey,
		onreact,
		onpicker,
		onreply,
		onedit,
		ondelete,
		ontrail,
		onquote,
		onretry
	}: Props = $props();

	const parts = $derived(linkParts(msg.text));
	const pictures = $derived(msg.attachments.filter((a) => isPicture(a.type)));
	const files = $derived(msg.attachments.filter((a) => !isPicture(a.type)));
	const reactions = $derived(Object.entries(msg.reactions).filter(([, who]) => who.length));
	let pickerBtn = $state<HTMLButtonElement | null>(null);

	function reactedByMe(who: { kind: string; id: string }[]): boolean {
		return who.some((p) => `${p.kind[0]}:${p.id}` === meKey);
	}

	async function copy() {
		try {
			await navigator.clipboard.writeText(msg.text);
		} catch {
			/* no clipboard */
		}
	}
</script>

{#if msg.from.kind === 'system'}
	<p class="chat-system" data-id={msg.id}>
		<span class="chat-system-text"><span>{eventText(msg.event, t)}</span><Time value={msg.created_at} /></span
		>
	</p>
{:else}
	<div
		class={['chat-msg', mine ? 'is-mine' : 'is-theirs', first && 'is-first', marked && 'is-marked']}
		data-id={msg.id}
		role="group"
		aria-label={t('chat.message_from', { name: msg.from.name || '?' })}
	>
		{#if first && !mine}
			<div class="chat-msg-head">
				{#if msg.from.kind === 'user'}
					<Avatar user={{ id: Number(msg.from.id) || 0, username: msg.from.name }} size="sm" />
				{:else}
					<span class="chat-device-mark" aria-hidden="true"><Icon name="computer" /></span>
				{/if}
				<span class="chat-msg-name">{msg.from.name}</span>
			</div>
		{/if}
		{#snippet meta()}
			<span class="chat-meta">
				{#if msg.edited_at && !msg.deleted}<span>{t('chat.edited')}</span>{/if}
				<Time
					value={msg.created_at}
					relative={false}
					format={{ year: undefined, month: undefined, day: undefined, hour: '2-digit', minute: '2-digit' }}
				/>
			</span>
		{/snippet}
		<div class="chat-msg-row">
			<div class={['chat-bubble', msg.deleted && 'is-deleted', msg.pending && 'is-pending']}>
				{#if msg.deleted}
					<span class="chat-deleted"><Icon name="block" />{t('chat.message_deleted')}</span>
					{@render meta()}
				{:else}
					{#if msg.reply_to}
						<button type="button" class="chat-quote" onclick={() => msg.reply_to && onquote(msg.reply_to.id)}>
							<span class="chat-quote-name">{msg.reply_to.from_name}</span>
							<span class="chat-quote-text"
								>{msg.reply_to.deleted ? t('chat.message_deleted') : msg.reply_to.preview}</span
							>
						</button>
					{/if}
					{#each pictures as p (p.id)}
						<a class="chat-picture" href={p.url} target="_blank" rel="noopener noreferrer">
							<img
								src={`${p.url}?inline=1`}
								alt={p.name}
								width={p.width || undefined}
								height={p.height || undefined}
								loading="lazy"
							/>
						</a>
					{/each}
					{#each files as f (f.id)}
						<a class="chat-file" href={f.url} download={f.name}>
							<Icon name="description" />
							<span class="chat-file-name">{f.name}</span>
							<span class="chat-file-size">{formatBytes(f.size)}</span>
						</a>
					{/each}
					{#if msg.text}
						<span class="chat-text"
							>{#each parts as p, i (i)}{#if p.href}<a href={p.href} target="_blank" rel="noopener noreferrer"
										>{p.text}</a
									>{:else}{p.text}{/if}{/each}</span
						>
					{/if}
					{@render meta()}
				{/if}
			</div>
			{#if !msg.deleted && !msg.pending}
				<div class="chat-actions" role="group" aria-label={t('chat.message_actions')}>
					<IconButton
						bind:element={pickerBtn}
						icon="add_reaction"
						label={t('chat.react')}
						aria-haspopup="menu"
						onclick={() => pickerBtn && onpicker(msg, pickerBtn)}
					/>
					<IconButton icon="reply" label={t('chat.reply')} onclick={() => onreply(msg)} />
					<MenuButton kebab label={t('chat.more_actions')} align={mine ? 'end' : 'start'}>
						{#each QUICK.slice(0, 3) as e (e)}
							<MenuItem onselect={() => onreact(msg, e, true)}>
								{#snippet lead()}<ChatEmoji emoji={e} />{/snippet}
								{t('chat.react_with', { emoji: e })}
							</MenuItem>
						{/each}
						{#if msg.text}<MenuItem icon="content_copy" onselect={copy}>{t('chat.copy_text')}</MenuItem>{/if}
						{#if mayEdit}<MenuItem icon="edit" onselect={() => onedit(msg)}>{t('chat.edit')}</MenuItem>{/if}
						{#if mayTrail && msg.edited_at}<MenuItem icon="history" onselect={() => ontrail(msg)}
								>{t('chat.edit_history')}</MenuItem
							>{/if}
						{#if mayDelete}<MenuItem icon="delete" danger onselect={() => ondelete(msg)}
								>{t('chat.delete')}</MenuItem
							>{/if}
					</MenuButton>
				</div>
			{:else if msg.deleted && mayTrail}
				<div class="chat-actions">
					<IconButton icon="history" label={t('chat.edit_history')} onclick={() => ontrail(msg)} />
				</div>
			{/if}
		</div>
		{#if reactions.length}
			<div class="chat-reactions">
				{#each reactions as [emoji, who] (emoji)}
					{@const on = reactedByMe(who)}
					<button
						type="button"
						class={['chat-reaction', on && 'is-mine']}
						aria-pressed={on}
						title={reactors(who)}
						aria-label={t('chat.reaction_label', { emoji, count: who.length, names: reactors(who) })}
						onclick={() => onreact(msg, emoji, !on)}><ChatEmoji {emoji} /> {who.length}</button
					>
				{/each}
			</div>
		{/if}
		{#if msg.pending === 'sending'}
			<p class="chat-msg-state">{t('chat.sending')}</p>
		{:else if msg.pending === 'failed'}
			<p class="chat-msg-state is-error">
				{t('chat.not_sent')}
				<button type="button" class="chat-link-btn" onclick={() => onretry(msg)}>{t('actions.retry')}</button>
			</p>
		{:else if mine && seen}
			<p class="chat-msg-state">{t('chat.seen')}</p>
		{/if}
	</div>
{/if}

<style>
	/* A line of the system: one centred, muted pill */
	.chat-system {
		display: flex;
		justify-content: center;
		margin: var(--space-3) 0;
	}
	.chat-system-text {
		display: inline-flex;
		flex-wrap: wrap;
		justify-content: center;
		gap: 0 var(--space-2);
		max-width: 100%;
		padding: 2px var(--space-3);
		border-radius: 999px;
		background: var(--bg-subtle);
		color: var(--text-muted);
		font-size: var(--text-sm);
		text-align: center;
	}
	/* Someone else's messages sit under their name, past the avatar (24 px and the gap) */
	.chat-msg {
		display: flex;
		flex-direction: column;
		align-items: flex-start;
		margin-top: 2px;
		padding-inline-start: calc(24px + var(--space-2));
		border-radius: var(--radius);
		transition: background-color var(--motion);
	}
	.chat-msg.is-first {
		margin-top: var(--space-3);
	}
	.chat-msg.is-mine {
		align-items: flex-end;
		padding-inline-start: 0;
	}
	.chat-msg.is-marked {
		background: var(--accent-tint);
	}
	.chat-msg-head {
		display: flex;
		align-items: center;
		gap: var(--space-2);
		margin-inline-start: calc(-24px - var(--space-2));
		margin-bottom: var(--space-1);
		font-size: var(--text-sm);
	}
	.chat-msg-name {
		font-weight: 600;
	}
	.chat-msg-state {
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	/* Time (and "edited") at the end of the bubble's last line, or under it when the line is full */
	.chat-meta {
		display: inline-flex;
		float: inline-end;
		gap: var(--space-1);
		margin: 4px 0 -2px var(--space-3);
		color: var(--text-muted);
		font-size: 11px;
		line-height: 1.6;
		white-space: nowrap;
	}
	.chat-msg-state {
		margin: 2px 0 0;
	}
	.chat-msg-state.is-error {
		color: var(--danger);
	}
	.chat-device-mark {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		width: 24px;
		height: 24px;
		border-radius: 50%;
		background: var(--surface-2, var(--hover));
		color: var(--text-muted);
		font-size: 16px;
	}
	.chat-msg-row {
		display: flex;
		align-items: flex-start;
		gap: var(--space-1);
		max-width: min(640px, 88%);
	}
	.is-mine .chat-msg-row {
		flex-direction: row-reverse;
	}
	.chat-bubble {
		display: flow-root;
		min-width: 0;
		padding: 6px var(--space-3);
		border: 1px solid var(--rule);
		border-radius: calc(var(--radius) * 3);
		background: var(--bg-subtle);
		overflow-wrap: anywhere;
	}
	.is-mine .chat-bubble {
		border-color: transparent;
		background: var(--accent-tint);
	}
	.is-theirs.is-first .chat-bubble {
		border-start-start-radius: var(--radius);
	}
	.is-mine.is-first .chat-bubble {
		border-start-end-radius: var(--radius);
	}
	.chat-bubble.is-pending {
		opacity: 0.7;
	}
	.chat-bubble.is-deleted {
		background: transparent;
		color: var(--text-muted);
		font-style: italic;
	}
	.chat-deleted {
		display: inline-flex;
		align-items: center;
		gap: var(--space-1);
	}
	.chat-text {
		white-space: pre-wrap;
	}
	.chat-text a {
		color: var(--link, var(--accent));
		text-decoration: underline;
	}
	.chat-quote {
		display: flex;
		flex-direction: column;
		width: 100%;
		margin: 0 0 var(--space-2);
		padding: var(--space-1) var(--space-2);
		border: 0;
		border-inline-start: 3px solid var(--accent);
		border-radius: var(--radius);
		background: var(--hover);
		color: var(--text);
		font: inherit;
		font-size: var(--text-sm);
		text-align: start;
		cursor: pointer;
	}
	.chat-quote-name {
		font-weight: 600;
	}
	.chat-quote-text {
		display: -webkit-box;
		overflow: hidden;
		color: var(--text-muted);
		-webkit-box-orient: vertical;
		-webkit-line-clamp: 2;
		line-clamp: 2;
	}
	.chat-picture {
		display: block;
		margin-bottom: var(--space-1);
	}
	.chat-picture img {
		display: block;
		max-width: min(320px, 100%);
		max-height: 240px;
		width: auto;
		height: auto;
		border-radius: var(--radius);
		object-fit: contain;
	}
	.chat-file {
		display: flex;
		align-items: center;
		gap: var(--space-2);
		margin-bottom: var(--space-1);
		padding: var(--space-2);
		border: 1px solid var(--rule);
		border-radius: var(--radius);
		background: var(--bg);
		color: var(--text);
		text-decoration: none;
	}
	.chat-file:hover {
		background: var(--hover);
	}
	.chat-file-name {
		font-weight: 600;
		overflow-wrap: anywhere;
	}
	.chat-file-size {
		color: var(--text-muted);
		font-size: var(--text-sm);
		white-space: nowrap;
	}
	.chat-actions {
		display: flex;
		flex: none;
		align-items: center;
	}
	@media (hover: hover) {
		.chat-msg:not(:hover):not(:focus-within) .chat-actions {
			opacity: 0;
		}
	}
	.chat-reactions {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-1);
		margin-top: var(--space-1);
	}
	.chat-reaction {
		display: inline-flex;
		align-items: center;
		gap: 4px;
		min-height: var(--control-h-sm);
		padding: 0 var(--space-2);
		border: 1px solid var(--rule);
		border-radius: 999px;
		background: var(--bg);
		color: var(--text);
		font: inherit;
		font-size: var(--text-sm);
		cursor: pointer;
	}
	.chat-reaction.is-mine {
		border-color: var(--accent);
		background: var(--accent-tint);
	}
	.chat-reaction:hover {
		background: var(--hover);
	}
	.chat-link-btn {
		padding: 0;
		border: 0;
		background: none;
		color: var(--link, var(--accent));
		font: inherit;
		text-decoration: underline;
		cursor: pointer;
	}
	@media (pointer: coarse) {
		.chat-reaction {
			min-height: 36px;
		}
	}
</style>
