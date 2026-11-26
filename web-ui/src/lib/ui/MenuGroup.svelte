<!--
  A named group of items in a Menu. Screen readers announce the name; a rule
  separates it from the group before it. Put no empty group in a menu: the
  caller renders a group only when it holds an item.
-->
<script lang="ts">
	import type { Snippet } from 'svelte';

	let { label, class: cls = '', children }: { label: string; class?: string; children: Snippet } = $props();
</script>

<div class={['menu-group', cls]} role="group" aria-label={label}>
	{@render children()}
</div>

<style>
	:global(.menu-group + .menu-group) {
		margin-top: var(--space-1);
		padding-top: var(--space-1);
		border-top: 1px solid var(--rule);
	}
</style>
