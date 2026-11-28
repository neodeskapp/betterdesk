<!--
  Add widget: the widgets this account may put on the dashboard, the
  overview's own first, then the ones people fill in themselves. A widget
  that can be there only once is left out when it already is.
-->
<script lang="ts">
	import { tf } from '#lib/i18n/i18n.svelte.ts';
	import { can } from '#lib/state/session.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import Dialog from '#lib/ui/Dialog.svelte';
	import Icon from '#lib/ui/Icon.svelte';
	import ListRow from '#lib/ui/ListRow.svelte';
	import RowList from '#lib/ui/RowList.svelte';
	import { addable, type Board, type WidgetKind } from './model.ts';

	interface Props {
		board: Board;
		onadd: (kind: WidgetKind) => void;
		onclose: () => void;
	}

	let { board, onadd, onclose }: Props = $props();

	const kinds = $derived(addable(board, can));
	const groups = $derived([
		{
			key: 'overview',
			title: tf('dashboard.group_overview', 'From the overview'),
			kinds: kinds.filter((k) => k.group === 'overview')
		},
		{
			key: 'custom',
			title: tf('dashboard.group_custom', 'Your own'),
			kinds: kinds.filter((k) => k.group === 'custom')
		}
	]);
</script>

<Dialog title={tf('dashboard.add_widget', 'Add widget')} {onclose}>
	{#each groups as g (g.key)}
		{#if g.kinds.length}
			<h3 class="group-title">{g.title}</h3>
			<RowList>
				{#each g.kinds as k (k.type)}
					<ListRow title={tf(k.nameKey, k.name)} sub={tf(k.hintKey, k.hint)} onclick={() => onadd(k)}>
						{#snippet start()}<Icon name={k.icon} />{/snippet}
					</ListRow>
				{/each}
			</RowList>
		{/if}
	{/each}
	{#snippet footer()}
		<Button onclick={onclose}>{tf('common.cancel', 'Cancel')}</Button>
	{/snippet}
</Dialog>

<style>
	.group-title {
		margin: var(--space-4) 0 var(--space-2);
		font-size: var(--text-base);
	}
	.group-title:first-child {
		margin-top: 0;
	}
</style>
