<!--
  Hardening tab: the hardening settings this server runs with, read from
  its configuration (the session timeouts and the session limit per
  account), each with its severity, area and whether it is on.
-->
<script lang="ts">
	import { untrack } from 'svelte';
	import { listHardening, type HardeningItem } from '#lib/api/audit.ts';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import BooleanStatus from '#lib/ui/BooleanStatus.svelte';
	import ListRow from '#lib/ui/ListRow.svelte';
	import RowList from '#lib/ui/RowList.svelte';
	import States from '#lib/ui/States.svelte';
	import Status from '#lib/ui/Status.svelte';
	import Tag from '#lib/ui/Tag.svelte';

	const uid = $props.id();

	let items = $state.raw<HardeningItem[] | null>(null);
	let itemsError = $state<string | null>(null);

	async function loadItems() {
		itemsError = null;
		try {
			items = await listHardening();
		} catch {
			itemsError = t('security_audit.hardening_load_failed');
		}
	}

	$effect(() => {
		untrack(() => void loadItems());
	});

	function severity(sev: string): string {
		const key = `security_audit.severity_${sev}`;
		const word = t(key) === key ? sev : t(key);
		return word.charAt(0).toUpperCase() + word.slice(1);
	}
	const sevTone = (sev: string) =>
		sev === 'critical' || sev === 'high' ? 'danger' : sev === 'medium' ? 'warning' : 'neutral';
</script>

<section class="section" aria-labelledby="{uid}-title">
	<h2 id="{uid}-title" class="sr-only">{t('security_audit.tab_hardening')}</h2>
	<p class="section-note">{t('security_audit.hardening_hint')}</p>
	{#if itemsError}
		<States
			kind="error"
			message={itemsError}
			retryLabel={t('security_audit.try_again')}
			onretry={() => void loadItems()}
		/>
	{:else if !items}
		<States kind="loading" />
	{:else if !items.length}
		<States message={t('security_audit.no_hardening')} />
	{:else}
		<RowList>
			{#each items as h (h.id || h.name)}
				{@const sev = String(h.severity || 'medium').toLowerCase()}
				{#snippet sub()}<span class="desc">{h.description}</span><span class="meta"
						><span class="sr-only"
							>{t('security_audit.hardening_meta', {
								severity: severity(sev),
								category: h.category || '-'
							})}</span
						><span class="marks" aria-hidden="true"
							><Status tone={sevTone(sev)} label={severity(sev)} />{#if h.category}<Tag
									label={h.category}
								/>{/if}</span
						></span
					>{/snippet}
				{#snippet state()}<BooleanStatus value={h.implemented} />{/snippet}
				<ListRow title={h.name} {sub} end={state} />
			{/each}
		</RowList>
	{/if}
</section>

<style>
	.desc,
	.meta {
		display: block;
	}
	.marks {
		display: inline-flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-3);
		margin-top: var(--space-1);
	}
</style>
