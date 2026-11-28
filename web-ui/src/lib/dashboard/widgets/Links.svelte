<!--
  Links: shortcuts someone put on the dashboard, to console pages
  ("/devices?status=offline") or http(s) websites, which open in a new tab.
  Addresses of any other kind never get here (model.ts safeUrl).
-->
<script lang="ts">
	import ExternalRow from '#lib/devices-area/ExternalRow.svelte';
	import { tf } from '#lib/i18n/i18n.svelte.ts';
	import { link } from '#lib/nav.ts';
	import ListRow from '#lib/ui/ListRow.svelte';
	import RowList from '#lib/ui/RowList.svelte';
	import { isExternal, linksOf } from '../model.ts';
	import type { WidgetProps } from './types.ts';

	let { widget }: WidgetProps = $props();
	const uid = $props.id();
	const links = $derived(linksOf(widget));
</script>

<section class="section" aria-labelledby="{uid}-title">
	<div class="section-head">
		<h2 id="{uid}-title">{widget.title || tf('dashboard.widget.links', 'Links')}</h2>
	</div>
	<RowList>
		{#each links as l, i (i)}
			{#if isExternal(l.url)}
				<ExternalRow href={l.url} title={l.label || l.url} />
			{:else}
				<ListRow href={link(l.url)} title={l.label || l.url} />
			{/if}
		{:else}
			<ListRow
				empty
				title={tf('dashboard.widget.links_empty', 'No links yet. Customize the dashboard to add some.')}
			/>
		{/each}
	</RowList>
</section>
