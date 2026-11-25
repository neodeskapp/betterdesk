<!--
  Your account › Signed-in devices (#sessions): the browsers this account
  is signed in on right now (A239), the same list as Signed-in devices in
  the account menu (lib/people/sessions-list.svelte.ts, SessionsRows):
  "This browser" marked, Sign out on every other one, and Sign out
  everywhere else with its result in the line beside it. Audit log › Your
  sessions led here before (/security-audit?tab=sessions); the audit log
  keeps the history of ended sessions.
-->
<script lang="ts">
	import { untrack } from 'svelte';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import SessionsRows from '#lib/people/SessionsRows.svelte';
	import { SessionsList } from '#lib/people/sessions-list.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import FormStatus from '#lib/ui/FormStatus.svelte';

	const model = new SessionsList(null);

	$effect(() => {
		untrack(() => void model.load());
	});
</script>

<section class="section" id="sessions" aria-labelledby="sessions-title">
	<h2 id="sessions-title">{t('account.sessions')}</h2>
	<p class="section-note">{t('sessions.subtitle_own')}</p>
	<div class="sessions-list">
		<SessionsRows {model} />
	</div>
	<div class="sessions-actions">
		<FormStatus message={model.status} tone={model.statusTone} />
		{#if model.list && model.others.length}
			<Button variant="danger-quiet" disabled={model.busy} onclick={() => model.signOutAll()}
				>{model.allLabel}</Button
			>
		{/if}
	</div>
</section>

<style>
	.sessions-list {
		max-width: var(--measure);
	}
	.sessions-actions {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-2) var(--space-4);
		max-width: var(--measure);
		margin-top: var(--space-3);
	}
	.sessions-actions :global(.form-status) {
		flex: 1 1 16rem;
		margin: 0;
	}
</style>
