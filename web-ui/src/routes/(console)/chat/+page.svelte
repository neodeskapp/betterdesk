<!--
  Chat (/chat, A242): Support, the conversations with the people at the
  computers, and Team, the conversations between colleagues, as two
  sections of the same page. The address keeps where one is:
  ?s=support|team, ?f=open|mine|closed (the support inbox's filter) and
  ?c=<conversation> (a device id, dm:<id>:<id> or group:<id>), so a reload,
  a link and the back button land in the same place.

  The page shows lib/chat/ChatView on the tab's chat (lib/chat/client),
  which the shell starts for accounts with chat.support or chat.team;
  without either it says so in one line.
-->
<script lang="ts">
	import { page } from '$app/state';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import { can } from '#lib/state/session.svelte.ts';
	import Notice from '#lib/ui/Notice.svelte';
	import ChatSettingsMenu from '#lib/chat/ChatSettingsMenu.svelte';
	import ChatView from '#lib/chat/ChatView.svelte';
	import { chat } from '#lib/chat/client.svelte.ts';
	import { goChat } from '#lib/chat/nav.ts';
	import { sectionOf } from '#lib/chat/panel.svelte.ts';
	import type { InboxFilter, Section } from '#lib/chat/types.ts';

	const allowed = $derived(can('chat.support') || can('chat.team'));
	const conv = $derived(page.url.searchParams.get('c') || null);
	const section = $derived<Section>(
		(page.url.searchParams.get('s') as Section | null) ??
			(conv ? sectionOf(conv) : can('chat.support') ? 'support' : 'team')
	);
	const filter = $derived<InboxFilter>(
		(['open', 'mine', 'closed'] as const).find((f) => f === page.url.searchParams.get('f')) ?? 'open'
	);

	function go(w: { section?: Section; filter?: InboxFilter; conv?: string | null }) {
		const query = Object.fromEntries(page.url.searchParams);
		if (w.section) query.s = w.section;
		if (w.filter) query.f = w.filter;
		if (w.conv) query.c = w.conv;
		else if (w.conv === null) delete query.c;
		// Opening a conversation is a step back can undo; switching filters is not
		goChat(query, w.conv === undefined);
	}
</script>

<div class={['page page-wide chat-page', conv && 'is-reading']}>
	<div class="page-head">
		<h1>{t('nav.chat')}</h1>
		{#if allowed}
			<div class="page-actions">
				{#if chat.status === 'closed'}<span class="chat-offline" role="status">{t('chat.not_connected')}</span
					>{/if}
				<ChatSettingsMenu />
			</div>
		{/if}
	</div>

	{#if !allowed}
		<Notice tone="info" message={t('chat.needs_access')} />
	{:else}
		<div class="chat-frame">
			<ChatView {section} {filter} {conv} onchange={go} />
		</div>
	{/if}
</div>

<style>
	/* The chat fills the window below the top bar; its parts scroll on their own */
	.chat-page {
		height: calc(100dvh - var(--topbar-h) - var(--page-pad) - var(--space-5));
		min-height: 480px;
		gap: var(--space-3);
	}
	.chat-page .page-head {
		align-items: center;
	}
	.chat-offline {
		color: var(--danger);
		font-size: var(--text-sm);
	}
	/* One framed box: the list and the conversation share its edges */
	.chat-frame {
		display: flex;
		flex: 1;
		min-height: 0;
		overflow: hidden;
		border: 1px solid var(--rule);
		border-radius: calc(var(--radius) * 2);
		background: var(--bg);
	}
	.chat-frame > :global(.chat-view) {
		border-top: 0;
	}
	@media (max-width: 767px) {
		.chat-page {
			height: calc(100dvh - var(--topbar-h) - 56px - env(safe-area-inset-bottom, 0px) - var(--space-4));
			min-height: 420px;
			gap: var(--space-2);
		}
		/* Reading on a phone: the conversation takes the page, its head says where one is */
		.chat-page.is-reading .page-head {
			display: none;
		}
	}
</style>
