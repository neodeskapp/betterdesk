<!--
  A group of settings (settings-pass-2.md rules D and F): an h2, one
  sentence about what the group is for, and the one line that says when
  its changes take effect ("Applies to new connections within a minute"),
  said once here instead of under every field. `note` holds a notice that
  is true for every row of the group ("No NeoDesk app applies this group
  yet"): it goes here once, never on the rows. `status` names the group's
  state when it says something ("Set for this organization"); `actions`
  holds buttons that act on the whole group, only when they can do
  something ("Remove all rules of this group" while it has one). When the
  page filter matches none of its rows, the group hides.

  The rows sit in a size container (settings-rows): from 900 px of width
  each row is two columns, name and help left, the control right.
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
		/** A note above the rows that holds for all of them (a notice that the group is not applied) */
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
	const filterOf = useFilter();
	const hidden = $derived(!!filterOf()?.active && !rows.anyShown);
	const titleId = $derived(`${id}-title`);
</script>

<section class={['settings-group', cls]} {id} aria-labelledby={titleId} {hidden}>
	<header class="group-head">
		<div class="group-text">
			<svelte:element this={`h${level}`} class="group-title" id={titleId}>{title}</svelte:element>
			{#if description || consequence}
				<p class="group-description">
					{#if description}{description}{/if}
					{#if consequence}<span class="group-consequence">{consequence}</span>{/if}
				</p>
			{/if}
		</div>
		{#if status || actions}
			<div class="group-side">
				{#if status}<span class="group-status">{status}</span>{/if}
				{@render actions?.()}
			</div>
		{/if}
	</header>
	{#if note}<div class="group-note">{@render note()}</div>{/if}
	<div class="group-rows">
		{@render children()}
	</div>
</section>

<style>
	.settings-group {
		display: flex;
		flex-direction: column;
		gap: var(--space-2);
		min-width: 0;
		max-width: var(--settings-max, 72rem);
		scroll-margin-top: var(--space-7);
	}
	.settings-group[hidden] {
		display: none;
	}
	.group-head {
		display: flex;
		flex-wrap: wrap;
		align-items: flex-end;
		justify-content: space-between;
		gap: var(--space-2) var(--space-4);
	}
	.group-text {
		display: flex;
		flex-direction: column;
		gap: 2px;
		flex: 1 1 320px;
		min-width: 0;
	}
	.group-title {
		margin: 0;
		font-size: var(--text-md);
		font-weight: 600;
		line-height: 1.3;
	}
	.group-description {
		margin: 0;
		max-width: var(--measure);
		color: var(--text-muted);
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
	.group-note {
		display: flex;
		flex-direction: column;
		gap: var(--space-2);
		min-width: 0;
	}
	/* A note snippet that rendered nothing takes no room */
	.group-note:not(:has(*)) {
		display: none;
	}
	/* Rows sit flush with the group's edges: their padding lines the text up with the heading */
	.group-rows {
		margin: 0 calc(-1 * var(--row-pad-x));
		border-top: 1px solid var(--rule);
		container: settings-rows / inline-size;
	}
</style>
