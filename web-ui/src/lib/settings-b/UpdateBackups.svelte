<!--
  Server › Backups › Before updates: the copies of the files each update
  replaced (old settings.js loadUpdateBackups), each with Restore these
  files and Delete. How many to keep and "Delete older update backups now"
  are the page's (BackupTab: the setting in its draft, the action in its
  danger zone); the page hears the list through `onlist` and asks for it
  again by changing `reload`.
-->
<script lang="ts">
	import {
		deleteUpdateBackup,
		errorText,
		getConsoleStatus,
		listUpdateBackups,
		restoreUpdateBackup,
		type UpdateBackup
	} from '#lib/api/settings-system.ts';
	import { formatBytes, formatNumber } from '#lib/i18n/format.ts';
	import { t, tf } from '#lib/i18n/i18n.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import CountLine from '#lib/ui/CountLine.svelte';
	import ListRow from '#lib/ui/ListRow.svelte';
	import RowList from '#lib/ui/RowList.svelte';
	import States from '#lib/ui/States.svelte';
	import Time from '#lib/ui/Time.svelte';
	import { confirm } from '#lib/ui/dialogs.svelte.ts';
	import { toast } from '#lib/ui/toasts.svelte.ts';
	import { awaitRestart, consoleVersionNow, restartBlocked, sleepMs } from './updates.ts';

	interface Props {
		/** The list as loaded, after every load */
		onlist?: (backups: UpdateBackup[]) => void;
		/** Change it to load the list again */
		reload?: number;
	}

	let { onlist, reload = 0 }: Props = $props();

	let backups = $state.raw<UpdateBackup[]>([]);
	let listState = $state<'loading' | 'ready' | 'error'>('loading');
	let listError = $state('');
	let busy = $state<string | null>(null);

	const total = $derived(backups.reduce((sum, b) => sum + (b.sizeBytes ?? 0), 0));

	async function load() {
		try {
			backups = await listUpdateBackups();
			listState = 'ready';
			onlist?.(backups);
		} catch (err) {
			listError = errorText(err, t('errors.server_error'));
			listState = 'error';
		}
	}

	async function restore(name: string) {
		const yes = await confirm({
			title: t('settings.updates_tab.restore_files'),
			message: t('updates.restore_confirm'),
			confirmLabel: t('settings.updates_tab.restore_files'),
			danger: true
		});
		if (!yes) return;
		busy = name;
		const before = await consoleVersionNow(getConsoleStatus);
		try {
			const answer = await restoreUpdateBackup(name);
			if (restartBlocked(answer)) {
				// No supervisor: the old process goes on until someone starts the server again
				toast.warning(
					tf('updates.restore_restart_by_hand', 'Backup restored. Start the server again to use it.')
				);
				busy = null;
				return;
			}
			toast.success(t('updates.restore_success'));
			// The console ends shortly after the answer: reload once it answers again
			const end = await awaitRestart(before, {
				version: async () => (await getConsoleStatus()).cacheVersion ?? '',
				sleep: sleepMs
			});
			if (end === 'restarted') location.reload();
			else {
				toast.warning(t('updates.restart_timeout'));
				busy = null;
			}
		} catch (err) {
			toast.error(errorText(err, t('errors.server_error')));
			busy = null;
		}
	}

	async function remove(name: string) {
		const yes = await confirm({
			title: t('updates.delete'),
			message: t('updates.delete_confirm', { name }),
			confirmLabel: t('updates.delete'),
			danger: true
		});
		if (!yes) return;
		busy = name;
		try {
			await deleteUpdateBackup(name);
			toast.success(t('updates.delete_success'));
			await load();
		} catch (err) {
			toast.error(errorText(err, t('errors.server_error')));
		} finally {
			busy = null;
		}
	}

	$effect(() => {
		void reload;
		void load();
	});
</script>

<div class="update-backups" id="update-backups-list">
	{#if listState === 'ready' && backups.length}
		<CountLine
			text={t('settings.updates_tab.backups_summary', { size: formatBytes(total) })}
			values={{ count: backups.length }}
		/>
	{/if}
	{#if listState === 'loading'}
		<States kind="loading" />
	{:else if listState === 'error'}
		<States kind="error" message={listError} onretry={() => void load()} />
	{:else if !backups.length}
		<p class="none">{tf('backups_page.no_update_backups', 'No update backups yet.')}</p>
	{:else}
		<RowList>
			{#each backups as b (b.name)}
				<ListRow title={b.name}>
					{#snippet sub()}
						<Time value={b.timestamp} />{#if b.sha},
							<span class="mono">{b.sha}</span>{/if},
						{formatNumber(b.fileCount || b.filesBackedUp || 0)}
						{t('updates.files')}, {formatBytes(b.sizeBytes ?? 0)}
					{/snippet}
					{#snippet end()}
						<Button size="sm" busy={busy === b.name} onclick={() => void restore(b.name)}
							>{t('settings.updates_tab.restore_files')}</Button
						>
						<Button
							size="sm"
							variant="danger-quiet"
							disabled={busy === b.name}
							onclick={() => void remove(b.name)}>{t('updates.delete')}</Button
						>
					{/snippet}
				</ListRow>
			{/each}
		</RowList>
	{/if}
</div>

<style>
	.update-backups {
		display: flex;
		flex-direction: column;
		gap: var(--space-3);
		padding: var(--space-3) var(--row-pad-x);
	}
	.none {
		margin: 0;
		color: var(--text-muted);
	}
	.mono {
		font-family: var(--mono);
	}
</style>
