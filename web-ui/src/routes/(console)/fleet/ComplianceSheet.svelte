<!--
  A device's last compliance scan in a wide details sheet: each check
  (passed or failed, with what the device said about it), then the
  installed software the device reported. A device without a scan says
  so.
-->
<script lang="ts">
	import { untrack } from 'svelte';
	import { complianceDetails, type ComplianceCheck, type Software } from '#lib/api/fleet.ts';
	import { checkKey } from '#lib/canvas/fleet.ts';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import CountLine from '#lib/ui/CountLine.svelte';
	import Sheet from '#lib/ui/Sheet.svelte';
	import States from '#lib/ui/States.svelte';
	import Status from '#lib/ui/Status.svelte';
	import Table from '#lib/ui/Table.svelte';
	import Time from '#lib/ui/Time.svelte';
	import type { Column } from '#lib/ui/table.ts';

	interface Props {
		deviceId: string;
		title: string;
		onclose: () => void;
	}

	let { deviceId, title, onclose }: Props = $props();

	let details = $state.raw<{ checks: ComplianceCheck[]; software: Software[]; last_scan: string } | null>(
		null
	);
	let failed = $state<string | null>(null);

	$effect(() => {
		const ctrl = new AbortController();
		const id = deviceId;
		untrack(() => {
			complianceDetails(id, ctrl.signal).then(
				(out) => (details = out),
				(err: unknown) => {
					if (err instanceof DOMException && err.name === 'AbortError') return;
					failed = t('fleet.details_failed', { error: err instanceof Error ? err.message : '' });
				}
			);
		});
		return () => ctrl.abort();
	});

	const softwareKey = (s: Software) => JSON.stringify([s.name, s.version, s.publisher]);
	/** Each program once: the table keys its rows by name, version and publisher */
	const software = $derived([
		...new Map((details?.software ?? []).map((s) => [softwareKey(s), s] as const)).values()
	]);

	const checkColumns = $derived<Column<ComplianceCheck>[]>([
		{ id: 'check', label: t('fleet.checks_title'), cell: checkCell, primary: true },
		{ id: 'result', label: t('fleet.col_status'), cell: resultCell, sortValue: (c) => (c.ok ? 1 : 0) },
		{ id: 'detail', label: t('fleet.col_detail'), cell: detailCell }
	]);
	const softwareColumns = $derived<Column<Software>[]>([
		{
			id: 'name',
			label: t('fleet.col_name'),
			cell: nameCell,
			primary: true,
			sortValue: (s) => s.name.toLowerCase()
		},
		{ id: 'version', label: t('fleet.col_version'), cell: versionCell },
		{ id: 'publisher', label: t('fleet.col_publisher'), cell: publisherCell, sortValue: (s) => s.publisher }
	]);
</script>

{#snippet checkCell(c: ComplianceCheck)}{@const key = checkKey(c.key)}{key ? t(key) : c.key}{/snippet}
{#snippet resultCell(c: ComplianceCheck)}<Status
		tone={c.ok ? 'good' : 'danger'}
		label={c.ok ? t('fleet.check_passed') : t('fleet.check_failed')}
	/>{/snippet}
{#snippet detailCell(c: ComplianceCheck)}{c.detail || '–'}{/snippet}
{#snippet nameCell(s: Software)}{s.name}{/snippet}
{#snippet versionCell(s: Software)}<span class="mono">{s.version || '–'}</span>{/snippet}
{#snippet publisherCell(s: Software)}{s.publisher || '–'}{/snippet}

<Sheet {title} wide {onclose}>
	{#if failed}
		<States kind="error" message={failed} />
	{:else if details === null}
		<States kind="loading" message={t('fleet.loading_details')} />
	{:else if !details.last_scan}
		<p class="muted">{t('fleet.no_scan_yet')}</p>
	{:else}
		<div class="details">
			<p class="muted">{t('fleet.col_last_scan')}: <Time value={details.last_scan} /></p>
			<Table
				rows={details.checks}
				columns={checkColumns}
				key={(c) => c.key}
				label={t('fleet.checks_title')}
				labelVisible
				emptyText={t('fleet.no_checks')}
			/>
			<p class="muted" aria-live="polite">
				<CountLine text={t('fleet.count_software')} values={{ count: software.length }} />
			</p>
			<Table
				rows={software}
				columns={softwareColumns}
				key={softwareKey}
				label={t('fleet.software_title')}
				labelVisible
				emptyText={t('fleet.no_software')}
			/>
		</div>
	{/if}
	{#snippet footer()}
		<Button onclick={onclose}>{t('fleet.close')}</Button>
	{/snippet}
</Sheet>

<style>
	.details {
		display: flex;
		flex-direction: column;
		gap: var(--space-4);
	}
	.muted {
		margin: 0;
		color: var(--text-muted);
	}
	.mono {
		font-family: var(--mono);
	}
</style>
