<!--
  A group of settings (PATTERN.md rules 7 and 10): an h2, one sentence
  about what the group is for, and the one line that says when its
  changes take effect ("Applies to new connections within a minute"),
  said once here instead of under every field. `status` names the
  group's state ("Set for this organization"); `actions` holds buttons
  that act on the whole group ("Use the server's values"). When the page
  filter matches none of its rows, the group hides.
-->
<script lang="ts">
	import type { Snippet } from 'svelte';
	import { GroupRows, provideGroup, useFilter } from './filter.svelte.ts';

	interface Props {
		id: string;
		title: string;
		description?: string;
		/** When changes take effect, once for the group */
		consequence?: string;
		status?: string;
		actions?: Snippet;
		/** A note above the rows (a notice that the group is not applied) */
		note?: Snippet;
		level?: 2 | 3;
		class?: string;
		children: Snippet;
	}

	let {
		id,
		title,
		description,
		consequence,
		status,
		actions,
		note,
		level = 2,
		class: cls = '',
		children
	}: Props = $props();

	const rows = provideGroup(new GroupRows());
	const filter = useFilter();
	const hidden = $derived(!!filter?.active && !rows.anyShown);
	const titleId = $derived(`${id}-title`);
</script>

<section class={['settings-group', cls]} {id} aria-labelledby={titleId} {hidden}>
	<header class="group-head">
		<div class="group-text">
			<svelte:element this={`h${level}`} class="group-title" id={titleId}>{title}</svelte:element>
			{#if description}<p class="group-description">{description}</p>{/if}
			{#if consequence}<p class="group-consequence">{consequence}</p>{/if}
		</div>
		{#if status || actions}
			<div class="group-side">
				{#if status}<span class="group-status">{status}</span>{/if}
				{@render actions?.()}
			</div>
		{/if}
	</header>
	{@render note?.()}
	<div class="group-rows">
		{@render children()}
	</div>
</section>

<style>
	.settings-group {
		display: flex;
		flex-direction: column;
		gap: var(--space-3);
		min-width: 0;
		scroll-margin-top: var(--space-7);
	}
	.settings-group[hidden] {
		display: none;
	}
	.group-head {
		display: flex;
		flex-wrap: wrap;
		align-items: flex-start;
		justify-content: space-between;
		gap: var(--space-2) var(--space-4);
	}
	.group-text {
		display: flex;
		flex-direction: column;
		gap: var(--space-1);
		flex: 1 1 320px;
		min-width: 0;
	}
	.group-title {
		margin: 0;
		font-size: var(--text-md);
		font-weight: 600;
		line-height: 1.3;
	}
	.group-description,
	.group-consequence {
		margin: 0;
		max-width: var(--measure);
		color: var(--text-muted);
	}
	.group-consequence {
		font-size: var(--text-sm);
	}
	.group-side {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-2) var(--space-3);
	}
	/* Plain text, so it never reads as a button beside the group's actions */
	.group-status {
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	/* Rows sit flush with the group's edges: their padding lines the text up with the heading */
	.group-rows {
		margin: 0 calc(-1 * var(--row-pad-x));
		border-top: 1px solid var(--rule);
	}
</style>
