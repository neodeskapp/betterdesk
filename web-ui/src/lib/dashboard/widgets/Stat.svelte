<!--
  Single number: one count from what the overview already loads (devices
  online, offline, in total, banned; open join or help requests), linking
  to the list it counts. An account that can't read the count sees a
  sentence instead of a wrong zero.
-->
<script lang="ts">
	import { deviceParts } from '#lib/devices-area/dashboard.ts';
	import { formatNumber } from '#lib/i18n/format.ts';
	import { t, tf } from '#lib/i18n/i18n.svelte.ts';
	import { link } from '#lib/nav.ts';
	import { can } from '#lib/state/session.svelte.ts';
	import { METRIC_INFO, metricAllowed, metricOf } from '../model.ts';
	import type { WidgetProps } from './types.ts';

	let { widget, data }: WidgetProps = $props();
	const uid = $props.id();

	const metric = $derived(metricOf(widget));
	const info = $derived(METRIC_INFO[metric]);
	const allowed = $derived(metricAllowed(info, can));
	const label = $derived(widget.title || tf(info.key, info.label));

	const value = $derived.by((): number | null => {
		if (metric === 'registrations') return data.attentionKnown ? data.registrations : null;
		if (metric === 'help') return data.attentionKnown ? data.openHelp : null;
		if (!data.stats) return null;
		if (metric === 'total') return data.stats.total;
		return deviceParts(data.stats)[metric];
	});
	// A count of something that waits for someone takes its colour while above zero
	const tone = $derived.by(() => {
		if (!value) return '';
		if (metric === 'banned') return 'danger';
		if (metric === 'registrations' || metric === 'help') return 'warning';
		return '';
	});
</script>

<section class="section stat" aria-labelledby="{uid}-title">
	<h2 id="{uid}-title" class="stat-label">{label}</h2>
	{#if !allowed}
		<p class="stat-none">{tf('dashboard.widget.not_allowed', 'Your account can’t see this.')}</p>
	{:else}
		<a
			class={['stat-value', tone && `tone-${tone}`]}
			href={link(info.href)}
			aria-label="{label}: {value === null ? t('status.checking') : formatNumber(value)}"
			>{value === null ? '–' : formatNumber(value)}</a
		>
	{/if}
</section>

<style>
	.stat {
		gap: var(--space-1);
	}
	.stat-label {
		font-size: var(--text-base);
		font-weight: 600;
		color: var(--text-muted);
	}
	.stat-value {
		align-self: flex-start;
		font-size: calc(var(--text-xl) * 2);
		font-weight: 600;
		line-height: 1.1;
		font-variant-numeric: tabular-nums;
		color: var(--text);
		text-decoration: none;
	}
	.stat-value:hover {
		text-decoration: underline;
	}
	.tone-warning {
		color: var(--warning);
	}
	.tone-danger {
		color: var(--danger);
	}
	.stat-none {
		margin: 0;
		color: var(--text-muted);
	}
</style>
