<!--
  Settings › MeshCentral (server.config): whether MeshAgent devices can
  connect, the mesh groups with their .msh files, the name in the .msh
  file with the install steps, and the recorded sessions. Nothing here
  goes into a save bar: adding, renaming and removing a group are actions
  that save at once (PATTERN.md rule 1), and the name in the .msh file
  only goes into the downloaded file.

  Element ids are the old page's, for links and the settings search.
-->
<script lang="ts">
	import {
		getMeshGroups,
		getMeshStatus,
		getRecordings,
		meshDownloadHref,
		recordingHref,
		saveMeshGroups,
		type MeshGroup,
		type MeshStatus,
		type Recording
	} from '#lib/api/settings.ts';
	import { t, tf } from '#lib/i18n/i18n.svelte.ts';
	import { formatBytes, formatNumber } from '#lib/i18n/format.ts';
	import { can } from '#lib/state/session.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import Dialog from '#lib/ui/Dialog.svelte';
	import Empty from '#lib/ui/Empty.svelte';
	import Facts from '#lib/ui/Facts.svelte';
	import Field from '#lib/ui/Field.svelte';
	import FormStatus from '#lib/ui/FormStatus.svelte';
	import Input from '#lib/ui/Input.svelte';
	import MenuButton from '#lib/ui/MenuButton.svelte';
	import MenuDivider from '#lib/ui/MenuDivider.svelte';
	import MenuItem from '#lib/ui/MenuItem.svelte';
	import Notice from '#lib/ui/Notice.svelte';
	import States from '#lib/ui/States.svelte';
	import Status from '#lib/ui/Status.svelte';
	import Steps from '#lib/ui/Steps.svelte';
	import Table from '#lib/ui/Table.svelte';
	import Time from '#lib/ui/Time.svelte';
	import ValueCopy from '#lib/ui/ValueCopy.svelte';
	import ValueField from '#lib/ui/ValueField.svelte';
	import { confirm, prompt } from '#lib/ui/dialogs.svelte.ts';
	import type { Column } from '#lib/ui/table.ts';
	import { toast } from '#lib/ui/toasts.svelte.ts';
	import SettingRow from '#lib/ui/settings/SettingRow.svelte';
	import SettingsBody from '#lib/ui/settings/SettingsBody.svelte';
	import SettingsGroup from '#lib/ui/settings/SettingsGroup.svelte';
	import { meshGroupIdValid } from '../general.ts';

	const canConfig = can('server.config');

	type Line = { message: string; tone: 'neutral' | 'ok' | 'error' };

	let mesh = $state.raw<MeshStatus | null>(null);
	let groups = $state.raw<MeshGroup[]>([]);
	let groupsError = $state('');
	let groupsStatus = $state<Line>({ message: '', tone: 'neutral' });
	let recordings = $state.raw<Recording[] | null>(null);
	let recordingsError = $state('');
	let mshName = $state('NeoDesk Mesh');

	async function loadMesh() {
		mesh = await getMeshStatus();
		if (!mesh.enabled) return;
		void loadGroups();
		void loadRecordings();
	}

	async function loadGroups() {
		try {
			groups = await getMeshGroups();
			groupsError = '';
		} catch {
			groupsError = t('mesh.groups_load_error');
		}
	}

	async function loadRecordings() {
		try {
			recordings = await getRecordings();
			recordingsError = '';
		} catch {
			recordingsError = t('mesh.recordings_load_error');
		}
	}

	async function storeGroups(next: MeshGroup[], okMessage: string) {
		groupsStatus = { message: '', tone: 'neutral' };
		await saveMeshGroups(next);
		await loadGroups();
		groupsStatus = { message: okMessage, tone: 'ok' };
		toast.success(okMessage);
	}

	const saveFailed = (err: unknown) =>
		err instanceof Error && err.message ? err.message : t('mesh.groups_save_error');

	// The install disclosure downloads the default group's file (or the first one's)
	const installGroup = $derived(groups.find((g) => g.id === 'default') ?? groups[0]);

	// Add a group: a dialog with name and ID; saved right away
	let adding = $state(false);
	let addName = $state('');
	let addId = $state('');
	let addStatus = $state<Line>({ message: '', tone: 'neutral' });
	let addBusy = $state(false);
	let addNameEl = $state<HTMLInputElement | null>(null);
	let addIdEl = $state<HTMLInputElement | null>(null);

	function openAdd() {
		addName = '';
		addId = '';
		addStatus = { message: '', tone: 'neutral' };
		adding = true;
	}

	async function submitAdd(e?: SubmitEvent) {
		e?.preventDefault();
		if (addBusy) return;
		const name = addName.trim();
		const id = addId.trim();
		if (!id || !name) {
			addStatus = { message: t('settings.general.mesh_group_needs_both'), tone: 'error' };
			(name ? addIdEl : addNameEl)?.focus();
			return;
		}
		if (!meshGroupIdValid(id)) {
			addStatus = { message: t('settings.general.mesh_group_id_invalid'), tone: 'error' };
			addIdEl?.focus();
			return;
		}
		if (groups.some((g) => g.id === id)) {
			addStatus = { message: t('settings.general.mesh_group_exists'), tone: 'error' };
			addIdEl?.focus();
			return;
		}
		addBusy = true;
		addStatus = { message: t('branding.status_saving'), tone: 'neutral' };
		try {
			await storeGroups([...groups, { id, name, mesh_id: '' }], t('settings.general.mesh_group_added'));
			adding = false;
		} catch (err) {
			addStatus = { message: saveFailed(err), tone: 'error' };
		} finally {
			addBusy = false;
		}
	}

	async function renameGroup(g: MeshGroup) {
		const name = await prompt({
			title: t('settings.general.mesh_group_rename_title'),
			label: t('mesh.group_name_placeholder'),
			value: g.name,
			hint: t('settings.general.mesh_group_name_hint'),
			confirmLabel: t('settings.general.mesh_group_rename')
		});
		const next = name?.trim();
		if (!next || next === g.name) return;
		try {
			await storeGroups(
				groups.map((x) => (x.id === g.id ? { ...x, name: next } : x)),
				t('settings.general.mesh_group_renamed')
			);
		} catch (err) {
			groupsStatus = { message: saveFailed(err), tone: 'error' };
		}
	}

	async function removeGroup(g: MeshGroup) {
		const ok = await confirm({
			title: t('settings.general.mesh_group_remove_title'),
			message: t('settings.general.mesh_group_remove_msg', { name: g.name || g.id }),
			confirmLabel: t('settings.general.mesh_group_remove'),
			danger: true
		});
		if (!ok) return;
		try {
			await storeGroups(
				groups.filter((x) => x.id !== g.id),
				t('settings.general.mesh_group_removed')
			);
		} catch (err) {
			groupsStatus = { message: saveFailed(err), tone: 'error' };
		}
	}

	const groupColumns: Column<MeshGroup>[] = $derived([
		{ id: 'name', label: t('settings.general.mesh_col_name'), cell: groupName },
		{ id: 'id', label: t('settings.general.mesh_col_id'), cell: groupId },
		{ id: 'meshid', label: t('settings.general.mesh_col_meshid'), cell: groupMeshId, class: 'col-meshid' },
		{ id: 'actions', label: t('common.actions'), labelHidden: true, align: 'actions', cell: groupActions }
	]);

	// Downloading a recording needs device.connect (server.config alone gets 403)
	const canDownloadRecordings = can('device.connect');
	const recordingColumns: Column<Recording>[] = $derived([
		{ id: 'device', label: t('settings.general.rec_device'), cell: recDevice },
		{ id: 'type', label: t('settings.general.rec_type'), cell: recType },
		{ id: 'when', label: t('settings.general.rec_when'), cell: recWhen },
		{ id: 'size', label: t('settings.general.rec_size'), align: 'num', cell: recSize },
		{ id: 'actions', label: t('common.actions'), labelHidden: true, align: 'actions', cell: recActions }
	]);

	let meshError = $state('');

	$effect(() => {
		if (!canConfig) return;
		loadMesh().catch((err: unknown) => {
			meshError = err instanceof Error && err.message ? err.message : t('errors.server_error');
		});
	});
