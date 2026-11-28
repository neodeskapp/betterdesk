<!--
  Dashboards: shortcuts to other dashboards of this account, so one
  dashboard can lead to more detailed ones. Without a choice it lists every
  other dashboard; one that is gone (deleted, hidden, no longer shared)
  is left out.
-->
<script lang="ts">
	import { tf } from '#lib/i18n/i18n.svelte.ts';
	import ListRow from '#lib/ui/ListRow.svelte';
	import RowList from '#lib/ui/RowList.svelte';
	import { boardLinksOf } from '../model.ts';
	import type { WidgetProps } from './types.ts';

	let { widget, actions, boards }: WidgetProps = $props();
	const uid = $props.id();

	const chosen = $derived(boardLinksOf(widget));
	const shown = $derived(
		chosen.length ? chosen.map((k) => boards.find((b) => b.key === k)).filter((b) => !!b) : boards
	);
</script>

<section class="section" aria-labelledby="{uid}-title">
	<div class="section-head">
		<h2 id="{uid}-title">{widget.title || tf('dashboard.widget.boards', 'Dashboards')}</h2>
	</div>
	<RowList>
		{#each shown as b (b.key)}
			<ListRow title={b.board.name} chevron onclick={() => actions.openBoard(b.key)} />
		{:else}
			<ListRow empty title={tf('dashboard.widget.boards_empty', 'No other dashboards yet.')} />
		{/each}
	</RowList>
</section>
