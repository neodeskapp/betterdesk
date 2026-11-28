<!--
  Start a conversation (A242). Support: find a computer by name, ID, host
  name, owner or folder and open its conversation. Team: pick a colleague
  for a direct conversation, or several and a name for a group; people
  with Manage teams can set up a team from here (TeamDialog). Editing a
  group uses the same dialog with its name and members filled in.
-->
<script lang="ts">
	import { untrack } from 'svelte';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import Checkbox from '#lib/ui/Checkbox.svelte';
	import Dialog from '#lib/ui/Dialog.svelte';
	import Field from '#lib/ui/Field.svelte';
	import Icon from '#lib/ui/Icon.svelte';
	import Input from '#lib/ui/Input.svelte';
	import ListRow from '#lib/ui/ListRow.svelte';
	import OsIcon from '#lib/ui/OsIcon.svelte';
	import RowList from '#lib/ui/RowList.svelte';
	import SearchField from '#lib/ui/SearchField.svelte';
	import States from '#lib/ui/States.svelte';
	import Status from '#lib/ui/Status.svelte';
	import ValueCopy from '#lib/ui/ValueCopy.svelte';
	import { toast } from '#lib/ui/toasts.svelte.ts';
	import { platformId } from '#lib/util/display.ts';
	import * as api from './api.ts';
	import { chat } from './client.svelte.ts';
	import type { Device, Person, Section } from './types.ts';

	interface Props {
		section: Section;
		/** A group to change: its id, name and members */
		group?: { id: string; title: string; members: number[] } | null;
		onclose: () => void;
		onopen: (conv: string) => void;
		/** Set up a team instead (Manage teams) */
		onteam?: () => void;
	}

	let { section, group = null, onclose, onopen, onteam }: Props = $props();

	let q = $state('');
	let devices = $state.raw<Device[] | null>(null);
	let people = $state.raw<Person[] | null>(null);
	let picked = $state<number[]>(untrack(() => group?.members.filter((m) => m !== chat.me?.id) ?? []));
	let title = $state(untrack(() => group?.title ?? ''));
	let busy = $state(false);

	$effect(() => {
		const text = q.trim();
		if (section !== 'support') return;
		const timer = setTimeout(() => {
			api.findDevices(text).then(
				(d) => (devices = d),
				() => (devices = [])
			);
		}, 200);
		return () => clearTimeout(timer);
	});

	$effect(() => {
		if (section !== 'team') return;
		api.loadTeam().then(
			(r) => (people = r.people),
			() => (people = [])
		);
	});

	const shownPeople = $derived(
		(people ?? []).filter(
			(p) =>
				!q.trim() ||
				p.name.toLowerCase().includes(q.trim().toLowerCase()) ||
				p.username.toLowerCase().includes(q.trim().toLowerCase())
		)
	);

	function toggle(id: number, on: boolean) {
		picked = on ? [...picked, id] : picked.filter((x) => x !== id);
	}

	async function start() {
		busy = true;
		try {
			if (group) {
				const c = await api.editGroup(group.id, title.trim(), picked);
				onopen(c.id);
			} else if (picked.length === 1 && !title.trim()) {
				const c = await api.openDirect(picked[0] ?? 0);
				onopen(c.id);
			} else {
				const c = await api.newGroup(title.trim(), picked);
				onopen(c.id);
			}
			onclose();
		} catch (err) {
			toast.error(err instanceof Error ? err.message : t('chat.action_failed'));
		} finally {
			busy = false;
		}
	}

	const needsTitle = $derived(!group && picked.length > 1 && !title.trim());
</script>

<Dialog
	open
	title={group ? t('chat.edit_group') : section === 'support' ? t('chat.new_support') : t('chat.new_team')}
	{onclose}
>
	<SearchField
		label={section === 'support' ? t('chat.search_support') : t('chat.search_people')}
		autocomplete="off"
		bind:value={q}
	/>
	{#if section === 'support'}
		{#if devices === null}
			<States kind="loading" />
		{:else if !devices.length}
			<States message={t('chat.no_match')} />
		{:else}
			<div class="nc-scroll">
				<RowList>
					{#each devices as d (d.id)}
						<ListRow
							title={d.name}
							onclick={() => {
								onopen(d.id);
								onclose();
							}}
						>
							{#snippet start()}
								<span class="nc-icon" aria-hidden="true"
									>{#if platformId(d.platform)}<OsIcon platform={d.platform} />{:else}<Icon
											name="computer"
										/>{/if}</span
								>
							{/snippet}
							{#snippet sub()}
								<span class="nc-meta">
									<Status
										tone={d.online ? 'good' : 'neutral'}
										label={d.online ? t('chat.online') : t('chat.offline')}
									/>
									{#if d.owner}<span>{d.owner}</span>{/if}
									{#if d.folder}<span>{d.folder}</span>{/if}
									<ValueCopy value={d.id} copy={false} />
								</span>
							{/snippet}
						</ListRow>
					{/each}
				</RowList>
			</div>
		{/if}
	{:else}
		{#if people === null}
			<States kind="loading" />
		{:else if !shownPeople.length}
			<States message={people.length ? t('chat.no_match') : t('chat.empty_team')} />
		{:else}
			<fieldset class="nc-people">
				<legend class="sr-only">{t('chat.colleagues')}</legend>
				{#each shownPeople as p (p.id)}
					<div class="nc-person">
						<Checkbox
							label={p.name}
							hint={t(`chat.presence_${p.status}`)}
							checked={picked.includes(p.id)}
							onchange={(e) => toggle(p.id, (e.currentTarget as HTMLInputElement).checked)}
						/>
					</div>
				{/each}
			</fieldset>
		{/if}
		{#if group || picked.length > 1}
			<Field id="chat-group-title" label={t('chat.group_name')}>
				{#snippet children(f)}<Input {...f} maxlength={120} bind:value={title} />{/snippet}
			</Field>
		{/if}
	{/if}
	{#snippet footer()}
		{#if section === 'team' && !group && onteam}
			<Button
				class="modal-footer-start"
				icon="workspaces"
				onclick={() => {
					onclose();
					onteam();
				}}>{t('chat.new_team_title')}</Button
			>
		{/if}
		<Button onclick={onclose}>{t('actions.cancel')}</Button>
		{#if section === 'team'}
			<Button variant="primary" {busy} disabled={!picked.length || needsTitle} onclick={() => void start()}
				>{group
					? t('actions.save')
					: picked.length > 1
						? t('chat.start_group')
						: t('chat.start_direct')}</Button
			>
		{/if}
	{/snippet}
</Dialog>

<style>
	/* The rows scroll; the padding gives back the room the row list hangs outside its column */
	.nc-scroll {
		max-height: min(50vh, 420px);
		margin: var(--space-3) calc(-1 * var(--row-pad-x)) 0;
		padding-inline: var(--row-pad-x);
		overflow-y: auto;
	}
	.nc-icon {
		display: inline-flex;
		flex: none;
		align-items: center;
		justify-content: center;
		width: 32px;
		height: 32px;
		border-radius: var(--radius);
		background: var(--tag-bg);
		color: var(--text-muted);
	}
	.nc-icon > :global(.material-icons) {
		font-size: 20px;
	}
	.nc-meta {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 0 var(--space-3);
	}
	.nc-people {
		max-height: min(45vh, 380px);
		margin: var(--space-3) 0;
		padding: 0;
		overflow-y: auto;
		border: 0;
	}
	.nc-person {
		padding: var(--space-1) 0;
	}
</style>
