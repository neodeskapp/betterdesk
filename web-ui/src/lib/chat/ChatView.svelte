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
	import States from '#lib/ui/States.svelte';
	import { toast } from '#lib/ui/toasts.svelte.ts';
	import * as api from './api.ts';
	import CannedReplies from './CannedReplies.svelte';
	import { chat } from './client.svelte.ts';
	import ConversationHeader from './ConversationHeader.svelte';
	import ConversationList from './ConversationList.svelte';
	import NewConversation from './NewConversation.svelte';
	import ReachNote from './ReachNote.svelte';
	import { sectionOf } from './panel.svelte.ts';
	import TeamDialog from './TeamDialog.svelte';
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

	/** The open conversation's facts: the ones loaded last show at once, fresh ones follow */
	const detail = $derived<api.ConversationDetail | null>((conv && chat.details[conv]) || null);
	let lastRow: HTMLElement | null = null;
	/** A found message to show when its conversation opens */
	let focus = $state<{ conv: string; message: string } | null>(null);

	// A team that is deleted, or one this account left, closes
	$effect(() => {
		const c = conv;
		if (!c) return;
		return chat.onRemoved((id) => {
			if (id === c) onchange({ conv: null });
		});
	});

	// The open conversation's facts, and again when the server says it changed
	$effect(() => {
		const c = conv;
		if (!c) return;
		void reload(c, false);
		return chat.onConversation((changed) => {
			if (changed.id === c) void reload(c);
		});
	});

	/** fresh: after a change, not the answer of a request that left before it */
	async function reload(c: string, fresh = true) {
		try {
			await chat.loadDetail(c, fresh);
		} catch {
			/* keeps what it has */
		}
	}

	function changed(c: Conversation) {
		const known = chat.details[c.id];
		if (known) chat.setDetail(c.id, { ...known, conversation: c });
		chat.listVersion++;
		void reload(c.id);
	}

	/** The first answer to a support conversation nobody has takes it, for those who may assign */
	async function takeOnReply(c: string) {
		const d = chat.details[c];
		const me = chat.me;
		if (!d || !me || !chat.may.assign) return;
		const it = d.conversation;
		if (it.kind !== 'support' || it.state !== 'open' || it.assignee_id) return;
		try {
			changed(await api.assign(c, me.id));
		} catch (err) {
			toast.error(err instanceof Error ? err.message : t('chat.action_failed'));
		}
	}

	function open(c: string, row: HTMLElement | null, message?: string) {
		lastRow = row;
		focus = message ? { conv: c, message } : null;
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
	/** The team in TeamDialog: its id, '' for a new one */
	let editingTeam = $state<string | null>(null);

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
				<Thread
					{conv}
					{target}
					{writable}
					focus={focus?.conv === conv ? focus.message : null}
					autofocus={!compact && !focus}
					onmanagecanned={() => (managing = true)}
					onsend={() => void takeOnReply(conv)}
				>
					{#snippet header()}
						<ConversationHeader
							{conv}
							{detail}
							{compact}
							onback={back}
							onchanged={changed}
							ongroup={() => (editingGroup = true)}
							onteam={() => (editingTeam = conv)}
						/>
					{/snippet}
					{#snippet notice()}
						{#if kind === 'support' && detail?.device}
							<ReachNote device={detail.device} />
						{/if}
					{/snippet}
					{#snippet empty()}
						<States
							class="chat-empty-thread"
							message={kind === 'support'
								? t('chat.empty_thread_support', { name: target })
								: t('chat.empty_thread_team', { name: target })}
						/>
					{/snippet}
				</Thread>
			{/key}
		{:else}
			<States class="chat-pick" message={t('chat.pick_conversation')} />
		{/if}
	</div>
</div>

{#if creating}
	<NewConversation
		{section}
		onteam={can('chat.teams_manage') ? () => (editingTeam = '') : undefined}
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
{#if editingTeam !== null}
	<TeamDialog
		id={editingTeam || null}
		onclose={() => (editingTeam = null)}
		onsaved={(c) => {
			chat.listVersion++;
			if (c === conv) void reload(c);
			else onchange({ section: 'team', conv: c });
		}}
		ondeleted={(c) => {
			chat.listVersion++;
			if (c === conv) onchange({ conv: null });
		}}
	/>
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
	/* One muted line, centred: in the middle of the empty pane, or at the
	   top of a conversation without messages */
	.chat-pane-thread :global(:is(.chat-pick, .chat-empty-thread)) {
		justify-content: center;
		text-align: center;
	}
	.chat-pane-thread :global(.chat-pick) {
		flex: 1;
		padding-inline: var(--space-5);
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
