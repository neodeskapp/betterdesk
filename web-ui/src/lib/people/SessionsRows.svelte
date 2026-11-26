<!--
  The rows of a signed-in devices list (sessions-list.svelte.ts): one row
  per browser with its address, when it signed in and when it was last
  used; "This browser" is marked and has no Sign out, every other row has
  Sign out. Then the notes: nothing else signed in, and the limit.
-->
<script lang="ts">
	import { formatDate, formatRelative } from '#lib/i18n/format.ts';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import ListRow from '#lib/ui/ListRow.svelte';
	import RowList from '#lib/ui/RowList.svelte';
	import States from '#lib/ui/States.svelte';
	import Tag from '#lib/ui/Tag.svelte';
	import type { SessionsList } from './sessions-list.svelte.ts';
	import { deviceLabel, limitText } from './sessions.ts';

	let { model }: { model: SessionsList } = $props();

	const other = $derived(model.user);
	const listLabel = $derived(
		other ? t('sessions.user_title', { username: other.username }) : t('sessions.title')
	);
</script>

{#if model.list === null && !model.loadError}
	<States kind="loading" />
{:else if model.loadError}
	<States kind="error" message={model.loadError} onretry={() => void model.load()} />
{:else if model.list}
	{#if model.list.sessions.length}
		<RowList label={listLabel}>
			{#each model.list.sessions as d (d.id)}
				{#snippet sub()}
					<span class="session-facts">
						<span>{t('sessions.address')}: {d.ipAddress || '-'}</span>
						<span title={formatDate(d.createdAt)}
							>{t('sessions.signed_in')}: {formatRelative(d.createdAt)}</span
						>
						<span title={formatDate(d.lastActivityAt)}
							>{t('sessions.last_used')}: {formatRelative(d.lastActivityAt)}</span
						>
					</span>
				{/snippet}
				{#snippet here()}<Tag label={t('sessions.this_browser')} icon="check_circle" />{/snippet}
				{#snippet signOutButton()}<Button
						size="sm"
						variant="danger-quiet"
						disabled={model.busy}
						onclick={() => model.signOut(d)}
						>{t('sessions.sign_out')}<span class="sr-only"> {deviceLabel(d, t)}</span></Button
					>{/snippet}
				<ListRow
					title={deviceLabel(d, t)}
					{sub}
					end={d.current ? here : undefined}
					actions={d.current ? undefined : signOutButton}
				/>
			{/each}
		</RowList>
	{:else}
		<p class="note">{other ? t('sessions.none_user', { username: other.username }) : t('sessions.none')}</p>
	{/if}
	{#if !other && model.list.sessions.length && !model.others.length}
		<p class="note">{t('sessions.none_other')}</p>
	{/if}
	<p class="note">{limitText(model.list.limit, t)}</p>
{/if}

<style>
	.session-facts {
		display: flex;
		flex-wrap: wrap;
		gap: 0 var(--space-3);
	}
	.note {
		margin: var(--space-3) 0 0;
		color: var(--text-muted);
	}
</style>
