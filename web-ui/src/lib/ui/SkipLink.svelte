<!--
  "Skip to main content" (WCAG 2.4.1): the first thing on a page with
  blocks before its content. Hidden above the window until it has focus.
  Moves focus to the element with the id `target` (main by default),
  which takes focus even when it is not a control.
-->
<script lang="ts">
	import { tf } from '#lib/i18n/i18n.svelte.ts';

	let { target = 'main-content' }: { target?: string } = $props();

	function skip(e: MouseEvent) {
		const el = document.getElementById(target);
		if (!el) return;
		e.preventDefault();
		if (!el.hasAttribute('tabindex')) el.setAttribute('tabindex', '-1');
		el.focus();
	}
</script>

<a class="skip-link" href="#{target}" onclick={skip}>{tf('shell.skip_to_content', 'Skip to main content')}</a>

<style>
	/* Above the screen until it has focus; its shadow only then, since the
	   shadow of the hidden link would still reach into the top edge */
	.skip-link {
		position: fixed;
		top: var(--space-2);
		inset-inline-start: var(--space-2);
		z-index: calc(var(--layer-toast) + 1);
		padding: var(--space-2) var(--space-3);
		border-radius: var(--radius);
		background: var(--bg-float);
		color: var(--accent);
		transform: translateY(calc(-100% - var(--space-4)));
	}
	.skip-link:focus-visible,
	.skip-link:focus {
		box-shadow: var(--shadow-float);
		transform: none;
	}
</style>
