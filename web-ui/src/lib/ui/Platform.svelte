<!--
  Operating system (GUIDE section 10): its mark and its name on one line
  ("Windows 11 Pro"). `short` shows only the system and its main version
  ("Windows 11"); the full text stays in the tooltip. No platform gives
  Empty (dash, or the `empty` word).
-->
<script lang="ts">
	import { shortPlatform } from '#lib/util/display.ts';
	import Empty from './Empty.svelte';
	import OsIcon from './OsIcon.svelte';

	let {
		platform,
		empty,
		short = false
	}: { platform: string | null | undefined; empty?: string; short?: boolean } = $props();
	const p = $derived(String(platform ?? '').trim());
	const text = $derived(short ? shortPlatform(p) : p);
</script>

{#if p}
	<span class="platform"><OsIcon platform={p} /><span class="platform-name" title={p}>{text}</span></span>
{:else}
	<Empty word={empty} />
{/if}

<style>
	.platform {
		display: inline-flex;
		align-items: center;
		gap: var(--space-2);
		min-width: 0;
		max-width: 100%;
		vertical-align: middle;
		white-space: nowrap;
	}
	/* A long name ends in an ellipsis where its box is narrower (a tile, a narrow column) */
	.platform-name {
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		unicode-bidi: isolate;
	}
</style>
