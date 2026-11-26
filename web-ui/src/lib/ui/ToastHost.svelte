<!--
  Where toasts appear (components.css "Toasts"): the end side of the bottom
  edge, above the mobile tab bar on small screens. The shell mounts it
  once. Errors sit above the rest. Screen readers hear toasts through the
  announcer (announce.svelte.ts), not from here, so the close buttons are
  not read with each message; the toasts themselves can still be reached
  and closed.

  While a menu, dialog or sheet is open the stack goes below it, so a
  toast never covers what the person opened ("Copied" from a sheet then
  shows as the tick on its button; screen readers still hear the toast).
  An error stays on top. The container hangs off <body> and is marked
  data-layer-live, so the page behind an open dialog can be inert while
  toasts are still read.
-->
<script lang="ts">
	import Toast from './Toast.svelte';
	import { portal } from './portal.ts';
	import { overlays } from './popups.svelte.ts';
	import { toasts } from './toasts.svelte.ts';

	const polite = $derived(toasts.items.filter((t) => t.type !== 'error'));
	const errors = $derived(toasts.items.filter((t) => t.type === 'error'));
	const under = $derived((overlays.popup || overlays.layers > 0) && errors.length === 0);
</script>

<!-- Hangs off <body> outside the page, so its close buttons stay reachable while the page behind a dialog is inert (layers.ts) -->
<div class={['toast-container', under && 'is-under']} data-layer-live {@attach portal}>
	<div class="toast-region">
		{#each polite as t (t.id)}<Toast toast={t} />{/each}
	</div>
	<div class="toast-region">
		{#each errors as t (t.id)}<Toast toast={t} />{/each}
	</div>
</div>

<style>
	.toast-container {
		position: fixed;
		inset-inline-end: var(--space-4);
		bottom: var(--space-4);
		z-index: var(--layer-toast);
		display: flex;
		flex-direction: column;
		gap: var(--space-2);
		pointer-events: none;
	}
	.toast-container.is-under {
		z-index: 0;
	}
	.toast-region {
		display: flex;
		flex-direction: column;
		gap: var(--space-2);
	}
	@media (max-width: 767px) {
		.toast-container {
			inset-inline: var(--space-4);
			bottom: calc(64px + env(safe-area-inset-bottom, 0px));
		}
	}
</style>
