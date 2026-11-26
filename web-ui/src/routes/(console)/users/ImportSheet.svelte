<!--
  Import accounts from a CSV file (user.create). The template names the
  columns; the file goes to the server, which checks every line and
  changes nothing (POST /api/users/import/preview). The sheet lists the
  lines with what is wrong with each; the lines without problems are
  chosen, and Import makes those accounts (POST /api/users/import), which
  checks them again. How they get a password: an invite by mail (needs the
  email settings), a temporary password each (shown once, with a download),
  or none yet. Closing after an import reloads the list.
-->
<script lang="ts">
	import { untrack } from 'svelte';
	import {
		importUsers,
		previewImport,
		type ImportMode,
		type ImportPreview,
		type ImportResult,
		type ImportRow
	} from '#lib/api/users.ts';
	import { plural, t } from '#lib/i18n/i18n.svelte.ts';
	import { importTemplate, saveCsv, toCsv } from '#lib/people/userList.ts';
	import Button from '#lib/ui/Button.svelte';
	import ChoiceList from '#lib/ui/ChoiceList.svelte';
	import Field from '#lib/ui/Field.svelte';
	import FilePicker from '#lib/ui/FilePicker.svelte';
	import Notice from '#lib/ui/Notice.svelte';
	import Sheet from '#lib/ui/Sheet.svelte';
	import Status from '#lib/ui/Status.svelte';
	import Table from '#lib/ui/Table.svelte';
	import type { Column } from '#lib/ui/table.ts';
	import { ICONS } from '#lib/util/display.ts';

	interface Props {
		/** The console can send mail, so invites can go out */
		mailReady: boolean;
		onclose: () => void;
		/** Accounts were made: the list loads again */
		ondone: () => void;
	}

	let { mailReady, onclose, ondone }: Props = $props();

	const MAX_BYTES = 1 << 20;

	let mode = $state<ImportMode>(untrack(() => (mailReady ? 'invite' : 'temporary')));
	let files = $state<FileList | null>(null);
	let csv = $state('');
	let phase = $state<'choose' | 'checking' | 'preview' | 'running' | 'done'>('choose');
	let preview = $state.raw<ImportPreview | null>(null);
	let selection = $state.raw(new Set<string>());
	let results = $state.raw<ImportResult[]>([]);
	let error = $state('');

	const message = (err: unknown) =>
		err instanceof Error && err.message ? err.message : t('errors.server_error');

	const modes = $derived([
		{
			value: 'invite' as const,
			title: t('users.import_mode_invite'),
			description: mailReady ? t('users.import_mode_invite_hint') : t('users.import_mode_invite_off'),
			disabled: !mailReady
		},
		{
			value: 'temporary' as const,
			title: t('users.import_mode_temporary'),
			description: t('users.import_mode_temporary_hint')
		},
		{
			value: 'none' as const,
			title: t('users.import_mode_none'),
			description: t('users.import_mode_none_hint')
		}
	]);

	// A chosen file is read and checked
	$effect(() => {
		const file = files?.[0];
		if (!file) return;
		untrack(() => void read(file));
	});

	async function read(file: File) {
		error = '';
		if (file.size > MAX_BYTES) {
			error = t('users.import_too_large', { size: '1 MB' });
			return;
		}
		try {
			csv = await file.text();
		} catch {
			error = t('users.import_unreadable');
			return;
		}
		await check();
	}

	async function check() {
		phase = 'checking';
		error = '';
		try {
			const out = await previewImport(csv, mode);
			preview = out;
			selection = new Set(out.rows.filter((r) => !r.problems.length).map((r) => String(r.line)));
			phase = 'preview';
		} catch (err) {
			preview = null;
			error = message(err);
			phase = 'choose';
		}
	}

	// Another way to get a password checks the file again (invites need addresses)
	$effect(() => {
		void mode;
		untrack(() => {
			if (csv && phase !== 'running' && phase !== 'done') void check();
		});
	});

	const good = $derived(new Set((preview?.rows ?? []).filter((r) => !r.problems.length).map((r) => r.line)));
	const chosen = $derived([...selection].map(Number).filter((line) => good.has(line)));

	async function run() {
		phase = 'running';
		error = '';
		try {
			results = await importUsers(csv, mode, chosen);
			phase = 'done';
		} catch (err) {
			error = message(err);
			phase = 'preview';
		}
	}

	const created = $derived(results.filter((r) => r.outcome === 'created'));
	const notMade = $derived(results.filter((r) => r.outcome === 'refused' || r.outcome === 'failed'));
	const warned = $derived(created.filter((r) => r.warning));
	const passwords = $derived(created.filter((r) => r.password));

	function close() {
		if (created.length) ondone();
		else onclose();
	}

	function savePasswords() {
		saveCsv(
			'temporary-passwords.csv',
			toCsv([
				[t('users.username'), t('users.import_password')],
				...passwords.map((r) => [r.username, r.password])
			])
		);
	}

	const columns = $derived<Column<ImportRow>[]>([
		{ id: 'line', label: t('users.import_line'), cell: lineCell, align: 'num', sortValue: (r) => r.line },
		{
			id: 'username',
			label: t('users.username'),
			cell: nameCell,
			primary: true,
			sortValue: (r) => r.username
		},
		{ id: 'email', label: t('users.email'), cell: emailCell },
		{ id: 'role', label: t('users.role'), cell: roleCell },
		{ id: 'check', label: t('users.import_check'), cell: checkCell, sortValue: (r) => r.problems.length }
	]);
	const passwordColumns = $derived<Column<ImportResult>[]>([
		{ id: 'username', label: t('users.username'), cell: madeNameCell, primary: true },
		{ id: 'password', label: t('users.import_password'), cell: passwordCell }
	]);
