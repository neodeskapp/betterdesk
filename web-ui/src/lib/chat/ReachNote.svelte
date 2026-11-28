<!--
  Whether a support message reaches the person (A242), above the composer.
  It shows only when it doesn't: neither the NeoDesk app's chat nor an
  agent that takes chat is connected, as a warning notice. When the person
  can be reached it shows nothing.
-->
<script lang="ts">
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import Notice from '#lib/ui/Notice.svelte';
	import { chat } from './client.svelte.ts';
	import type { Device } from './types.ts';

	let { device }: { device: Device } = $props();

	const reachable = $derived(!!(chat.deviceChat[device.id] ?? device.chat_connected) || device.agent_chat);
</script>

{#if !reachable}
	<Notice tone="warning" class="chat-reach" role="status" message={t('chat.reach_none')} />
{/if}

<style>
	/* Small text, lined up with the messages and the composer */
	:global(.notice.chat-reach) {
		font-size: var(--text-sm);
		padding-inline: var(--space-4);
	}
</style>
