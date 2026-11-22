<!--
  Files (?tab=files): the folders the console may open on its machine, to
  look at only: nothing here creates, changes, moves or deletes a file.
  A path bar, Up one level and Refresh above the list; a click on a folder
  opens it, a click on a file opens it in a read-only sheet, a link opens
  what it points to. Beside the list, the folders the console may open,
  each one click away.
-->
<script lang="ts">
	import { untrack } from 'svelte';
	import {
		getServerInfo,
		listFiles,
		readFile,
		type FileContent,
		type FileEntry
	} from '#lib/api/serverManagement.ts';
	import { formatBytes } from '#lib/i18n/format.ts';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import { countEntries, entryType, permsText, upFrom } from '#lib/serverarea/management.ts';
	import Button from '#lib/ui/Button.svelte';
	import CountLine from '#lib/ui/CountLine.svelte';
	import Empty from '#lib/ui/Empty.svelte';
	import Input from '#lib/ui/Input.svelte';
	import ListRow from '#lib/ui/ListRow.svelte';
	import RowList from '#lib/ui/RowList.svelte';
	import States from '#lib/ui/States.svelte';
	import Table from '#lib/ui/Table.svelte';
	import Tag from '#lib/ui/Tag.svelte';
	import Time from '#lib/ui/Time.svelte';
	import ValueCopy from '#lib/ui/ValueCopy.svelte';
	import type { Column } from '#lib/ui/table.ts';
	import { toast } from '#lib/ui/toasts.svelte.ts';
	import { ICONS } from '#lib/util/display.ts';
	import FileViewer from './FileViewer.svelte';

	const uid = $props.id();

	let cwd = $state('');
	let pathInput = $state('');
	let entries = $state.raw<FileEntry[]>([]);
	let loading = $state(true);
	let loadError = $state<string | null>(null);

	let roots = $state.raw<string[] | null>(null);
	let rootsError = $state(false);

	let viewing = $state.raw<{ file: FileContent; requested: string } | null>(null);

	let inflight: AbortController | null = null;

	async function refresh(path?: string) {
		const target = path || pathInput.trim() || cwd || '/';
		inflight?.abort();
		const ctrl = new AbortController();
		inflight = ctrl;
		loading = true;
		loadError = null;
		try {
			const data = await listFiles(target, ctrl.signal);
			if (inflight !== ctrl) return;
			cwd = data.cwd || target;
			pathInput = cwd;
			entries = data.entries;
		} catch (err) {
			if (err instanceof DOMException && err.name === 'AbortError') return;
			if (inflight !== ctrl) return;
			pathInput = target;
			entries = [];
			loadError = t('server_mgmt.files_open_error', { error: message(err) });
		} finally {
			if (inflight === ctrl) loading = false;
		}
	}

	const message = (err: unknown) => (err instanceof Error ? err.message : String(err));

	// Start in the first folder the console may open
	$effect(() => {
		untrack(() => {
			void (async () => {
				let start = '/';
				try {
					const info = await getServerInfo();
					roots = info.fileRoots;
					if (info.fileRoots[0]) start = info.fileRoots[0];
				} catch {
					rootsError = true;
					roots = [];
				}
				await refresh(start);
			})();
		});
	});

	/* ── Opening ── */

	async function openFile(path: string) {
		try {
			const file = await readFile(path);
			viewing = { file, requested: path };
		} catch (err) {
			toast.error(t('server_mgmt.read_failed_reason', { error: message(err) }));
		}
	}

	/** A link opens what it points to: a folder, or else the file */
	async function openLink(path: string) {
		try {
			const data = await listFiles(path);
			cwd = data.cwd || path;
			pathInput = cwd;
			entries = data.entries;
			loadError = null;
		} catch {
			await openFile(path);
		}
	}

	function openEntry(e: FileEntry) {
		const type = entryType(e);
		if (type === 'dir') void refresh(e.path);
		else if (type === 'file') void openFile(e.path);
		else void openLink(e.path);
	}

	/* ── Table ── */

	const counts = $derived(countEntries(entries));

	const columns = $derived<Column<FileEntry>[]>([
		{
			id: 'name',
			label: t('server_mgmt.files_name'),
			cell: nameCell,
			primary: true,
			class: 'col-name'
		},
		{ id: 'size', label: t('server_mgmt.files_size'), cell: sizeCell, align: 'num', class: 'col-size' },
		{ id: 'mtime', label: t('server_mgmt.files_mtime'), cell: timeCell, class: 'col-time' },
		{ id: 'perms', label: t('server_mgmt.files_perms'), cell: permsCell, class: 'col-perms' }
	]);
</script>

