<!--
  Set up a team of Team chat (Manage teams): its name, the people picked
  by hand and the user groups whose members are in it for as long as they
  are in the user group. Without an id it makes a new team; with one it
  changes that team and can delete it with its messages.
-->
<script lang="ts">
	import { untrack } from 'svelte';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import Checkbox from '#lib/ui/Checkbox.svelte';
	import Dialog from '#lib/ui/Dialog.svelte';
	import Input from '#lib/ui/Input.svelte';
	import SearchField from '#lib/ui/SearchField.svelte';
	import { confirm } from '#lib/ui/dialogs.svelte.ts';
	import { toast } from '#lib/ui/toasts.svelte.ts';
	import * as api from './api.ts';
	import { chat } from './client.svelte.ts';
	import type { Person, UserGroupOption } from './types.ts';

	interface Props {
		/** The team to change; null makes a new one */
		id?: string | null;
		onclose: () => void;
		/** The team was made or changed */
		onsaved: (conv: string) => void;
		ondeleted?: (conv: string) => void;
	}

	let { id = null, onclose, onsaved, ondeleted }: Props = $props();

	let loaded = $state(false);
	let people = $state.raw<Person[]>([]);
	let userGroups = $state.raw<UserGroupOption[]>([]);
	let title = $state('');
	let picked = $state<number[]>([]);
	let groups = $state<string[]>([]);
	let q = $state('');
	let busy = $state(false);

	$effect(() => {
		const team = id;
		untrack(() => {
			api.loadTeams().then(
				(r) => {
					people = r.people;
					userGroups = r.userGroups;
					const found = team ? r.teams.find((x) => x.id === team) : undefined;
					if (found) {
						title = found.title;
						picked = found.people;
						groups = found.groups;
					} else if (chat.me && r.people.some((p) => p.id === chat.me?.id)) {
						// A new team starts with the one who makes it
						picked = [chat.me.id];
					}
					loaded = true;
				},
				() => {
					toast.error(t('chat.action_failed'));
					onclose();
				}
			);
		});
	});

	const tq = $derived(q.trim().toLowerCase());
	const shownPeople = $derived(
		people.filter((p) => !tq || p.name.toLowerCase().includes(tq) || p.username.toLowerCase().includes(tq))
	);

	/** Everyone in the team: picked by hand or in one of its user groups, with Team chat */
	const total = $derived.by(() => {
		const may = people.map((p) => p.id);
		const all = [...picked];
		for (const g of userGroups) {
			if (!groups.includes(g.guid)) continue;
			for (const m of g.members) if (may.includes(m) && !all.includes(m)) all.push(m);
		}
		return all.length;
	});

	function toggle<T>(list: T[], value: T, on: boolean): T[] {
		return on ? [...list, value] : list.filter((x) => x !== value);
	}

	async function save() {
		busy = true;
		try {
			const change = { title: title.trim(), people: picked, groups };
			const c = id ? await api.editTeam(id, change) : await api.newTeam(change);
			onsaved(c.id);
			onclose();
		} catch (err) {
			toast.error(err instanceof Error ? err.message : t('chat.action_failed'));
		} finally {
			busy = false;
		}
	}

	async function remove() {
		if (!id) return;
		const sure = await confirm({
			title: t('chat.delete_team_title', { name: title }),
			message: t('chat.delete_team_message'),
			confirmLabel: t('chat.delete_team'),
			danger: true
		});
		if (!sure) return;
		busy = true;
		try {
			await api.deleteTeam(id);
			ondeleted?.(id);
			onclose();
		} catch (err) {
			toast.error(err instanceof Error ? err.message : t('chat.action_failed'));
		} finally {
			busy = false;
		}
	}
</script>

<Dialog open size="large" title={id ? t('chat.edit_team') : t('chat.new_team_title')} {onclose}>
	{#if !loaded}
		<p class="td-state">{t('common.loading')}</p>
	{:else}
		<label class="td-label" for="chat-team-title">{t('chat.team_name')}</label>
		<Input
			id="chat-team-title"
			maxlength={120}
			placeholder={t('chat.team_name_placeholder')}
			bind:value={title}
		/>
		<div class="dialog-cols td-cols">
			<fieldset class="td-block">
				<legend class="td-label">{t('chat.team_people')}</legend>
				<SearchField label={t('chat.search_people')} autocomplete="off" bind:value={q} />
				{#if !shownPeople.length}
					<p class="td-state">{people.length ? t('chat.no_match') : t('chat.empty_team')}</p>
				{:else}
					<div class="td-list">
						{#each shownPeople as p (p.id)}
							<Checkbox
								label={p.name}
								hint={p.name !== p.username ? p.username : undefined}
								checked={picked.includes(p.id)}
								onchange={(e) =>
									(picked = toggle(picked, p.id, (e.currentTarget as HTMLInputElement).checked))}
							/>
						{/each}
					</div>
				{/if}
			</fieldset>
			<fieldset class="td-block">
				<legend class="td-label">{t('chat.team_user_groups')}</legend>
				<p class="td-hint">{t('chat.team_user_groups_hint')}</p>
				{#if !userGroups.length}
					<p class="td-state">{t('chat.team_no_user_groups')}</p>
				{:else}
					<div class="td-list">
						{#each userGroups as g (g.guid)}
							<Checkbox
								label={g.name}
								hint={t('chat.member_count', { count: g.members.length })}
								checked={groups.includes(g.guid)}
								onchange={(e) =>
									(groups = toggle(groups, g.guid, (e.currentTarget as HTMLInputElement).checked))}
							/>
						{/each}
					</div>
				{/if}
			</fieldset>
		</div>
		<p class="td-total">{t('chat.team_total', { count: total })}</p>
	{/if}
	{#snippet footer()}
		{#if id}
			<Button
				class="modal-footer-start"
				variant="danger"
				icon="delete"
				disabled={busy || !loaded}
				onclick={() => void remove()}>{t('chat.delete_team')}</Button
			>
		{/if}
		<Button onclick={onclose}>{t('actions.cancel')}</Button>
		<Button
			variant="primary"
			{busy}
			disabled={!loaded || !title.trim() || (!picked.length && !groups.length)}
			onclick={() => void save()}>{id ? t('actions.save') : t('chat.create_team')}</Button
		>
	{/snippet}
</Dialog>

<style>
	.td-state,
	.td-hint,
	.td-total {
		margin: var(--space-1) 0;
		color: var(--text-muted);
	}
	.td-hint {
		font-size: var(--text-sm);
	}
	.td-label {
		display: block;
		margin: 0 0 var(--space-1);
		padding: 0;
		font-weight: 600;
	}
	.td-cols {
		margin-top: var(--space-4);
	}
	.td-block {
		min-width: 0;
		margin: 0;
		padding: 0;
		border: 0;
	}
	.td-list {
		display: flex;
		flex-direction: column;
		gap: var(--space-1);
		max-height: min(40vh, 320px);
		margin-top: var(--space-2);
		overflow-y: auto;
	}
</style>
