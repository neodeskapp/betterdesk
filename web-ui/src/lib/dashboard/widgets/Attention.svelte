<!--
  Needs attention: one line for each thing that waits for someone, only
  while its count is above zero (server problems, join requests, help
  requests, banned devices), each linking to where it is handled.
-->
<script lang="ts">
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import { link } from '#lib/nav.ts';
	import { can, session } from '#lib/state/session.svelte.ts';
	import ListRow from '#lib/ui/ListRow.svelte';
	import RowList from '#lib/ui/RowList.svelte';
	import { deviceParts } from '#lib/devices-area/dashboard.ts';
	import type { WidgetProps } from './types.ts';

	let { widget, data }: WidgetProps = $props();
	const uid = $props.id();

	const canDevices = can('device.view');
	const navPages = new Set(session.nav.flatMap((s) => s.pages.map((p) => p.page)));
	const showServerLink = navPages.has('server-management:overview');

	const banned = $derived(canDevices && data.stats ? deviceParts(data.stats).banned : 0);
	const rows = $derived(
		(data.problem ? 1 : 0) +
			(data.registrations > 0 ? 1 : 0) +
			(data.openHelp > 0 ? 1 : 0) +
			(banned > 0 ? 1 : 0)
	);
</script>

<section class="section" aria-labelledby="{uid}-title">
	<div class="section-head">
		<h2 id="{uid}-title">{widget.title || t('dashboard.attention_panel')}</h2>
	</div>
	<div aria-live="polite">
		<RowList>
			{#if data.problem}
				<ListRow
					mark="danger"
					title={t('dashboard.server_status')}
					sub={t(data.problem)}
					href={showServerLink ? link('/server-management?tab=overview') : undefined}
				/>
			{/if}
			{#if data.registrations > 0}
				<ListRow
					mark="warning"
					count={data.registrations}
					title={t('nav.registrations')}
					href={link('/registrations')}
				/>
			{/if}
			{#if data.openHelp > 0}
				<ListRow
					mark="warning"
					count={data.openHelp}
					title={t('nav.help_requests')}
					href={link('/help-requests')}
				/>
			{/if}
			{#if banned > 0}
				<ListRow
					mark="danger"
					count={banned}
					title={t('dashboard.banned')}
					href={link('/devices?status=banned')}
				/>
			{/if}
			{#if !rows}
				<ListRow
					empty
					title={data.attentionKnown && data.statusKnown
						? t('dashboard.attention_none')
						: t('status.checking')}
				/>
			{/if}
		</RowList>
	</div>
</section>