{#snippet nameCell(e: FileEntry)}
	{@const type = entryType(e)}
	{#if type === 'dir'}<span class="dir-name">{e.name}/</span>{:else}<span class="file-name">{e.name}</span
		>{#if type === 'symlink'}
			<Tag label={t('server_mgmt.files_link')} />{/if}{/if}
{/snippet}

{#snippet sizeCell(e: FileEntry)}
	{#if entryType(e) !== 'dir'}{formatBytes(e.size ?? 0) || '0 B'}{/if}
{/snippet}

{#snippet timeCell(e: FileEntry)}<Time value={e.mtime} />{/snippet}

{#snippet permsCell(e: FileEntry)}
	{@const p = permsText(e.mode)}
	{#if p}<ValueCopy value={p} copy={false} />{:else}<Empty />{/if}
{/snippet}

<div class="page-cols">
	<section class="section page-main" aria-label={t('server_mgmt.tab_files')}>
		<form
			class="pathbar"
			role="search"
			onsubmit={(e) => {
				e.preventDefault();
				void refresh();
			}}
		>
			<label class="label" for="{uid}-path">{t('server_mgmt.files_path_label')}</label>
			<div class="pathrow">
				<Input
					id="{uid}-path"
					class="path-input"
					spellcheck="false"
					autocomplete="off"
					bind:value={pathInput}
				/>
				<Button type="submit" class="files-go" icon={ICONS.files}>{t('server_mgmt.files_go')}</Button>
			</div>
		</form>
		<div class="toolbar">
			<Button
				class="files-up"
				disabled={!cwd || upFrom(cwd) === cwd}
				onclick={() => void refresh(upFrom(cwd))}>{t('server_mgmt.files_up')}</Button
			>
			<Button class="files-refresh" icon={ICONS.refresh} onclick={() => void refresh(cwd)}
				>{t('actions.refresh')}</Button
			>
		</div>
		<div class="count" aria-live="polite">
			{#if !loading && !loadError}<CountLine text={t('server_mgmt.files_count')} values={counts} />{/if}
		</div>
		<div class="files-table">
			<Table
				rows={entries}
				{columns}
				key={(e) => e.path}
				label={t('server_mgmt.tab_files')}
				onrowclick={openEntry}
				rowClass={(e) => `type-${entryType(e)}`}
				{loading}
				loadingText={t('server_mgmt.files_loading')}
				error={loadError}
				onretry={() => void refresh()}
				emptyText={t('server_mgmt.files_empty')}
			/>
		</div>
	</section>

	<aside class="page-side" aria-labelledby="{uid}-roots">
		<section class="section">
			<div class="section-head"><h2 id="{uid}-roots">{t('server_mgmt.files_roots_title')}</h2></div>
			<p class="section-note">{t('server_mgmt.files_roots_note')}</p>
			{#if roots === null}
				<States kind="loading" message={t('server_mgmt.files_roots_loading')} />
			{:else if rootsError}
				<p class="muted">{t('server_mgmt.files_roots_error')}</p>
			{:else if !roots.length}
				<p class="muted">{t('server_mgmt.files_roots_none')}</p>
			{:else}
				<div class="roots">
					<RowList label={t('server_mgmt.files_roots_title')}>
						{#each roots as r (r)}
							<ListRow title={r} chevron current={cwd === r} onclick={() => void refresh(r)} />
						{/each}
					</RowList>
				</div>
			{/if}
		</section>
	</aside>
</div>

{#if viewing}
	<FileViewer file={viewing.file} requested={viewing.requested} onclose={() => (viewing = null)} />
{/if}

<style>
	.pathbar {
		display: flex;
		flex-direction: column;
		gap: var(--space-1);
	}
	.label {
		font-weight: 600;
	}
	.pathrow {
		display: flex;
		gap: var(--space-2);
		max-width: 720px;
	}
	.pathrow :global(.path-input) {
		flex: 1;
		min-width: 0;
		font-family: var(--mono);
	}
	.toolbar {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-2);
	}
	.count {
		margin: 0;
		min-height: 1.5em;
		color: var(--text-muted);
	}
	.files-table :global(.dir-name) {
		font-weight: 600;
	}
	.files-table :global(.file-name),
	.files-table :global(.dir-name) {
		overflow-wrap: anywhere;
	}
	/* Phones: names stay readable; the table scrolls sideways instead */
	.files-table :global(td.col-name),
	.files-table :global(th.col-name) {
		min-width: 14ch;
	}
	.files-table :global(:is(td, th).col-size),
	.files-table :global(:is(td, th).col-perms) {
		white-space: nowrap;
	}
	.muted {
		margin: 0;
		color: var(--text-muted);
	}
	/* Paths are read exactly: monospace; long ones wrap */
	.roots :global(.row-title) {
		font-family: var(--mono);
		overflow-wrap: anywhere;
	}
	@media (pointer: coarse) {
		.files-table :global(.row-open) {
			min-height: 44px;
		}
	}
</style>
