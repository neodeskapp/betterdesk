<!--
  The top bar's chat button (A242): opens the chat panel over the page.
  Unread conversations and messages put a dot on it, as on the
  notifications bell; the number is in the button's name.
-->
<script lang="ts">
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import Icon from '#lib/ui/Icon.svelte';
	import { chat } from './client.svelte.ts';
	import { chatPanel, closeChat, openChat } from './panel.svelte.ts';

	let button = $state<HTMLButtonElement | null>(null);
	const count = $derived(chat.total);
	const label = $derived(count ? t('chat.button_unread', { count }) : t('nav.chat'));
</script>

<button
	bind:this={button}
	type="button"
	class="chat-btn"
	aria-label={label}
	title={label}
	aria-haspopup="dialog"
	aria-expanded={chatPanel.open}
	onclick={() => (chatPanel.open ? closeChat() : openChat(null, button))}
>
	<Icon name="chat" />
	{#if count}<span class="chat-btn-count" aria-hidden="true"></span>{/if}
</button>

<style>
	.chat-btn {
		position: relative;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		width: var(--control-h);
		height: var(--control-h);
		border: 0;
		border-radius: var(--radius);
		background: transparent;
		color: var(--text);
		cursor: pointer;
	}
	.chat-btn:hover {
		background: var(--hover);
	}
	/* A dot, as on the notifications bell */
	.chat-btn-count {
		position: absolute;
		top: 6px;
		inset-inline-end: 6px;
		width: 8px;
		height: 8px;
		border-radius: 50%;
		background: var(--danger);
		pointer-events: none;
	}
</style>
