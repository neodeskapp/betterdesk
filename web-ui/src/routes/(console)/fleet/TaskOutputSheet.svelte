<!--
  A task's last run in a wide details sheet: a table of its devices (state,
  exit code, attempt, when it finished; a device that has not answered yet
  waits for it), then what all of them printed in one code block. `actions`
  go before Close in the footer (Retry for a failed task).
-->
<script lang="ts">
	import type { Snippet } from 'svelte';
	import { untrack } from 'svelte';
	import { taskRuns, type TaskRun } from '#lib/api/fleet.ts';
	import { formatNumber } from '#lib/i18n/format.ts';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import CodeBlock from '#lib/ui/CodeBlock.svelte';
	import Sheet from '#lib/ui/Sheet.svelte';
	import States from '#lib/ui/States.svelte';
	import Status from '#lib/ui/Status.svelte';
	import Table from '#lib/ui/Table.svelte';
	import Time from '#lib/ui/Time.svelte';
	import type { Column } from '#lib/ui/table.ts';
	import { runStatusKey, runTone } from '#lib/canvas/fleet.ts';

	interface Props {
		taskId: string;
		title: string;
		actions?: Snippet;
		onclose: () => void;
	}

	let { taskId, title, actions, onclose }: Props = $props();

	let runs = $state.raw<TaskRun[] | null>(null);
	let output = $state('');
	let failed = $state<string | null>(null);

	$effect(() => {
		const ctrl = new AbortController();
		const id = taskId;
		untrack(() => {
			taskRuns(id, ctrl.signal).then(
				(out) => {
					runs = out.runs;
					output = out.output;
				},
				(err: unknown) => {
					if (err instanceof DOMException && err.name === 'AbortError') return;
					failed = t('fleet.output_failed', { error: err instanceof Error ? err.message : '' });
				}
			);
		});
		return () => ctrl.abort();
	});

	const columns = $derived<Column<TaskRun>[]>([
		{
			id: 'device',
			label: t('fleet.col_device'),
			cell: deviceCell,
			primary: true,
			sortValue: (r) => r.device_id
		},
		{ id: 'status', label: t('fleet.col_status'), cell: statusCell, sortValue: (r) => r.status },
		{
			id: 'exit',
			label: t('fleet.col_exit_code'),
			cell: exitCell,
			align: 'num',
			sortValue: (r) => Number(r.exit_code ?? -1)
		},
		{ id: 'attempt', label: t('fleet.col_attempt'), cell: attemptCell, align: 'num' },
		{ id: 'finished', label: t('fleet.col_finished'), cell: finishedCell, sortValue: (r) => r.finished_at }
	]);
</script>

{#snippet deviceCell(r: TaskRun)}<span class="mono">{r.device_id}</span>{#if r.hostname}<span class="cell-sub"
			>{r.hostname}</span
		>{/if}{/snippet}

{#snippet statusCell(r: TaskRun)}<Status
		tone={runTone(r.status)}
		label={t(runStatusKey(r.status))}
	/>{/snippet}

{#snippet exitCell(r: TaskRun)}{r.exit_code === null || r.exit_code === undefined || r.exit_code === ''
		? '–'
		: formatNumber(Number(r.exit_code))}{/snippet}

{#snippet attemptCell(r: TaskRun)}{formatNumber(Number(r.attempt ?? 1))}{/snippet}

{#snippet finishedCell(r: TaskRun)}<Time value={r.finished_at} />{/snippet}

<Sheet {title} subtitle={t('fleet.tasks_wait')} wide {onclose}>
	{#if failed}
		<States kind="error" message={failed} />
	{:else if runs === null}
		<States kind="loading" message={t('fleet.loading_output')} />
	{:else}
		<div class="runs">
			<Table
				rows={runs}
				{columns}
				key={(r) => r.id}
				label={t('fleet.runs_label')}
				emptyText={t('fleet.no_runs')}
			/>
			{#if runs.length}
				<CodeBlock code={output || t('fleet.no_output')} title={t('fleet.task_output')} />
			{/if}
		</div>
	{/if}
	{#snippet footer()}
		{@render actions?.()}
		<Button onclick={onclose}>{t('fleet.close')}</Button>
	{/snippet}
</Sheet>

<style>
	.runs {
		display: flex;
		flex-direction: column;
		gap: var(--space-4);
	}
	.mono {
		font-family: var(--mono);
	}
	.cell-sub {
		display: block;
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
</style>
