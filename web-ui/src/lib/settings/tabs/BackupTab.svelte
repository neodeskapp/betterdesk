<!--
  Server › Backups (/server/backups, server.config; /settings?tab=backup
  leads here). One settings page (PATTERN.md):

    summary      what a backup holds now (the server's parts, with why a
                 part is left out), and the scheduled backups' state
    Make a backup     actions: download one, make one now
    Scheduled backups settings: on, how often, time of day, how many to
                      keep, where
    Before updates    setting: how many update backups to keep; the list
    Restore           three steps: a file, what to restore, confirm
    Danger zone       Delete older update backups now

  The settings of both groups are one SettingsDraft saved by the one
  ChangesBar. Downloading, scheduling and restoring full backups is for
  super admins (a backup holds every secret of the server).
-->
<script lang="ts">
	import {
		downloadBackup,
		errorText,
		getBackupRetention,
		getBackupSchedule,
		getBackupStats,
		pruneUpdateBackups,
		restoreBackup,
		runScheduledBackup,
		saveBackupSchedule,
		setBackupRetention,
		testBackupDestination,
		type BackupStats,
		type RestoreResult,
		type Schedule,
		type ScheduleStatus,
		type UpdateBackup
	} from '#lib/api/settings-system.ts';
	import { formatDate, formatNumber } from '#lib/i18n/format.ts';
	import { i18n, t, tf } from '#lib/i18n/i18n.svelte.ts';
	import {
		INTERVALS,
		JSON_RESTORE_ITEMS,
		RESTORE_PARTS,
		backupErrors,
		backupValues,
		canRestore,
		fileKind,
		nextBackup,
		partRows,
		pruneCount,
		restoreFields,
		restoreHint,
		restoreNeedsRestart,
		restoreOutcome,
		restoreStep,
		restoredPartLabel,
		savePlan,
		scheduleBody,
		securityWarning,
		takesTimeOfDay,
		timeOfDayOptions,
		updateKeepFromStored,
		updateKeepToStored,
		withInterval,
		type BackupValues,
		type KeepValue,
		type RestorePart
	} from '#lib/settings-b/backup.ts';
	import IdFacts, { type IdFact } from '#lib/settings-b/IdFacts.svelte';
	import UpdateBackups from '#lib/settings-b/UpdateBackups.svelte';
	import { onRefresh } from '#lib/shell/page.svelte.ts';
	import { can, session } from '#lib/state/session.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import Checkbox from '#lib/ui/Checkbox.svelte';
	import DangerZone from '#lib/ui/DangerZone.svelte';
	import Dialog from '#lib/ui/Dialog.svelte';
	import Facts, { type Fact } from '#lib/ui/Facts.svelte';
	import FilePicker from '#lib/ui/FilePicker.svelte';
	import FormStatus from '#lib/ui/FormStatus.svelte';
	import Input from '#lib/ui/Input.svelte';
	import Notice from '#lib/ui/Notice.svelte';
	import Select from '#lib/ui/Select.svelte';
	import States from '#lib/ui/States.svelte';
	import Status from '#lib/ui/Status.svelte';
	import Time from '#lib/ui/Time.svelte';
	import ValueCopy from '#lib/ui/ValueCopy.svelte';
	import { confirm } from '#lib/ui/dialogs.svelte.ts';
	import ChangesBar from '#lib/ui/settings/ChangesBar.svelte';
	import KeepCount from '#lib/ui/settings/KeepCount.svelte';
	import SettingRow, { type ValueSource } from '#lib/ui/settings/SettingRow.svelte';
	import SettingsGroup from '#lib/ui/settings/SettingsGroup.svelte';
	import SettingsPage from '#lib/ui/settings/SettingsPage.svelte';
	import { SettingsDraft, saveAndConfirm } from '#lib/ui/settings/draft.svelte.ts';
	import { toast } from '#lib/ui/toasts.svelte.ts';

	const allowed = can('server.config');
	/** Full backups hold every secret of the server: only super admins download, schedule or restore them */
	const fullBackups = $derived(session.isSuperAdmin);

	type Line = { message: string; tone: 'neutral' | 'ok' | 'error' };
	const NONE: Line = { message: '', tone: 'neutral' };
	const ok = (message: string): Line => ({ message, tone: 'ok' });
	const fail = (err: unknown): Line => ({ message: errorText(err, t('errors.server_error')), tone: 'error' });

	// ── The settings: one draft for the schedule and "Keep update backups" ──

	function intervalLabel(hours: number): string {
		if (hours === 24) return tf('backups_page.every_day', 'Every day');
		if (hours === 168) return tf('backups_page.every_week', 'Every week');
		const known = INTERVALS.find((i) => i.value === hours);
		return known ? t(known.label) : tf('backups_page.every_n_hours', 'Every {count} hours', { count: hours });
	}
	const destinationLabel = (v: unknown) =>
		v === 'path'
			? tf('backups_page.dest_folder', 'A folder on the server')
			: tf('backups_page.dest_local', 'The NeoDesk data folder');
	const keepLabel = (v: unknown) => {
		const k = v as KeepValue;
		return k.all ? tf('backups_page.keep_all_short', 'all') : String(k.count);
	};

	const draft = new SettingsDraft<BackupValues>(backupValues(null, 0), {
		enabled: { label: tf('backups_page.enabled', 'Make backups on a schedule') },
		intervalHours: {
			label: tf('backups_page.how_often', 'How often'),
			format: (v) => intervalLabel(Number(v))
		},
		timeOfDay: {
			label: tf('backups_page.time_of_day', 'Time of day'),
			format: (v) => (v ? String(v) : tf('backups_page.no_set_time', 'No set time'))
		},
		retentionCount: { label: t('backup.schedule_retention') },
		destinationType: { label: tf('backups_page.where', 'Where to save'), format: destinationLabel },
		destinationPath: { label: tf('backups_page.folder', 'Folder on the server') },
		updateKeep: { label: t('settings.updates_tab.retention_label'), format: keepLabel }
	});
	$effect(() => draft.track());

	const v = $derived(draft.values);
	const errors = $derived(backupErrors(v, tf));
	const scheduleChanged = $derived(savePlan(draft.changes.map((c) => c.field)).schedule);

	let loaded = $state(false);
	let loadError = $state('');
	let savedSchedule = $state.raw<Schedule | null>(null);
	let scheduleStatus = $state.raw<ScheduleStatus | null>(null);
	let serverNext = $state<string | null>(null);
	let timeZone = $state('');
	let keepSource = $state('');
	let savedKeep = $state.raw<KeepValue>(updateKeepFromStored(0));

	async function load() {
		try {
			const [info, keep] = await Promise.all([
				fullBackups ? getBackupSchedule() : Promise.resolve(null),
				getBackupRetention()
			]);
			applySchedule(info);
			keepSource = keep.source ?? '';
			savedKeep = updateKeepFromStored(keep.count);
			draft.load(backupValues(info?.schedule, keep.count));
			loadError = '';
			loaded = true;
		} catch (err) {
			loadError = errorText(err, t('errors.server_error'));
		}
	}

	function applySchedule(info: Awaited<ReturnType<typeof getBackupSchedule>> | null) {
		if (!info) return;
		savedSchedule = info.schedule ?? savedSchedule;
		scheduleStatus = info.status ?? scheduleStatus;
		serverNext = info.next?.at ?? null;
		timeZone = info.next?.timeZone ?? timeZone;
	}

	/** The summary follows the server; the draft keeps what is typed */
	async function refreshSchedule() {
		if (!fullBackups) return;
		try {
			applySchedule(await getBackupSchedule());
		} catch {
			/* the summary keeps the last known state */
		}
	}

	async function save() {
		const refused = Object.values(errors).find(Boolean);
		if (refused) {
			draft.error = refused;
			return;
		}
		const values = $state.snapshot(draft.values) as BackupValues;
		await saveAndConfirm(
			draft,
			async (changes) => {
				const plan = savePlan(changes.map((c) => c.field));
				if (plan.schedule) await saveBackupSchedule(scheduleBody(values));
				if (plan.updateKeep) {
					await setBackupRetention(updateKeepToStored(values.updateKeep));
					savedKeep = values.updateKeep;
					keepSource = 'database';
				}
			},
			{ reload: refreshSchedule }
		);
	}

	const intervalOptions = $derived.by(() => {
		const hours: number[] = INTERVALS.map((i) => i.value);
		if (!hours.includes(v.intervalHours)) hours.push(v.intervalHours);
		return hours.sort((a, b) => a - b).map((h) => ({ value: h, label: intervalLabel(h) }));
	});
	const timeOptions = $derived([
		{ value: '', label: tf('backups_page.no_set_time_long', 'No set time: count from the last backup') },
		...timeOfDayOptions(v.timeOfDay).map((h) => ({ value: h, label: h }))
	]);
	const destinationOptions = $derived([
		{ value: 'local' as const, label: destinationLabel('local') },
		{ value: 'path' as const, label: destinationLabel('path') }
	]);
	const zone = $derived(timeZone || tf('backups_page.server_zone', "the server's time zone"));

	const keepSourceLine = $derived.by((): ValueSource | null => {
		if (draft.isChanged('updateKeep')) return null;
		if (keepSource === 'environment')
			return {
				kind: 'environment',
				text: tf(
					'backups_page.keep_from_env',
					'Set in the environment file (BACKUP_RETENTION_COUNT). Saving here overrides it.'
				)
			};
		if (keepSource === 'unset')
			return { kind: 'default', text: tf('backups_page.keep_default', 'Built-in default: keep all.') };
		return null;
	});

	// ── When the next scheduled backup runs ──
	const next = $derived(nextBackup(savedSchedule, scheduleStatus, Date.now(), serverNext));
	const nextWords = $derived(
		next.kind === 'at'
			? formatDate(next.at, {
					weekday: 'short',
					day: 'numeric',
					month: 'short',
					hour: '2-digit',
					minute: '2-digit'
				})
			: next.kind === 'soon'
				? t('settings.side.backup_soon')
				: ''
	);
	const scheduleConsequence = $derived(
		scheduleChanged
			? tf('backups_page.after_save', 'When you save, the next backup runs on the new schedule.')
			: nextWords
				? tf('backups_page.next_is', 'The next backup runs on this schedule: {when}.', { when: nextWords })
				: undefined
	);

	// ── Actions: download, make one now, test the folder ──
	let downloading = $state(false);
	let downloadLine = $state<Line>(NONE);
	let running = $state(false);
	let runLine = $state<Line>(NONE);
	let testing = $state(false);
	let testLine = $state<Line>(NONE);

	async function download() {
		downloading = true;
		downloadLine = NONE;
		try {
			const { blob, filename } = await downloadBackup();
			const url = URL.createObjectURL(blob);
			const a = document.createElement('a');
			a.href = url;
			a.download = filename;
			a.click();
			URL.revokeObjectURL(url);
			downloadLine = ok(t('settings.backup_tab.downloaded', { name: filename }));
		} catch (err) {
			downloadLine = fail(err);
		} finally {
			downloading = false;
		}
	}

	async function runNow() {
		running = true;
		runLine = NONE;
		try {
			const r = await runScheduledBackup();
			runLine = ok(`${t('backup.schedule_run_success')}: ${r?.filename ?? ''}`);
		} catch (err) {
			runLine = fail(err);
		} finally {
			running = false;
			void refreshSchedule();
		}
	}

	async function testDestination() {
		testing = true;
		testLine = NONE;
		try {
			const r = await testBackupDestination({
				destinationType: v.destinationType,
				destinationPath: v.destinationPath
			});
			testLine = ok(
				tf('backups_page.folder_ok', 'The server can write to {folder}.', { folder: r?.destination ?? '' })
			);
		} catch (err) {
			testLine = fail(err);
		} finally {
			testing = false;
		}
	}

	const savedTo = $derived(
		savedSchedule?.destinationType === 'path' && savedSchedule.destinationPath
			? savedSchedule.destinationPath
			: destinationLabel('local')
	);

	// ── Update backups: the list, and Delete older update backups now ──
	let updateBackups = $state.raw<UpdateBackup[] | null>(null);
	let reloadList = $state(0);
	let pruning = $state(false);
	const toDelete = $derived(pruneCount(updateBackups?.length ?? 0, savedKeep));

	const pruneConsequence = $derived(
		draft.isChanged('updateKeep')
			? tf('backups_page.prune_save_first', 'Save the new number to keep first.')
			: savedKeep.all
				? tf('backups_page.prune_all', '"Keep all update backups" is on, so there is nothing to delete.')
				: toDelete === 0
					? tf(
							'backups_page.prune_none',
							'There are no more than {keep} update backups. Nothing to delete.',
							{
								keep: savedKeep.count
							}
						)
					: tf(
							'backups_page.prune_some',
							'Deletes the {count} oldest update backups and keeps the newest {keep}.',
							{
								count: toDelete,
								keep: savedKeep.count
							}
						)
	);

	async function prune() {
		const keep = savedKeep.count;
		const count = toDelete;
		const yes = await confirm({
			title: tf('backups_page.prune_title', 'Delete older update backups now'),
			message: tf(
				'backups_page.prune_confirm',
				'{count} update backups will be deleted for good: all but the newest {keep}. You cannot put their files back afterwards.',
				{ count, keep }
			),
			confirmLabel: tf('backups_page.prune_button', 'Delete {count} update backups', { count }),
			danger: true
		});
		if (!yes) return;
		pruning = true;
		try {
			const r = await pruneUpdateBackups(keep);
			toast.success(t('updates.prune_done', { n: r.deleted?.length ?? 0 }));
			reloadList++;
		} catch (err) {
			toast.error(errorText(err, t('errors.server_error')));
		} finally {
			pruning = false;
		}
	}

	// ── Restore: 1 a file, 2 what to restore, 3 confirm ──
	let files = $state<FileList | null>(null);
	let picked = $state.raw<ReadonlySet<RestorePart['field']>>(new Set());
	let restoring = $state(false);
	let restoreLine = $state<Line>(NONE);
	let result = $state.raw<RestoreResult | null>(null);
	let confirming = $state(false);

	const file = $derived(files?.[0] ?? null);
	const kind = $derived(fileKind(file?.name));
	const step = $derived(restoreStep(kind, confirming));
	const hint = $derived(restoreHint(kind, picked.size));
	const startable = $derived(canRestore(kind, picked.size));
	const confirmItems = $derived(
		kind === 'json'
			? JSON_RESTORE_ITEMS.map((k) => t(k))
			: RESTORE_PARTS.filter((p) => picked.has(p.field)).map((p) => t(p.label))
	);
	const fileError = $derived(kind === 'invalid' ? t('backup.invalid_format') : '');

	/** A new file starts without the last result */
	function fileChosen() {
		result = null;
		restoreLine = NONE;
	}

	function togglePart(field: RestorePart['field'], on: boolean) {
		// A new set each time: picked is replaced, never changed in place
		// eslint-disable-next-line svelte/prefer-svelte-reactivity
		const nextSet = new Set(picked);
		if (on) nextSet.add(field);
		else nextSet.delete(field);
		picked = nextSet;
	}

	async function restore() {
		confirming = false;
		if (!file || !startable) return;
		restoring = true;
		restoreLine = NONE;
		try {
			result = await restoreBackup(file, restoreFields(kind === 'archive' ? picked : new Set()));
			const outcome = restoreOutcome(result, kind === 'archive' ? picked : new Set());
			restoreLine =
				outcome === 'ok'
					? ok(t('backup.restore_success'))
					: {
							message:
								outcome === 'none'
									? t('settings.backup_tab.restore_nothing')
									: t('settings.backup_tab.restore_partial'),
							tone: 'error'
						};
		} catch (err) {
			restoreLine = fail(err);
		} finally {
			restoring = false;
		}
	}

	// ── Summary: what a backup holds now ──
	let stats = $state.raw<BackupStats | null>(null);
	let statsError = $state('');

	async function loadStats() {
		try {
			stats = await getBackupStats();
			statsError = '';
		} catch (err) {
			statsError = errorText(err, t('errors.server_error'));
		}
	}

	const parts = $derived(partRows(stats, t));
	const partsIn = $derived(parts.filter((p) => p.included));
	const partsOut = $derived(parts.filter((p) => !p.included));
	const num = (n: number | undefined) => (stats ? formatNumber(n ?? 0) : undefined);
	const countFacts = $derived<IdFact[]>([
		{ id: 'backup-stat-users', label: t('backup.stat_users'), value: num(stats?.users) },
		{ id: 'backup-stat-settings', label: t('backup.stat_settings'), value: num(stats?.settings) },
		{ id: 'backup-stat-folders', label: t('backup.stat_folders'), value: num(stats?.folders) },
		{
			id: 'backup-stat-groups',
			label: t('backup.stat_groups'),
			value: stats ? formatNumber((stats.userGroups ?? 0) + (stats.deviceGroups ?? 0)) : undefined
		},
		{
			id: 'backup-stat-strategies',
			label: t('settings.backup_tab.stat_strategies'),
			value: num(stats?.strategies)
		},
		{
			id: 'backup-stat-uploads',
			label: t('settings.backup_tab.stat_uploads'),
			value: num(stats?.components.uploads)
		},
		{
			id: 'backup-stat-backend',
			label: t('settings.backup_tab.stat_backend'),
			value: stats
				? stats.backend === 'neodesk'
					? t('settings.backup_tab.backend_neodesk')
					: 'RustDesk'
				: undefined
		},
		{
			id: 'backup-stat-database',
			label: t('backup.stat_database'),
			value: stats ? (stats.dbType === 'postgres' ? 'PostgreSQL' : 'SQLite') : undefined
		}
	]);
	const stateFacts = $derived<Fact[]>([
		{ label: t('backup.schedule_last_run'), value: lastRun },
		{ label: t('settings.side.next_backup'), value: nextRun },
		{ label: t('settings.side.backup_kept'), value: savedToFact }
	]);

	$effect(() => {
		if (!allowed) return;
		void loadStats();
		void load();
		return onRefresh(() => {
			void loadStats();
			void refreshSchedule();
		});
	});

	const sections = $derived([
		{ id: 'backup-create', label: tf('backups_page.make_title', 'Make a backup') },
		...(fullBackups
			? [{ id: 'scheduled-backup-panel', label: tf('backups_page.schedule_title', 'Scheduled backups') }]
			: []),
		{ id: 'update-backups-panel', label: tf('backups_page.updates_title', 'Before updates') },
		...(fullBackups ? [{ id: 'backup-restore', label: t('settings.backup_tab.restore_title') }] : [])
	]);
