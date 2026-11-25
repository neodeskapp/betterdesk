<!--
  Signed-in devices (A239) in a sheet. Without `user`: the signed-in
  account's own sessions, from the account menu. "This browser" is marked
  and signs out with Sign out; every other entry has Sign out, and "Sign
  out everywhere else" ends them all. With `user`: that account's sessions
  on the user's page (user.edit), every entry with Sign out, and "Sign out
  everywhere". A browser signed out here goes to the sign-in page at its
  next step and is told why. The list and its actions are
  sessions-list.svelte.ts and SessionsRows.svelte, which Your account
  shows too.
-->
<script lang="ts">
	import { tick, untrack } from 'svelte';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import Sheet from '#lib/ui/Sheet.svelte';
	import SessionsRows from './SessionsRows.svelte';
	import { SessionsList, type SessionsOwner } from './sessions-list.svelte.ts';

	interface Props {
		/** Another account (the user's page); none: your own */
		user?: SessionsOwner | null;
		onclose: () => void;
	}

	let { user = null, onclose }: Props = $props();

	let sheet = $state<HTMLElement | null>(null);

	// A removed row takes its button along: focus stays in the sheet
	async function keepFocus() {
		await tick();
		if (!sheet?.contains(document.activeElement) || document.activeElement === document.body)
			sheet?.querySelector<HTMLElement>('.list .btn, .sheet-footer .btn')?.focus();
	}

	const model = new SessionsList(
		untrack(() => user),
		keepFocus
	);

	$effect(() => {
		untrack(() => void model.load());
	});
</script>

<Sheet
	title={model.user ? t('sessions.user_title', { username: model.user.username }) : t('sessions.title')}
	subtitle={model.user
		? t('sessions.subtitle_user', { username: model.user.username })
		: t('sessions.subtitle_own')}
	status={model.status}
	statusTone={model.statusTone}
	bind:element={sheet}
	{onclose}
>
	<SessionsRows {model} />

	{#snippet footer()}
		{#if model.list && model.others.length}
			<Button variant="danger" disabled={model.busy} onclick={() => model.signOutAll()}>{model.allLabel}</Button>
		{/if}
		<Button onclick={onclose}>{t('actions.close')}</Button>
	{/snippet}
</Sheet>
