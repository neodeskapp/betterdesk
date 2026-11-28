<!-- Trends: the reports page's two charts of the last 14 days; their code loads with the widget -->
<script lang="ts">
	import { lazy } from '#lib/util/lazy.ts';
	import type { WidgetProps } from './types.ts';

	const props: WidgetProps = $props();
	const trends = lazy(() => import('#lib/people/Trends.svelte'));
</script>

<div data-widget={props.widget.id}>
	{#await trends.optional() then { default: Trends }}<Trends />{/await}
</div>
