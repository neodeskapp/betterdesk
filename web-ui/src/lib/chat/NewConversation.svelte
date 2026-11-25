<!--
  Start a conversation (A242). Support: find a computer by name, ID, host
  name, owner or folder and open its conversation. Team: pick a colleague
  for a direct conversation, or several and a name for a group. Editing a
  group uses the same dialog with its name and members filled in.
-->
<script lang="ts">
	import { untrack } from 'svelte';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import Checkbox from '#lib/ui/Checkbox.svelte';
	import Dialog from '#lib/ui/Dialog.svelte';
	import Icon from '#lib/ui/Icon.svelte';
	import Input from '#lib/ui/Input.svelte';
	import SearchField from '#lib/ui/SearchField.svelte';
	import { toast } from '#lib/ui/toasts.svelte.ts';
	import * as api from './api.ts';
	import { chat } from './client.svelte.ts';
	import type { Device, Person, Section } from './types.ts';

	interface Props {
		section: Section;
		/** A group to change: its id, name and members */
		group?: { id: string; title: string; members: number[] } | null;
		onclose: () => void;
		onopen: (conv: string) => void;
	}

	let { section, group = null, onclose, onopen }: Props = $props();

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
			<p class="nc-state">{t('common.loading')}</p>
		{:else if !devices.length}
			<p class="nc-state">{t('chat.no_match')}</p>
		{:else}
			<ul class="nc-list">
				{#each devices as d (d.id)}
					<li>
						<button
							type="button"
							class="nc-row"
							onclick={() => {
								onopen(d.id);
								onclose();
							}}
						>
							<span class="nc-icon" aria-hidden="true"><Icon name="computer" /></span>
							<span class="nc-main">
								<span class="nc-name">{d.name}</span>
								<span class="nc-meta">
									<span>{d.online ? t('chat.online') : t('chat.offline')}</span>
									{#if d.owner}<span>{d.owner}</span>{/if}
									{#if d.folder}<span>{d.folder}</span>{/if}
									<span>{d.id}</span>
								</span>
							</span>
						</button>
					</li>
				{/each}
			</ul>
		{/if}
	{:else}
		{#if people === null}
			<p class="nc-state">{t('common.loading')}</p>
		{:else if !shownPeople.length}
			<p class="nc-state">{people.length ? t('chat.no_match') : t('chat.empty_team')}</p>
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
			<label class="nc-label" for="chat-group-title">{t('chat.group_name')}</label>
			<Input id="chat-group-title" maxlength={120} bind:value={title} />
		{/if}
	{/if}
	{#snippet footer()}
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
	.nc-state {
		color: var(--text-muted);
	}
	.nc-list {
		max-height: min(50vh, 420px);
		margin: var(--space-3) 0 0;
		padding: 0;
		overflow-y: auto;
		list-style: none;
		border-top: 1px solid var(--rule);
	}
	.nc-row {
		display: flex;
		gap: var(--space-3);
		width: 100%;
		padding: var(--space-2);
		border: 0;
		border-bottom: 1px solid var(--rule);
		background: none;
		color: var(--text);
		font: inherit;
		text-align: start;
		cursor: pointer;
	}
	.nc-row:hover {
		background: var(--hover);
	}
	.nc-icon {
		display: inline-flex;
		flex: none;
		align-items: center;
		justify-content: center;
		width: 32px;
		height: 32px;
		border-radius: 50%;
		background: var(--hover);
		color: var(--text-muted);
	}
	.nc-main {
		display: flex;
		flex-direction: column;
		min-width: 0;
	}
	.nc-name {
		font-weight: 600;
	}
	.nc-meta {
		display: flex;
		flex-wrap: wrap;
		gap: 0 var(--space-3);
		color: var(--text-muted);
		font-size: var(--text-sm);
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
	.nc-label {
		display: block;
		margin: var(--space-2) 0 var(--space-1);
		font-weight: 600;
	}
</style>