</script>

{#snippet lastRun()}
	<Time value={scheduleStatus?.lastSuccessAt} empty={t('settings.side.backup_none')} />
{/snippet}
{#snippet nextRun()}
	{#if next.kind === 'off'}
		<Status tone="neutral" label={t('settings.side.backup_off')} />
	{:else if next.kind === 'soon'}
		<Status tone="waiting" label={t('settings.side.backup_soon')} />
	{:else if next.kind === 'at'}
		<Time value={next.at} relative={false} />
	{:else}
		<Time value={null} />
	{/if}
{/snippet}
{#snippet savedToFact()}
	{#if savedSchedule?.destinationType === 'path' && savedSchedule.destinationPath}
		<ValueCopy value={savedSchedule.destinationPath} copy={false} />
	{:else}
		{destinationLabel('local')}
	{/if}
{/snippet}

<SettingsPage
	id="server-backups"
	title={t('nav.server_backups')}
	description={tf(
		'backups_page.purpose',
		'Copies of everything this server needs: made on a schedule, before each update, or by hand. Restore one here.'
	)}
	need="server.config"
	{sections}
	summaryLast
>
	{#snippet summary()}
		<section class="summary-part" aria-labelledby="backup-contents-title">
			<h2 id="backup-contents-title">{tf('backups_page.holds_title', 'What a backup holds now')}</h2>
			<p class="summary-note">
				{tf('backups_page.holds_note', 'As this server would write it now, on this kind of install.')}
			</p>
			{#if statsError}
				<Status tone="danger" label={statsError} />
			{:else if !stats}
				<States kind="loading" />
			{:else}
				{#if partsIn.length}
					<ul class="parts" aria-label={tf('backups_page.parts_in', 'In a backup')}>
						{#each partsIn as p (p.id)}
							<li id={`backup-part-${p.id}`} class="part is-in">
								<span class="part-mark" aria-hidden="true">✓</span>
								<span>{p.label}</span>
							</li>
						{/each}
					</ul>
				{/if}
				{#if partsOut.length}
					<h3 class="parts-title">{tf('backups_page.parts_out', 'Left out')}</h3>
					<ul class="parts">
						{#each partsOut as p (p.id)}
							<li id={`backup-part-${p.id}`} class="part is-out">
								<span>{p.label}</span>
								<span class="part-reason">{p.reason}</span>
							</li>
						{/each}
					</ul>
				{/if}
				<Notice tone="warning" message={securityWarning(stats, t, i18n.locale)} />
				<IdFacts items={countFacts} label={tf('backups_page.counts', 'Counts')} />
			{/if}
		</section>
		{#if fullBackups}
			<section class="summary-part" aria-labelledby="backup-side-title">
				<h2 id="backup-side-title">{tf('backups_page.schedule_title', 'Scheduled backups')}</h2>
				<Facts items={stateFacts} />
				{#if scheduleStatus?.lastError}
					<p class="state-error">{t('settings.side.backup_failed', { error: scheduleStatus.lastError })}</p>
				{/if}
			</section>
		{/if}
	{/snippet}

	<SettingsGroup
		id="backup-create"
		title={tf('backups_page.make_title', 'Make a backup')}
		description={tf(
			'backups_page.make_note',
			'One .tar.gz file. You can restore it on this server or on a new one. What it holds is listed under "What a backup holds now".'
		)}
	>
		{#if !fullBackups}
			<div class="group-pad">
				<Notice id="backup-super-admin-only" message={t('settings.backup_tab.super_admin_only')} />
			</div>
		{:else}
			<div class="action-row">
				<div class="action-text">
					<p class="action-name">{tf('backups_page.download_title', 'Download a backup')}</p>
					<p class="action-help" id="backup-download-help">
						{tf('backups_page.download_help', 'Makes a backup and saves it on this computer.')}
					</p>
				</div>
				<div class="action-end">
					<Button
						id="backup-download-btn"
						variant={draft.dirty ? 'secondary' : 'primary'}
						icon="download"
						busy={downloading}
						aria-describedby="backup-download-help"
						onclick={download}>{downloading ? t('backup.creating') : t('backup.download')}</Button
					>
					<FormStatus message={downloadLine.message} tone={downloadLine.tone} />
				</div>
			</div>
			<div class="action-row">
				<div class="action-text">
					<p class="action-name">{t('settings.backup_tab.run_now')}</p>
					<p class="action-help" id="backup-run-help">
						{tf(
							'backups_page.run_help',
							'Saves one on the server, where scheduled backups go ({folder}). The oldest beyond the number to keep are deleted.',
							{ folder: savedTo }
						)}
					</p>
				</div>
				<div class="action-end">
					<Button
						id="scheduled-backup-run-btn"
						busy={running}
						aria-describedby="backup-run-help"
						onclick={runNow}>{running ? t('backup.creating') : t('settings.backup_tab.run_now')}</Button
					>
					<FormStatus message={runLine.message} tone={runLine.tone} />
				</div>
			</div>
		{/if}
	</SettingsGroup>

	{#if !loaded}
		{#if loadError}
			<States kind="error" message={loadError} onretry={() => void load()} />
		{:else}
			<States kind="loading" />
		{/if}
	{:else}
		{#if fullBackups}
			<SettingsGroup
				id="scheduled-backup-panel"
				title={tf('backups_page.schedule_title', 'Scheduled backups')}
				description={tf(
					'backups_page.schedule_note',
					'The server makes a full backup on its own and checks that it can be read back.'
				)}
				consequence={scheduleConsequence}
			>
				<SettingRow
					id="scheduled-backup-enabled"
					label={tf('backups_page.enabled', 'Make backups on a schedule')}
					checked={v.enabled}
					onchange={(on) => draft.set('enabled', on)}
					changed={draft.isChanged('enabled')}
				/>
				<div id="scheduled-backup-details" hidden={!v.enabled}>
					<SettingRow
						id="scheduled-backup-interval"
						label={tf('backups_page.how_often', 'How often')}
						help={tf(
							'backups_page.how_often_help',
							'Daily and weekly backups can run at a time of day. Every other choice counts from the last backup.'
						)}
						changed={draft.isChanged('intervalHours')}
					>
						{#snippet control(f)}
							<Select
								id={f.id}
								describedby={f.describedby}
								options={intervalOptions}
								value={v.intervalHours}
								onchange={(e) => {
									const n = withInterval(v.timeOfDay, Number(e.currentTarget.value));
									draft.set('intervalHours', n.intervalHours);
									if (n.timeOfDay !== v.timeOfDay) draft.set('timeOfDay', n.timeOfDay);
								}}
							/>
						{/snippet}
					</SettingRow>
					<div class="shown-when" hidden={!takesTimeOfDay(v.intervalHours)}>
						<SettingRow
							id="scheduled-backup-time"
							label={tf('backups_page.time_of_day', 'Time of day')}
							help={tf(
								'backups_page.time_help',
								"In the server's time zone ({zone}). With no set time, the next backup counts from the last one.",
								{ zone }
							)}
							changed={draft.isChanged('timeOfDay')}
						>
							{#snippet control(f)}
								<Select
									id={f.id}
									describedby={f.describedby}
									options={timeOptions}
									value={v.timeOfDay}
									onchange={(e) => draft.set('timeOfDay', e.currentTarget.value)}
								/>
							{/snippet}
						</SettingRow>
					</div>
					<SettingRow
						id="scheduled-backup-retention"
						label={t('backup.schedule_retention')}
						help={tf(
							'backups_page.keep_help',
							'From 1 to 365. After each backup, the oldest beyond this number are deleted.'
						)}
						changed={draft.isChanged('retentionCount')}
						error={errors.retentionCount}
					>
						{#snippet control(f)}
							<Input
								id={f.id}
								describedby={f.describedby}
								invalid={f.invalid}
								type="number"
								width="short"
								min="1"
								max="365"
								step="1"
								value={v.retentionCount ?? ''}
								oninput={(e) => {
									const raw = e.currentTarget.value;
									draft.set('retentionCount', raw === '' ? null : Number(raw));
								}}
							/>
						{/snippet}
					</SettingRow>
					<SettingRow
						id="scheduled-backup-destination-type"
						label={tf('backups_page.where', 'Where to save')}
						help={tf(
							'backups_page.where_help',
							'The NeoDesk data folder, or a folder of your own. A network share must be mounted by the operating system first.'
						)}
						changed={draft.isChanged('destinationType')}
					>
						{#snippet control(f)}
							<div class="inline-controls">
								<Select
									id={f.id}
									describedby={f.describedby}
									options={destinationOptions}
									value={v.destinationType}
									onchange={(e) =>
										draft.set('destinationType', e.currentTarget.value === 'path' ? 'path' : 'local')}
								/>
								<Button id="scheduled-backup-test-btn" busy={testing} onclick={testDestination}
									>{tf('backups_page.test_folder', 'Test the folder')}</Button
								>
							</div>
							<FormStatus message={testLine.message} tone={testLine.tone} />
						{/snippet}
					</SettingRow>
					<div class="shown-when" hidden={v.destinationType !== 'path'}>
						<SettingRow
							id="scheduled-backup-path"
							label={tf('backups_page.folder', 'Folder on the server')}
							help={tf(
								'backups_page.folder_help',
								'The full path, for example /mnt/backups. The NeoDesk program folder cannot be used.'
							)}
							changed={draft.isChanged('destinationPath')}
							error={errors.destinationPath}
						>
							{#snippet control(f)}
								<Input
									id={f.id}
									describedby={f.describedby}
									invalid={f.invalid}
									class="mono"
									spellcheck="false"
									value={v.destinationPath}
									oninput={(e) => draft.set('destinationPath', e.currentTarget.value)}
								/>
							{/snippet}
						</SettingRow>
					</div>
				</div>
			</SettingsGroup>
		{/if}

		<SettingsGroup
			id="update-backups-panel"
			title={tf('backups_page.updates_title', 'Before updates')}
			description={t('settings.updates_tab.backups_note')}
			consequence={tf('backups_page.updates_consequence', 'A new number applies after the next update.')}
		>
			<SettingRow
				id="backup-retention-input"
				label={t('settings.updates_tab.retention_label')}
				help={tf(
					'backups_page.update_keep_help',
					'These are separate from full backups. After each update the oldest beyond this number are deleted.'
				)}
				group
				changed={draft.isChanged('updateKeep')}
				source={keepSourceLine}
			>
				{#snippet control(f)}
					<KeepCount
						id="backup-retention"
						labelledby={f.labelledby}
						describedby={f.describedby}
						all={v.updateKeep.all}
						count={v.updateKeep.count}
						onchange={(k) => draft.set('updateKeep', k)}
						allLabel={tf('backups_page.keep_all_updates', 'Keep all update backups')}
						countLabel={tf('backups_page.keep_this_many', 'Keep this many')}
						error={errors.updateKeep}
					/>
				{/snippet}
			</SettingRow>
			<UpdateBackups reload={reloadList} onlist={(list) => (updateBackups = list)} />
		</SettingsGroup>
	{/if}

	{#if fullBackups}
		<SettingsGroup
			id="backup-restore"
			title={t('settings.backup_tab.restore_title')}
			description={tf(
				'backups_page.restore_note',
				'Put data from a backup file back on this server. You choose what, and nothing changes before you confirm.'
			)}
		>
			<ol class="restore-steps">
				<li class="restore-step" aria-current={step === 'file' ? 'step' : undefined}>
					<h3 class="step-title">
						<span class="step-n" aria-hidden="true">1</span>{tf(
							'backups_page.step_file',
							'Choose a backup file'
						)}
					</h3>
					<div class="step-body">
						<label class="step-label" for="restore-file-input">{t('settings.backup_tab.restore_file')}</label>
						<FilePicker
							id="restore-file-input"
							describedby="restore-file-hint"
							accept=".gz,.tar.gz,.json"
							aria-controls="restore-options"
							invalid={!!fileError}
							bind:files
							onchange={fileChosen}
						/>
						<p class="step-help" id="restore-file-hint">{t('settings.backup_tab.restore_file_hint')}</p>
						{#if fileError}<p class="step-error" role="alert">{fileError}</p>{/if}
					</div>
				</li>
				<li class="restore-step" aria-current={step === 'parts' ? 'step' : undefined}>
					<h3 class="step-title">
						<span class="step-n" aria-hidden="true">2</span>{tf(
							'backups_page.step_parts',
							'Choose what to restore'
						)}
					</h3>
					<div class="step-body">
						<fieldset id="restore-options" class="restore-options" hidden={kind !== 'archive'}>
							<legend class="sr-only">{t('settings.backup_tab.restore_what')}</legend>
							{#each RESTORE_PARTS as part (part.field)}
								<Checkbox
									id={part.id}
									label={t(part.label)}
									hint={t(part.desc)}
									checked={picked.has(part.field)}
									onchange={(e) => togglePart(part.field, e.currentTarget.checked)}
								/>
							{/each}
						</fieldset>
						{#if kind === 'json'}
							<Notice message={t('settings.backup_tab.restore_json_note')} />
						{/if}
						{#if hint}
							<p class="step-help" role="status">{t(hint)}</p>
						{/if}
						{#if kind === 'archive' || kind === 'json'}
							<div class="inline-controls">
								<Button
									id="restore-start-btn"
									disabled={!startable}
									busy={restoring}
									onclick={() => (confirming = true)}
									>{restoring
										? t('settings.backup_tab.restoring')
										: tf('backups_page.review', 'Review and restore…')}</Button
								>
								<FormStatus message={restoreLine.message} tone={restoreLine.tone} />
							</div>
						{/if}
					</div>
				</li>
				<li class="restore-step" aria-current={step === 'confirm' ? 'step' : undefined}>
					<h3 class="step-title">
						<span class="step-n" aria-hidden="true">3</span>{tf('backups_page.step_confirm', 'Confirm')}
					</h3>
					<div class="step-body">
						<p class="step-help">
							{tf(
								'backups_page.step_confirm_help',
								'The console names what will be replaced and what is lost. Nothing changes before you confirm.'
							)}
						</p>
					</div>
				</li>
			</ol>
			{#if result}
				<div class="restore-result group-pad" id="restore-result">
					{#if result.restored.length || result.skipped.length || result.backupDate}
						<dl class="facts">
							{#if result.restored.length}
								<dt>{t('backup.restored')}</dt>
								<dd>{result.restored.map((p) => restoredPartLabel(p, t)).join(', ')}</dd>
							{/if}
							{#if result.skipped.length}
								<dt>{t('backup.skipped')}</dt>
								<dd>{result.skipped.map((p) => restoredPartLabel(p, t)).join(', ')}</dd>
							{/if}
							{#if result.backupDate}
								<dt>{t('backup.backup_date')}</dt>
								<dd><Time value={result.backupDate} relative={false} /></dd>
							{/if}
						</dl>
					{/if}
					{#if result.warnings.length}
						<Notice tone="warning">
							{#each result.warnings as w, i (i)}<p>{w}</p>{/each}
						</Notice>
					{/if}
					{#if result.requiresRestart}
						<Notice tone="warning" message={t('backup.restart_required')} />
					{/if}
				</div>
			{/if}
		</SettingsGroup>
	{/if}

	{#if loaded && updateBackups !== null}
		<div id="backup-danger">
			<DangerZone
				title={tf('backups_page.danger_title', 'Danger zone')}
				rows={[
					{
						title: tf('backups_page.prune_title', 'Delete older update backups now'),
						consequence: pruneConsequence,
						action: tf('backups_page.prune_action', 'Delete older update backups…'),
						disabled: toDelete === 0 || draft.isChanged('updateKeep'),
						busy: pruning,
						onclick: () => void prune()
					}
				]}
			/>
		</div>
	{/if}

	{#snippet bar()}<ChangesBar {draft} onsave={save} />{/snippet}
</SettingsPage>

{#if confirming && file}
	<Dialog title={t('settings.backup_tab.confirm_title')} onclose={() => (confirming = false)}>
		<p>{t('settings.backup_tab.confirm_intro', { file: file.name })}</p>
		<ul class="confirm-list">
			{#each confirmItems as item, i (i)}<li>{item}</li>{/each}
		</ul>
		<p>
			{tf(
				'backups_page.confirm_lost',
				'What is on this server now in these parts and not in the backup is lost, for example accounts or settings added since the backup was made.'
			)}
		</p>
		<p>{t('settings.backup_tab.confirm_undo')}</p>
		{#if restoreNeedsRestart(kind, picked)}
			<Notice tone="warning" message={t('backup.restart_required')} />
		{/if}
		{#snippet footer()}
			<Button onclick={() => (confirming = false)}>{t('actions.cancel')}</Button>
			<Button variant="danger" onclick={restore}>{t('settings.backup_tab.confirm_button')}</Button>
		{/snippet}
	</Dialog>
{/if}

<style>
	.summary-part {
		display: flex;
		flex-direction: column;
		gap: var(--space-2);
	}
	.summary-part + .summary-part {
		margin-top: var(--space-5);
	}
	.summary-part h2 {
		margin: 0;
		font-size: var(--text-md);
	}
	.summary-note {
		margin: 0;
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	.parts {
		display: flex;
		flex-direction: column;
		gap: var(--space-1);
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.part {
		display: flex;
		flex-wrap: wrap;
		gap: 0 var(--space-2);
		min-width: 0;
	}
	.part-mark {
		color: var(--ok);
	}
	.part.is-out > span:first-child {
		color: var(--text-muted);
	}
	.part-reason {
		flex-basis: 100%;
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	.parts-title {
		margin: var(--space-2) 0 0;
		font-size: var(--text-sm);
		font-weight: 600;
	}
	.state-error {
		margin: 0;
		color: var(--danger);
	}

	.group-pad {
		padding: var(--space-3) var(--row-pad-x);
	}
	.action-row {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		justify-content: space-between;
		gap: var(--space-2) var(--space-4);
		padding: var(--space-3) var(--row-pad-x);
		border-bottom: 1px solid var(--rule);
	}
	.action-text {
		flex: 1 1 280px;
		min-width: 0;
	}
	.action-text p {
		margin: 0;
	}
	.action-name {
		font-weight: 600;
	}
	.action-help {
		max-width: var(--measure);
		color: var(--text-muted);
		font-size: var(--text-sm);
		overflow-wrap: anywhere;
	}
	.action-end {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-2);
	}
	.inline-controls {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-2);
	}
	#scheduled-backup-details[hidden],
	.shown-when[hidden] {
		display: none;
	}

	.restore-steps {
		display: flex;
		flex-direction: column;
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.restore-step {
		display: flex;
		flex-direction: column;
		gap: var(--space-2);
		padding: var(--space-3) var(--row-pad-x);
		border-bottom: 1px solid var(--rule);
		border-inline-start: 3px solid transparent;
	}
	.restore-step[aria-current='step'] {
		border-inline-start-color: var(--accent);
	}
	.step-title {
		display: flex;
		align-items: center;
		gap: var(--space-2);
		margin: 0;
		font-size: var(--text-base);
		font-weight: 600;
	}
	.step-n {
		display: inline-grid;
		place-items: center;
		width: 1.5rem;
		height: 1.5rem;
		border: 1px solid var(--border);
		border-radius: 50%;
		font-size: var(--text-sm);
	}
	.restore-step[aria-current='step'] .step-n {
		border-color: var(--accent);
		color: var(--accent);
	}
	.step-body {
		display: flex;
		flex-direction: column;
		gap: var(--space-2);
		padding-inline-start: calc(1.5rem + var(--space-2));
		min-width: 0;
	}
	.step-label {
		font-weight: 600;
	}
	.step-help {
		margin: 0;
		max-width: var(--measure);
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	.step-error {
		margin: 0;
		color: var(--danger);
		font-size: var(--text-sm);
	}
	.restore-options {
		display: flex;
		flex-direction: column;
		gap: var(--space-3);
		margin: 0;
		padding: 0;
		border: 0;
	}
	.restore-options[hidden] {
		display: none;
	}
	.restore-result {
		display: flex;
		flex-direction: column;
		gap: var(--space-3);
	}
	.confirm-list {
		margin: var(--space-2) 0;
		padding-inline-start: 1.25em;
	}
	@media (max-width: 480px) {
		.step-body {
			padding-inline-start: 0;
		}
	}
</style>
