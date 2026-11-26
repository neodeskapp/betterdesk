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
			<p class="ts-state">{t('chat.action_failed')}</p>
		{:else if teams === null}
			<p class="ts-state">{t('common.loading')}</p>
		{:else if !teams.length}
			<p class="ts-state">{t('chat.no_teams')}</p>
		{:else}
			<ul class="ts-list">
				{#each teams as team (team.id)}
					{@const names = groupNames(team)}
					<li>
						<button type="button" class="ts-row" onclick={() => (editing = team.id)}>
							<span class="ts-icon" aria-hidden="true"><Icon name="workspaces" /></span>
							<span class="ts-main">
								<span class="ts-name">{team.title}</span>
								<span class="ts-meta">
									<span>{t('chat.team_people_count', { count: team.people.length })}</span>
									{#if names}<span>{names}</span>{/if}
								</span>
							</span>
							<Icon name="edit" />
						</button>
					</li>
				{/each}
			</ul>
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
	.ts-intro,
	.ts-state {
		margin: 0 0 var(--space-3);
		color: var(--text-muted);
	}
	.ts-list {
		margin: 0;
		padding: 0;
		list-style: none;
		border-top: 1px solid var(--rule);
	}
	.ts-row {
		display: flex;
		gap: var(--space-3);
		align-items: center;
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
	.ts-row:hover {
		background: var(--hover);
	}
	.ts-icon {
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
	.ts-main {
		display: flex;
		flex: 1;
		flex-direction: column;
		min-width: 0;
	}
	.ts-name {
		font-weight: 600;
	}
	.ts-meta {
		display: flex;
		flex-wrap: wrap;
		gap: 0 var(--space-3);
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
</style>
