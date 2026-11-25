<!--
  The top bar's chat button (A242): opens the chat panel over the page,
  with the number of unread conversations and messages on it.
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
	{#if count}<span class="chat-btn-count" aria-hidden="true">{count > 99 ? '99+' : count}</span>{/if}
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
	.chat-btn:focus-visible {
		outline: 2px solid var(--focus, var(--accent));
		outline-offset: 1px;
	}
	.chat-btn-count {
		position: absolute;
		top: 2px;
		inset-inline-end: 0;
		min-width: 18px;
		padding: 0 4px;
		border-radius: 999px;
		background: var(--accent);
		color: var(--on-accent, #fff);
		font-size: 11px;
		font-weight: 700;
		line-height: 18px;
		text-align: center;
	}
</style>
