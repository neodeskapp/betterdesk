<!-- Server status: the server processes at a glance; ports are rarely needed and stay folded -->
<script lang="ts">
	import {
		partLabelKey,
		partState,
		partTone,
		portList,
		type PartState
	} from '#lib/devices-area/dashboard.ts';
	import { formatDuration } from '#lib/i18n/format.ts';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import { link } from '#lib/nav.ts';
	import { session } from '#lib/state/session.svelte.ts';
	import Disclosure from '#lib/ui/Disclosure.svelte';
	import Status from '#lib/ui/Status.svelte';
	import type { WidgetProps } from './types.ts';

	let { widget, data }: WidgetProps = $props();
	const uid = $props.id();

	const navPages = new Set(session.nav.flatMap((s) => s.pages.map((p) => p.page)));
	const showServerLink = navPages.has('server-management:overview');

	const signalState = $derived<PartState | 'checking'>(
		data.statusKnown ? partState(data.status?.hbbs ?? null) : 'checking'
	);
	const relayState = $derived<PartState | 'checking'>(
		data.statusKnown ? partState(data.status?.hbbr ?? null) : 'checking'
	);
	const ports = $derived(portList(data.status));
</script>

<section class="section server-details" aria-labelledby="{uid}-title">
	<div class="section-head">
		<h2 id="{uid}-title">{widget.title || t('dashboard.server_status')}</h2>
		{#if showServerLink}
			<a href={link('/server-management?tab=overview')}>{t('dashboard.open_server_status')}</a>
		{/if}
	</div>
	<dl class="facts">
		<dt>{t('dashboard.signal_server')}</dt>
		<dd><Status tone={partTone(signalState)} label={t(partLabelKey(signalState))} /></dd>
		<dt>{t('dashboard.relay_server_label')}</dt>
		<dd><Status tone={partTone(relayState)} label={t(partLabelKey(relayState))} /></dd>
		<dt>{t('dashboard.web_console')}</dt>
		<dd><Status tone="good" label={t('status.running')} /></dd>
		<dt>{t('dashboard.server_uptime')}</dt>
		<dd class="num">{data.status?.uptime ? formatDuration(data.status.uptime) : '–'}</dd>
	</dl>
	<Disclosure title={t('dashboard.ports_title')} level={3}>
		<dl class="facts">
			{#each ports as p (p.key)}
				<dt>{t(p.key)}</dt>
				<dd class="num">{p.value ?? '–'}</dd>
			{/each}
		</dl>
	</Disclosure>
</section>

<style>
	/* The state at the end of its row, on phones too */
	.server-details > .facts {
		grid-template-columns: minmax(0, 1fr) auto;
	}
	.server-details > .facts > dd {
		justify-content: flex-end;
	}
	@media (max-width: 640px) {
		.server-details > .facts > dt {
			min-height: var(--row-h);
			padding-bottom: var(--row-pad-y);
			border-bottom: 1px solid var(--rule);
			font-size: inherit;
		}
		.server-details > .facts > dd {
			padding-top: var(--row-pad-y);
		}
	}
	.num {
		font-variant-numeric: tabular-nums;
	}
</style>
