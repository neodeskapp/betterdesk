<!--
  Operating system (GUIDE section 10): its mark and its name on one line
  ("Windows 11 Pro"). No platform gives Empty (dash, or the `empty` word).
-->
<script lang="ts">
	import Empty from './Empty.svelte';
	import OsIcon from './OsIcon.svelte';

	let { platform, empty }: { platform: string | null | undefined; empty?: string } = $props();
	const p = $derived(String(platform ?? '').trim());
</script>

{#if p}
	<span class="platform"><OsIcon platform={p} /><span class="platform-name" title={p}>{p}</span></span>
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
