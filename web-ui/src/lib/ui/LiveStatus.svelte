<!--
  The line next to a list that loads again by itself (state/live.ts):
  "Last updated 14:02:31", and while live updates are paused (the
  accessibility settings, WCAG 2.2.2) a sentence that says so and a
  Refresh button that loads everything on the page once. Screen readers
  hear the new time after a Refresh, not after every timed load.

  The shell shows one at the top of the page (fallback) when live updates
  are paused and the page has pollers but no line of its own.
-->
<script lang="ts">
	import { untrack } from 'svelte';
	import { formatDate } from '#lib/i18n/format.ts';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import { liveUpdates, refreshNow } from '#lib/state/live-updates.svelte.ts';
	import { announce } from './announce.svelte.ts';
	import Button from './Button.svelte';

	interface Props {
		/** The shell's own line: it does not count as the page's */
		fallback?: boolean;
		class?: string;
	}

	let { fallback = false, class: cls = '' }: Props = $props();

	// Count this line while it is on screen (written without reading it back)
	$effect(() => {
		if (fallback) return;
		untrack(() => liveUpdates.lines++);
		return () => untrack(() => liveUpdates.lines--);
	});

	const TIME: Intl.DateTimeFormatOptions = {
		year: undefined,
		month: undefined,
		day: undefined,
		hour: 'numeric',
		minute: '2-digit',
		second: '2-digit'
	};
	const updated = $derived(
		liveUpdates.lastUpdated === null
			? ''
			: t('live.last_updated', { time: formatDate(liveUpdates.lastUpdated, TIME) })
	);

	async function refresh() {
		await refreshNow();
		const at = liveUpdates.lastUpdated;
		if (at !== null) announce(t('live.last_updated', { time: formatDate(at, TIME) }));
	}
</script>

{#if updated || liveUpdates.paused}
	<div class={['live-status', fallback && 'is-fallback', cls]}>
		{#if liveUpdates.paused}<span>{t('live.paused')}</span>{/if}
		{#if updated}<span>{updated}</span>{/if}
		{#if liveUpdates.paused}
			<Button size="sm" icon="refresh" busy={liveUpdates.refreshing} onclick={() => void refresh()}
				>{t('common.refresh')}</Button
			>
		{/if}
	</div>
{/if}

<style>
	.live-status {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-1) var(--space-3);
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	.is-fallback {
		margin-bottom: var(--space-4);
	}
</style>