</script>

{#snippet madeNameCell(r: ImportResult)}{r.username}{/snippet}
{#snippet passwordCell(r: ImportResult)}<code>{r.password}</code>{/snippet}

{#snippet lineCell(r: ImportRow)}{r.line}{/snippet}
{#snippet nameCell(r: ImportRow)}{r.username || '–'}{#if r.first_name || r.last_name}<span class="cell-sub"
			>{[r.first_name, r.last_name].filter(Boolean).join(' ')}</span
		>{/if}{/snippet}
{#snippet emailCell(r: ImportRow)}{r.email}{/snippet}
{#snippet roleCell(r: ImportRow)}{r.role || 'viewer'}{#if r.organization}<span class="cell-sub"
			>{r.organization}</span
		>{/if}{/snippet}
{#snippet checkCell(r: ImportRow)}
	{#if r.problems.length}
		<ul class="problems">
			{#each r.problems as p, i (i)}<li><Status tone="danger" label={p} /></li>{/each}
		</ul>
	{:else}<Status tone="good" label={t('users.import_ok')} />{/if}
{/snippet}

<Sheet
	title={t('users.import_title')}
	subtitle={t('users.import_intro')}
	wide
	blocking={phase === 'running'}
	onclose={close}
>
	{#if phase === 'done'}
		<div class="import-done" role="status">
			<p class="lead">
				{plural(created.length, { one: t('users.import_done_one'), other: t('users.import_done_many') })}
			</p>
			{#if created.length && mode === 'invite'}<p>{t('users.import_invites_sent')}</p>{/if}
			{#if created.length && mode === 'none'}<Notice message={t('users.import_none_next')} />{/if}
			{#if passwords.length}
				<Notice
					tone="warning"
					title={t('users.import_passwords_title')}
					message={t('users.import_passwords_hint')}
				/>
				<div class="passwords">
					<Table
						rows={passwords}
						columns={passwordColumns}
						key={(r) => String(r.line)}
						label={t('users.import_passwords_title')}
					/>
				</div>
			{/if}
			{#if warned.length}
				<h3>{t('users.import_warnings')}</h3>
				<ul class="result-list">
					{#each warned as r (r.line)}<li><strong>{r.username}</strong>: {r.warning}</li>{/each}
				</ul>
			{/if}
			{#if notMade.length}
				<h3>{t('users.import_not_made')}</h3>
				<ul class="result-list">
					{#each notMade as r (r.line)}<li>
							<strong>{t('users.import_line_named', { line: r.line, username: r.username || '–' })}</strong>: {r.error}
						</li>{/each}
				</ul>
			{/if}
		</div>
	{:else}
		<section class="import-step">
			<p>{t('users.import_columns')}</p>
			<div>
				<Button
					size="sm"
					icon={ICONS.download}
					class="import-template"
					onclick={() => saveCsv('users-import.csv', importTemplate())}>{t('users.import_template')}</Button
				>
			</div>
		</section>

		<ChoiceList choices={modes} label={t('users.import_mode')} bind:value={mode} />

		<Field label={t('users.import_file')} hint={t('users.import_file_hint')} error={error || undefined}>
			{#snippet children(f)}<FilePicker
					{...f}
					class="import-file"
					accept=".csv,text/csv,text/plain"
					bind:files
				/>{/snippet}
		</Field>

		{#if phase === 'checking'}
			<p class="text-muted" aria-live="polite">{t('users.import_checking')}</p>
		{:else if preview}
			<div class="import-preview" aria-live="polite">
				<p>
					{t('users.import_summary', { ready: preview.ready, count: preview.rows.length })}
					{#if preview.ready < preview.rows.length}{t('users.import_problems')}{/if}
				</p>
				{#if preview.ignored_columns.length}
					<Notice message={t('users.import_ignored', { columns: preview.ignored_columns.join(', ') })} />
				{/if}
				<Table
					rows={preview.rows}
					{columns}
					key={(r) => String(r.line)}
					label={t('users.import_title')}
					selectable
					bind:selection
					selectLabel={(r) => t('users.import_include', { username: r.username || String(r.line) })}
					emptyText={t('users.import_empty_preview')}
				/>
			</div>
		{/if}
	{/if}

	{#snippet footer()}
		{#if phase === 'done'}
			{#if passwords.length}<Button icon={ICONS.download} class="save-passwords" onclick={savePasswords}
					>{t('users.import_passwords_download')}</Button
				>{/if}
			<Button variant="primary" onclick={close}>{t('actions.close')}</Button>
		{:else}
			<Button class="sheet-footer-start" onclick={close} disabled={phase === 'running'}
				>{t('actions.cancel')}</Button
			>
			<Button
				class="import-run"
				variant="primary"
				icon={ICONS.upload}
				busy={phase === 'running'}
				disabled={phase !== 'preview' || !chosen.length}
				onclick={() => void run()}
				>{plural(chosen.length, {
					one: t('users.import_run_one'),
					other: t('users.import_run_many')
				})}</Button
			>
		{/if}
	{/snippet}
</Sheet>

<style>
	.import-step,
	.import-preview,
	.import-done {
		display: flex;
		flex-direction: column;
		gap: var(--space-3);
		margin-bottom: var(--space-4);
	}
	.import-step p,
	.import-preview p,
	.import-done p {
		margin: 0;
	}
	.import-done .lead {
		font-weight: 600;
	}
	.import-done h3 {
		margin: var(--space-2) 0 0;
		font-size: var(--text-base);
	}
	.problems,
	.result-list {
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.result-list li + li {
		margin-top: var(--space-1);
	}
	.import-preview :global(.cell-sub) {
		display: block;
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	.passwords code {
		user-select: all;
		overflow-wrap: anywhere;
	}
</style>
