<!--
  Your account › Recent sign-ins and security events (#security-events):
  the account's sign-ins, changes to its password and two-factor sign-in,
  and every time an admin viewed NeoDesk as it (Sign in as), with who and
  why. Newest first, the last 50 (GET /api/users/me/security-events).
-->
<script lang="ts">
	import { untrack } from 'svelte';
	import { listOwnSecurityEvents, type SecurityEvent } from '#lib/api/security-events.ts';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import ListRow from '#lib/ui/ListRow.svelte';
	import RowList from '#lib/ui/RowList.svelte';
	import States from '#lib/ui/States.svelte';
	import Time from '#lib/ui/Time.svelte';
	import { eventFacts, eventTitle } from './security-events.ts';

	let events = $state.raw<SecurityEvent[] | null>(null);
	let failed = $state(false);

	async function load() {
		failed = false;
		try {
			events = await listOwnSecurityEvents();
		} catch {
			failed = true;
		}
	}

	$effect(() => {
		untrack(() => void load());
	});
</script>

<section class="section" id="security-events" aria-labelledby="security-events-title">
	<h2 id="security-events-title">{t('account.security_events')}</h2>
	<p class="section-note">{t('account.security_events_note')}</p>
	<div class="events">
		{#if failed}
			<States kind="error" message={t('account.security_events_failed')} onretry={() => void load()} />
		{:else if events === null}
			<States kind="loading" />
		{:else if events.length === 0}
			<p class="note">{t('account.security_events_empty')}</p>
		{:else}
			<RowList label={t('account.security_events')}>
				{#each events as e (e.id)}
					{#snippet sub()}
						<span class="row-facts">
							<Time value={e.at} />
							{#each eventFacts(e, t) as fact (fact)}<span>{fact}</span>{/each}
						</span>
					{/snippet}
					<ListRow
						title={eventTitle(e, t)}
						{sub}
						mark={e.action === 'impersonation_started' ? 'info' : undefined}
					/>
				{/each}
			</RowList>
		{/if}
	</div>
</section>

<style>
	.events {
		max-width: var(--measure);
	}
	.row-facts {
		display: flex;
		flex-wrap: wrap;
		gap: 0 var(--space-3);
	}
	.note {
		margin: 0;
		color: var(--text-muted);
	}
</style>
