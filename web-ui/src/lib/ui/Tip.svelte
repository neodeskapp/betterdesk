<!--
  An explanation that a title attribute would only show to a mouse
  (WCAG 1.3.1, 1.4.13): the content takes focus, and a tooltip opens on
  focus and on hover. It stays while the pointer is on it, Escape closes
  it, and screen readers hear it as the content's description. Used by
  Status and Tag; `class` goes on the focusable wrapper, so it can be the
  component's own root.
-->
<script lang="ts">
	import type { Snippet } from 'svelte';

	let { text, class: cls = '', children }: { text: string; class?: string; children: Snippet } = $props();

	const id = $props.id();
	let open = $state(false);

	function onkeydown(e: KeyboardEvent) {
		if (e.key !== 'Escape' || !open) return;
		// Only the tooltip closes, not a sheet or dialog around it
		e.stopPropagation();
		open = false;
	}
</script>

<!-- Text that takes focus so its tooltip opens from the keyboard too; it does nothing on Enter -->
<!-- svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_static_element_interactions -->
<span
	class={['tip', cls]}
	tabindex="0"
	aria-describedby={id}
	onfocus={() => (open = true)}
	onblur={() => (open = false)}
	onpointerenter={() => (open = true)}
	onpointerleave={() => (open = false)}
	{onkeydown}>{@render children()}<span class="tip-box" role="tooltip" {id} hidden={!open}>{text}</span></span
>

<style>
	.tip {
		position: relative;
	}
	.tip-box {
		position: absolute;
		top: calc(100% + var(--space-1));
		inset-inline-start: 0;
		z-index: var(--layer-toast);
		box-sizing: border-box;
		width: max-content;
		max-width: min(320px, 80vw);
		padding: var(--space-1) var(--space-2);
		border: 1px solid var(--rule);
		border-radius: var(--radius-sm);
		background: var(--bg-float);
		box-shadow: var(--shadow-float);
		color: var(--text);
		font-size: var(--text-sm);
		font-weight: 400;
		line-height: 1.4;
		white-space: normal;
		text-align: start;
	}
	.tip-box[hidden] {
		display: none;
	}
</style>