</script>

{#snippet groupName(g: MeshGroup)}{g.name}{/snippet}
{#snippet groupId(g: MeshGroup)}<ValueCopy value={g.id} copy={false} />{/snippet}
{#snippet groupMeshId(g: MeshGroup)}
	{#if g.mesh_id}
		<ValueField value={g.mesh_id} label={`${t('settings.general.mesh_col_meshid')}: ${g.name || g.id}`} />
	{:else}<Empty />{/if}
{/snippet}
{#snippet groupActions(g: MeshGroup)}
	<MenuButton kebab label={t('settings.general.mesh_group_actions', { name: g.name || g.id })}>
		<MenuItem icon="download" href={meshDownloadHref(g.name, g.mesh_id)} download
			>{t('settings.general.mesh_group_download')}</MenuItem
		>
		<MenuItem icon="rename" onselect={() => void renameGroup(g)}
			>{t('settings.general.mesh_group_rename')}</MenuItem
		>
		<!-- The default group stays: .msh files handed out name it (BUGS L384) -->
		{#if g.id !== 'default'}
			<MenuDivider />
			<MenuItem icon="delete" danger onselect={() => void removeGroup(g)}
				>{t('settings.general.mesh_group_remove')}</MenuItem
			>
		{/if}
	</MenuButton>
{/snippet}

{#snippet recDevice(r: Recording)}
	<span class="cell-stack">
		<ValueCopy value={r.peer_id} />
		{#if r.transport && r.transport !== 'mesh'}<span class="cell-sub">{r.transport}</span>{/if}
	</span>
{/snippet}
{#snippet recType(r: Recording)}{#if r.session_type}{r.session_type}{:else}<Empty />{/if}{/snippet}
{#snippet recWhen(r: Recording)}<Time value={r.modified} />{/snippet}
{#snippet recSize(r: Recording)}{formatBytes(Number(r.size) || 0)}{/snippet}
{#snippet recActions(r: Recording)}
	{#if canDownloadRecordings && r.transport === 'mesh' && r.id}
		<Button size="sm" icon="download" href={recordingHref(r.id)} download
			>{t('mesh.recording_download')}</Button
		>
	{/if}
{/snippet}

{#snippet meshStatusValue()}<Status tone="good" label={t('mesh.enabled_label')} />{/snippet}
{#snippet meshServerId()}
	{#if mesh?.server_id}<ValueField
			value={mesh.server_id}
			label={t('settings.general.mesh_server_id')}
		/>{:else}<Empty />{/if}
{/snippet}
{#snippet meshCert()}
	{#if mesh?.cert_present}
		{#if mesh.cert_modified}
			{@const parts = t('settings.general.mesh_cert_since').split('{date}')}
			{parts[0]}<Time
				value={mesh.cert_modified}
				relative={false}
				format={{ year: 'numeric', month: 'short', day: 'numeric', hour: undefined, minute: undefined }}
			/>{parts[1] ?? ''}
		{:else}{t('settings.general.mesh_cert_present')}{/if}
	{:else}
		<Status tone="warning" label={t('settings.general.mesh_cert_none')} />
	{/if}
{/snippet}

<SettingsBody saveModel="none">
	<SettingsGroup
		id="mesh-settings-panel"
		title={t('mesh.settings_title')}
		description={t('settings.general.mesh_note2')}
	>
		{#if meshError}
			<div class="group-body"><States kind="error" message={meshError} /></div>
		{:else if !mesh}
			<div class="group-body"><States kind="loading" /></div>
		{:else if !mesh.enabled}
			<p class="state" id="mesh-state">{t('settings.general.mesh_off')}</p>
		{:else}
			<div class="group-body" id="mesh-body">
				<Facts
					items={[
						{ label: t('settings.general.mesh_status'), value: meshStatusValue },
						{
							label: t('settings.general.mesh_agents'),
							value: formatNumber(Number(mesh.agents_online ?? 0))
						},
						{ label: t('settings.general.mesh_server_id'), value: meshServerId },
						{ label: t('settings.general.mesh_cert'), value: meshCert }
					]}
				/>
				{#if !mesh.cert_present}<Notice
						tone="warning"
						id="mesh-cert-banner"
						message={t('settings.general.mesh_cert_missing')}
					/>{/if}
			</div>
		{/if}
	</SettingsGroup>

	{#if mesh?.enabled}
		<SettingsGroup
			id="mesh-groups-form"
			title={t('mesh.groups_title')}
			description={t('settings.general.mesh_groups_note')}
			consequence={tf(
				'settings.mesh.groups_effect',
				'Adding, renaming and removing a group saves at once. Devices already installed stay in their group.'
			)}
		>
			{#snippet actions()}
				<Button size="sm" icon="add" id="mesh-group-add" onclick={openAdd}>{t('mesh.group_add')}</Button>
			{/snippet}
			<div class="group-body">
				{#if groupsError}
					<States kind="error" message={groupsError} onretry={() => void loadGroups()} />
				{:else if !groups.length}
					<p class="state">{t('settings.general.mesh_groups_empty')}</p>
				{:else}
					<div class="mesh-groups-table">
						<Table rows={groups} columns={groupColumns} key={(g) => g.id} label={t('mesh.groups_title')} />
					</div>
				{/if}
				{#if groupsStatus.message}<FormStatus message={groupsStatus.message} tone={groupsStatus.tone} />{/if}
			</div>
		</SettingsGroup>

		<SettingsGroup
			id="mesh-install"
			title={t('settings.general.mesh_install_title')}
			description={tf(
				'settings.mesh.install_note',
				'Download the .msh file of a group and install MeshAgent with it on the device.'
			)}
		>
			<div class="group-body">
				<Steps
					items={[
						t('settings.general.mesh_install_step1'),
						t('settings.general.mesh_install_step2'),
						t('settings.general.mesh_install_step3')
					]}
				/>
			</div>
			<SettingRow
				id="mesh-msh-name"
				label={t('settings.general.mesh_msh_name')}
				help={tf(
					'settings.mesh.msh_name_help',
					'MeshAgent shows this name for the server. It only goes into the downloaded file and is not saved. The file is for the group with the ID default; for another group, use the menu in its row.'
				)}
			>
				{#snippet control(f)}
					<div class="inline-controls">
						<Input
							id={f.id}
							describedby={f.describedby}
							bind:value={mshName}
							maxlength={80}
							onkeydown={(e) => {
								if (e.key === 'Enter') e.preventDefault();
							}}
						/>
						<Button icon="download" href={meshDownloadHref(mshName, installGroup?.mesh_id)} download
							>{t('mesh.download_msh')}</Button
						>
					</div>
				{/snippet}
			</SettingRow>
		</SettingsGroup>

		<SettingsGroup
			id="mesh-recordings"
			title={t('settings.general.rec_title')}
			description={recordings?.length ? t('settings.general.rec_note') : undefined}
		>
			<div class="group-body">
				{#if recordingsError}
					<States kind="error" message={recordingsError} onretry={() => void loadRecordings()} />
				{:else if !recordings}
					<States kind="loading" />
				{:else if !recordings.length}
					<p class="state">{t('settings.general.rec_empty')}</p>
				{:else}
					<Table
						rows={recordings}
						columns={recordingColumns}
						key={(r) => `${r.id}|${r.peer_id}|${r.modified}`}
						label={t('settings.general.rec_title')}
					/>
				{/if}
			</div>
		</SettingsGroup>
	{/if}
</SettingsBody>

{#if adding}
	<Dialog title={t('settings.general.mesh_group_add_title')} onclose={() => (adding = false)}>
		<form id="mesh-group-add-form" class="dialog-form" onsubmit={submitAdd} novalidate>
			<Field
				id="mesh-group-name"
				label={t('mesh.group_name_placeholder')}
				hint={t('settings.general.mesh_group_name_hint')}
			>
				{#snippet children(f)}
					<Input {...f} bind:value={addName} bind:element={addNameEl} maxlength={80} autocomplete="off" />
				{/snippet}
			</Field>
			<Field
				id="mesh-group-id"
				label={t('mesh.group_id_placeholder')}
				hint={t('settings.general.mesh_group_id_hint')}
				width="medium"
			>
				{#snippet children(f)}
					<Input
						{...f}
						class="mono"
						bind:value={addId}
						bind:element={addIdEl}
						maxlength={64}
						autocomplete="off"
						spellcheck={false}
					/>
				{/snippet}
			</Field>
			<FormStatus message={addStatus.message} tone={addStatus.tone} />
		</form>
		{#snippet footer()}
			<Button onclick={() => (adding = false)}>{t('actions.cancel')}</Button>
			<Button type="submit" form="mesh-group-add-form" variant="primary" icon="add" busy={addBusy}
				>{t('mesh.group_add')}</Button
			>
		{/snippet}
	</Dialog>
{/if}

<style>
	.group-body {
		display: flex;
		flex-direction: column;
		gap: var(--space-3);
		padding: var(--space-3) var(--row-pad-x);
		min-width: 0;
	}
	.state {
		margin: 0;
		padding: var(--space-3) var(--row-pad-x);
		color: var(--text-muted);
	}
	.group-body .state {
		padding: 0;
	}
	.inline-controls {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
		align-items: center;
	}
	.inline-controls :global(input) {
		flex: 1 1 220px;
		min-width: 0;
	}
	.cell-stack {
		display: flex;
		flex-direction: column;
		min-width: 0;
	}
	.cell-sub {
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	/* MeshIDs are long: the column takes what is left and cuts the value */
	.mesh-groups-table :global(.col-meshid) {
		width: 50%;
		max-width: 0;
	}
	.dialog-form {
		display: flex;
		flex-direction: column;
	}
</style>
