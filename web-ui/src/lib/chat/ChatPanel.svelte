<!--
  The chat panel (A242): the chat in a side sheet over any page, opened
  from the top bar's chat button or from a computer, a help request or a
  person (openChat). One column: the list, or a conversation with a way
  back. "Open the chat page" takes the same place to /chat.
-->
<script lang="ts">
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import IconButton from '#lib/ui/IconButton.svelte';
	import Sheet from '#lib/ui/Sheet.svelte';
	import ChatSettingsMenu from './ChatSettingsMenu.svelte';
	import ChatView from './ChatView.svelte';
	import { goChat } from './nav.ts';
	import { chatPanel, closeChat } from './panel.svelte.ts';

	function toPage() {
		const query: Record<string, string> = { s: chatPanel.section, f: chatPanel.filter };
		if (chatPanel.conv) query.c = chatPanel.conv;
		chatPanel.open = false;
		goChat(query);
	}
</script>

<Sheet open={chatPanel.open} label={t('nav.chat')} class="chat-panel" onclose={closeChat}>
	{#snippet header(parts)}
		<div class="sheet-header chat-panel-head">
			<h2 class="sheet-title" id={parts.titleId}>{t('nav.chat')}</h2>
			<ChatSettingsMenu />
			<IconButton icon="open_in_full" label={t('chat.open_page')} onclick={toPage} />
			<IconButton icon="close" label={t('actions.close')} onclick={parts.close} />
		</div>
	{/snippet}
	{#snippet body()}
		<div class="chat-panel-body">
			<ChatView
				compact
				section={chatPanel.section}
				filter={chatPanel.filter}
				conv={chatPanel.conv}
				onchange={(w) => {
					if (w.section) chatPanel.section = w.section;
					if (w.filter) chatPanel.filter = w.filter;
					if (w.conv !== undefined) chatPanel.conv = w.conv;
				}}
			/>
		</div>
	{/snippet}
</Sheet>

<style>
	:global(.sheet.chat-panel) {
		width: min(460px, 100vw);
	}
	.chat-panel-head {
		display: flex;
		align-items: center;
		gap: var(--space-1);
	}
	.chat-panel-head .sheet-title {
		flex: 1;
	}
	.chat-panel-body {
		display: flex;
		flex: 1;
		flex-direction: column;
		min-height: 0;
	}
</style>
