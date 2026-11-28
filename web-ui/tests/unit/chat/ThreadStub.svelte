<!--
  Stands in for Thread (and for ConversationList, which then shows
  nothing) in tests of ChatView: shows the header and the notice it is
  given, and with onsend a Send button that calls it like a sent message
  does.
-->
<script lang="ts">
	import type { Snippet } from 'svelte';

	let { header, notice, onsend }: { header?: Snippet; notice?: Snippet; onsend?: () => void } = $props();
</script>

{@render header?.()}
{@render notice?.()}
{#if onsend}<button type="button" onclick={() => onsend?.()}>Send</button>{/if}
