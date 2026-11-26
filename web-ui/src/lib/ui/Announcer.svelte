<!--
  The two live regions every status message goes through (announce.svelte.ts):
  polite and assertive. Mounted once in the root layout. They hang off
  <body> and are marked data-layer-live, so they stay announced while a
  dialog makes the page behind it inert (layers.ts). Not visible.
-->
<script lang="ts">
	import { announcer } from './announce.svelte.ts';
	import { portal } from './portal.ts';
</script>

<div class="announcer sr-only" data-layer-live {@attach portal}>
	<div data-announcer="polite" aria-live="polite" aria-atomic="true">{announcer.polite}</div>
	<div data-announcer="assertive" aria-live="assertive" aria-atomic="true">{announcer.assertive}</div>
</div>
