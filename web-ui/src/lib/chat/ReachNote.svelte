<!--
  Whom a support message reaches (A242), in one line above the composer:
  the person in the NeoDesk app, the agent's window, or nobody right now.
  The last one is tinted as a warning.
-->
<script lang="ts">
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import Icon from '#lib/ui/Icon.svelte';
	import { chat } from './client.svelte.ts';
	import type { Device } from './types.ts';

	let { device }: { device: Device } = $props();

	const reach = $derived.by(() => {
		if (chat.deviceChat[device.id] ?? device.chat_connected) return 'app';
		if (device.agent_chat) return 'agent';
		return 'none';
	});
</script>

<p class={['reach', reach === 'none' && 'is-off']} role="status">
	<Icon name={reach === 'none' ? 'cloud_off' : 'check_circle'} />
	{#if reach === 'app'}
		{t('chat.reach_app')}
	{:else if reach === 'agent'}
		{t('chat.reach_agent')}
	{:else}
		{t('chat.reach_none')}
	{/if}
</p>

<style>
	.reach {
		display: flex;
		align-items: center;
		gap: var(--space-2);
		margin: 0;
		padding: var(--space-1) var(--space-4);
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	.reach :global(.material-icons) {
		font-size: 16px;
	}
	.reach.is-off {
		padding-block: var(--space-2);
		border-top: 1px solid var(--rule);
		background: var(--warning-tint, var(--hover));
		color: var(--text);
	}
	@media (max-width: 767px) {
		.reach {
			padding-inline: var(--space-4);
		}
	}
</style>
