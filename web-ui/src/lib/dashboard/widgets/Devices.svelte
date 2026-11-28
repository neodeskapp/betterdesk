<!--
  Devices: the total and online, offline and banned as parts of the whole.
  A server without devices says what comes next instead of an empty bar.
-->
<script lang="ts">
	import { deviceParts } from '#lib/devices-area/dashboard.ts';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import { link } from '#lib/nav.ts';
	import { can } from '#lib/state/session.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import CountLine from '#lib/ui/CountLine.svelte';
	import ShareBar from '#lib/ui/ShareBar.svelte';
	import States from '#lib/ui/States.svelte';
	import { ICONS } from '#lib/util/display.ts';
	import type { WidgetProps } from './types.ts';

	let { widget, data, actions }: WidgetProps = $props();
	const uid = $props.id();
	const canApprove = can('enrollment.approve');
	const parts = $derived(data.stats ? deviceParts(data.stats) : null);
</script>

<section class="section devices-now" aria-labelledby="{uid}-title">
	<div class="section-head">
		<h2 id="{uid}-title">{widget.title || t('nav.devices')}</h2>
		<a href={link('/devices')}>{t('dashboard.open_device_list')}</a>
	</div>
	<div class="devices-body" aria-live="polite">
		{#if data.stats && data.stats.total === 0}
			<p class="devices-none">{t('devices.empty_none')}</p>
			{#if canApprove}
				<div class="devices-none-actions">
					<Button variant="primary" icon={ICONS.add} onclick={actions.openAddComputer}
						>{t('onboard.invite.button')}</Button
					>
					<a href={link('/registrations')}>{t('devices.empty_join_requests')}</a>
				</div>
			{/if}
		{:else if data.stats && parts}
			<CountLine text={t('dashboard.devices_total_line')} values={{ total: data.stats.total }} />
			<ShareBar
				label={t('nav.devices')}
				parts={[
					{
						value: parts.online,
						label: t('dashboard.devices_now_online'),
						tone: 'ok',
						href: link('/devices?status=online')
					},
					{
						value: parts.offline,
						label: t('dashboard.devices_now_offline'),
						tone: 'muted',
						href: link('/devices?status=offline')
					},
					{
						value: parts.banned,
						label: t('dashboard.devices_now_banned'),
						tone: 'danger',
						href: link('/devices?status=banned')
					}
				]}
			/>
		{:else if data.statsFailed}
			<States kind="error" message={t('errors.load_devices_failed')} onretry={() => void data.load()} />
		{:else}
			<States kind="loading" message={t('status.checking')} />
		{/if}
	</div>
</section>

<style>
	/* The counts keep their place while they load */
	.devices-body {
		display: flex;
		flex-direction: column;
		gap: var(--space-3);
		min-height: 5.5rem;
	}
	.devices-body :global(.share) {
		max-width: var(--measure);
	}
	.devices-none {
		margin: 0;
	}
	.devices-none-actions {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-2) var(--space-4);
	}
</style>
