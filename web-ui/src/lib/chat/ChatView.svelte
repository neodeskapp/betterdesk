<!--
  The chat (A242): the list of conversations beside the open one, on the
  chat page and in the chat panel. On a phone, and in the panel, one shows
  at a time: the list, or the conversation with a way back. The caller
  owns where it is (section, filter, conversation) and gets told what
  changes, so the page keeps it in its address and the panel in its state.
-->
<script lang="ts">
	import { tick, untrack } from 'svelte';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import { can, canWrite } from '#lib/state/session.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import Icon from '#lib/ui/Icon.svelte';
	import * as api from './api.ts';
	import CannedReplies from './CannedReplies.svelte';
	import { chat } from './client.svelte.ts';
	import ConversationHeader from './ConversationHeader.svelte';
	import ConversationList from './ConversationList.svelte';
	import NewConversation from './NewConversation.svelte';
	import { sectionOf } from './panel.svelte.ts';
	import Thread from './Thread.svelte';
	import type { Conversation, InboxFilter, Person, Section } from './types.ts';

	interface Props {
		section: Section;
		filter: InboxFilter;
		conv: string | null;
		/** The panel: one column, always list or conversation */
		compact?: boolean;
		onchange: (where: { section?: Section; filter?: InboxFilter; conv?: string | null }) => void;
	}

	let { section, filter, conv, compact = false, onchange }: Props = $props();

	const kind = $derived(conv ? sectionOf(conv) : null);
	// Old messages of version 1 are for reading only
	const writable = $derived(
		conv?.startsWith('old:')
			? false
			: kind === 'team'
				? canWrite('chat.team') || can('chat.team')
				: canWrite('chat.support')
	);

	let detail = $state<api.ConversationDetail | null>(null);
	let lastRow: HTMLElement | null = null;

	// The open conversation's facts, and again when the server says it changed
	$effect(() => {
		const c = conv;
		detail = null;
		if (!c) return;
		let gone = false;
		api.loadConversation(c).then(
			(d) => {
				if (!gone) detail = d;
			},
			() => {
				if (!gone) detail = null;
			}
		);
		const off = chat.onConversation((changed) => {
			if (changed.id === c) void reload(c);
		});
		return () => {
			gone = true;
			off();
		};
	});

	async function reload(c: string) {
		try {
			const d = await api.loadConversation(c);
			if (conv === c) detail = d;
		} catch {
			/* keeps what it has */
		}
	}

	function changed(c: Conversation) {
		if (detail && detail.conversation.id === c.id) detail = { ...detail, conversation: c };
		chat.listVersion++;
		void reload(c.id);
	}

	function open(c: string, row: HTMLElement | null) {
		lastRow = row;
		onchange({ conv: c });
	}

	function back() {
		onchange({ conv: null });
		void tick().then(() => {
			if (lastRow?.isConnected) lastRow.focus();
		});
	}

	async function person(p: Person, row: HTMLElement | null) {
		try {
			const c = await api.openDirect(p.id);
			chat.listVersion++;
			open(c.id, row);
		} catch {
			/* the list stays */
		}
	}

	let creating = $state(false);
	let editingGroup = $state(false);
	let managing = $state(false);

	const target = $derived.by(() => {
		if (!detail) return conv ?? '';
		if (detail.device) return detail.device.name;
		const others = (detail.people ?? []).filter((p) => p.id !== chat.me?.id);
		return detail.conversation.title || others.map((p) => p.name).join(', ') || conv || '';
	});

	// A section the account doesn't have gives way to the other one
	$effect(() => {
		const s = section;
		untrack(() => {
			if (chat.status === 'idle') return;
			if (s === 'support' && !chat.may.support && chat.may.team) onchange({ section: 'team' });
			if (s === 'team' && !chat.may.team && chat.may.support) onchange({ section: 'support' });
		});
	});
</script>

<div class={['chat-view', compact && 'is-compact', conv && 'is-reading']}>
	<div class="chat-pane-list">
		<ConversationList
			{section}
			{filter}
			active={conv}
			{compact}
			onsection={(s) => onchange({ section: s, conv: null })}
			onfilter={(f) => onchange({ filter: f })}
			onopen={open}
			onnew={() => (creating = true)}
			onperson={(p, row) => void person(p, row)}
		/>
	</div>
	<div class="chat-pane-thread">
		{#if conv}
			{#key conv}
				<Thread {conv} {target} {writable} autofocus={!compact} onmanagecanned={() => (managing = true)}>
					{#snippet header()}
						<ConversationHeader
							{conv}
							{detail}
							{compact}
							onback={back}
							onchanged={changed}
							ongroup={() => (editingGroup = true)}
						/>
					{/snippet}
					{#snippet empty()}
						<div class="chat-empty-thread">
							<Icon name="forum" />
							<p>
								{kind === 'support'
									? t('chat.empty_thread_support', { name: target })
									: t('chat.empty_thread_team', { name: target })}
							</p>
						</div>
					{/snippet}
				</Thread>
			{/key}
		{:else}
			<div class="chat-pick">
				<Icon name="forum" />
				<p>{t('chat.pick_conversation')}</p>
				<Button icon="edit_square" onclick={() => (creating = true)}>{t('chat.new')}</Button>
			</div>
		{/if}
	</div>
</div>

{#if creating}
	<NewConversation
		{section}
		onclose={() => (creating = false)}
		onopen={(c) => {
			chat.listVersion++;
			onchange({ conv: c });
		}}
	/>
{/if}
{#if editingGroup && detail}
	<NewConversation
		section="team"
		group={{
			id: detail.conversation.id,
			title: detail.conversation.title ?? '',
			members: (detail.people ?? []).map((p) => p.id)
		}}
		onclose={() => (editingGroup = false)}
		onopen={(c) => {
			chat.listVersion++;
			void reload(c);
		}}
	/>
{/if}
{#if managing}
	<CannedReplies onclose={() => (managing = false)} />
{/if}

<style>
	.chat-view {
		display: grid;
		grid-template-columns: minmax(280px, 360px) minmax(0, 1fr);
		flex: 1;
		min-height: 0;
		height: 100%;
		border-top: 1px solid var(--rule);
	}
	.chat-pane-list {
		min-width: 0;
		min-height: 0;
		border-inline-end: 1px solid var(--rule);
	}
	.chat-pane-thread {
		display: flex;
		flex-direction: column;
		min-width: 0;
		min-height: 0;
	}
	.chat-pick,
	.chat-empty-thread {
		display: flex;
		flex: 1;
		flex-direction: column;
		align-items: center;
		justify-content: center;
		gap: var(--space-3);
		padding: var(--space-5);
		color: var(--text-muted);
		text-align: center;
	}
	.chat-pick > :global(.material-icons),
	.chat-empty-thread > :global(.material-icons) {
		font-size: 48px;
	}
	.chat-pick p,
	.chat-empty-thread p {
		max-width: 36ch;
		margin: 0;
	}
	.is-compact {
		grid-template-columns: minmax(0, 1fr);
	}
	.is-compact.is-reading .chat-pane-list,
	.is-compact:not(.is-reading) .chat-pane-thread {
		display: none;
	}
	.is-compact .chat-pane-list {
		border-inline-end: 0;
	}
	@media (max-width: 767px) {
		.chat-view {
			grid-template-columns: minmax(0, 1fr);
		}
		.chat-pane-list {
			border-inline-end: 0;
		}
		.is-reading .chat-pane-list,
		.chat-view:not(.is-reading) .chat-pane-thread {
			display: none;
		}
	}
</style>
