<!--
  Every team of Team chat, for people with Manage teams: the ones they are
  no member of too, so a team made for others stays in reach. Each opens
  in TeamDialog; New team makes one.
-->
<script lang="ts">
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import Dialog from '#lib/ui/Dialog.svelte';
	import Icon from '#lib/ui/Icon.svelte';
	import ListRow from '#lib/ui/ListRow.svelte';
	import RowList from '#lib/ui/RowList.svelte';
	import States from '#lib/ui/States.svelte';
	import * as api from './api.ts';
	import { chat } from './client.svelte.ts';
	import TeamDialog from './TeamDialog.svelte';
	import type { TeamSetup, UserGroupOption } from './types.ts';

	let { onclose }: { onclose: () => void } = $props();

	let teams = $state.raw<TeamSetup[] | null>(null);
	let userGroups = $state.raw<UserGroupOption[]>([]);
	let failed = $state(false);
	/** The team open in TeamDialog: its id, '' for a new one */
	let editing = $state<string | null>(null);

	async function load() {
		failed = false;
		try {
			const r = await api.loadTeams();
			teams = r.teams;
			userGroups = r.userGroups;
		} catch {
			failed = true;
		}
	}

	$effect(() => {
		void load();
	});

	function groupNames(team: TeamSetup): string {
		return userGroups
			.filter((g) => team.groups.includes(g.guid))
			.map((g) => g.name)
			.join(', ');
	}

	function done() {
		chat.listVersion++;
		void load();
	}
</script>

{#if editing === null}
	<Dialog open title={t('chat.manage_teams')} {onclose}>
		<p class="ts-intro">{t('chat.manage_teams_hint')}</p>
		{#if failed}
			<States kind="error" message={t('chat.action_failed')} onretry={() => void load()} />
		{:else if teams === null}
			<States kind="loading" />
		{:else if !teams.length}
			<States message={t('chat.no_teams')} />
		{:else}
			<RowList>
				{#each teams as team (team.id)}
					{@const names = groupNames(team)}
					<ListRow title={team.title} onclick={() => (editing = team.id)}>
						{#snippet start()}
							<span class="ts-icon" aria-hidden="true"><Icon name="workspaces" /></span>
						{/snippet}
						{#snippet sub()}
							<span class="ts-meta">
								<span>{t('chat.team_people_count', { count: team.people.length })}</span>
								{#if names}<span>{names}</span>{/if}
							</span>
						{/snippet}
						{#snippet end()}<Icon name="edit" />{/snippet}
					</ListRow>
				{/each}
			</RowList>
		{/if}
		{#snippet footer()}
			<Button onclick={onclose}>{t('actions.close')}</Button>
			<Button variant="primary" icon="add" onclick={() => (editing = '')}>{t('chat.new_team_title')}</Button>
		{/snippet}
	</Dialog>
{:else}
	<TeamDialog id={editing || null} onclose={() => (editing = null)} onsaved={done} ondeleted={done} />
{/if}

<style>
	.ts-intro {
		margin: 0 0 var(--space-3);
		color: var(--text-muted);
	}
	.ts-icon {
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
	.ts-icon > :global(.material-icons) {
		font-size: 20px;
	}
	.ts-meta {
		display: flex;
		flex-wrap: wrap;
		gap: 0 var(--space-3);
	}
</style>
