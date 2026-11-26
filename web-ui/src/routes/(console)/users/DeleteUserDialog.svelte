<!--
  Delete user: before the person confirms, the dialog says what goes with
  the account and what stays, with the numbers the server counts (GET
  /api/users/{id}/delete-preview), and offers to hand over the account's
  API tokens, organization memberships, saved reports and open tickets to
  another account (one the signed-in person may change). Deleting sends
  DELETE /api/users/{id}?handover_to=<id>.
-->
<script lang="ts">
	import { untrack } from 'svelte';
	import { deleteUser, getDeletePreview, type ConsoleUser, type DeletePreview } from '#lib/api/users.ts';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import { deleteConfirmKey } from '#lib/people/users.ts';
	import Button from '#lib/ui/Button.svelte';
	import Dialog from '#lib/ui/Dialog.svelte';
	import Field from '#lib/ui/Field.svelte';
	import FormStatus from '#lib/ui/FormStatus.svelte';
	import Select from '#lib/ui/Select.svelte';
	import States from '#lib/ui/States.svelte';
	import { toast } from '#lib/ui/toasts.svelte.ts';

	interface Props {
		user: ConsoleUser;
		roleLabel: (role: string) => string;
		onclose: () => void;
		ondeleted: () => void;
	}

	let { user, roleLabel, onclose, ondeleted }: Props = $props();

	let preview = $state.raw<DeletePreview | null>(null);
	let loadError = $state('');
	let handover = $state('');
	let busy = $state(false);
	let error = $state('');

	async function load() {
		loadError = '';
		try {
			preview = await getDeletePreview(user.id);
		} catch (err) {
			loadError = err instanceof Error && err.message ? err.message : t('users.delete_preview_failed');
		}
	}

	$effect(() => {
		untrack(() => void load());
	});

	const receiver = $derived(preview?.candidates.find((c) => String(c.id) === handover) ?? null);

	type Line = { key: string; count?: number; moves?: boolean };
	/** What goes with the account; the lines a hand-over moves say so */
	const goes = $derived.by<Line[]>(() => {
		const c = preview?.counts;
		if (!c) return [];
		const moving = !!receiver;
		const lines: Line[] = [
			{ key: 'users.delete_item_account' },
			{ key: 'users.delete_item_sessions', count: c.sessions },
			{ key: 'users.delete_item_client_sign_ins', count: c.client_sign_ins },
			{ key: 'users.delete_item_api_tokens', count: c.api_tokens, moves: moving },
			{
				key: 'users.delete_item_organizations',
				count: c.organizations,
				moves: moving && !!preview?.organizations_move
			},
			{ key: 'users.delete_item_user_groups', count: c.user_groups },
			{ key: 'users.delete_item_device_grants', count: c.device_grants },
			{ key: 'users.delete_item_access' }
		];
		if (moving) {
			lines.push(
				{ key: 'users.delete_item_saved_reports', count: c.saved_reports, moves: true },
				{ key: 'users.delete_item_open_tickets', count: c.open_tickets, moves: true }
			);
		}
		return lines.filter((l) => l.count === undefined || l.count > 0);
	});
	const stays = $derived.by<Line[]>(() => {
		const c = preview?.counts;
		if (!c) return [];
		return [
			{ key: 'users.delete_item_audit', count: c.audit_entries },
			{ key: 'users.delete_item_tickets_opened', count: c.tickets_opened },
			{ key: 'users.delete_item_ticket_comments', count: c.ticket_comments },
			{ key: 'users.delete_item_chat', count: c.chat_messages }
		].filter((l) => l.count > 0);
	});

	const options = $derived([
		{ value: '', label: t('users.delete_handover_none') },
		...(preview?.candidates ?? []).map((c) => ({
			value: String(c.id),
			label: `${c.username} (${roleLabel(c.role)})`
		}))
	]);

	async function remove() {
		busy = true;
		error = '';
		try {
			await deleteUser(user.id, receiver ? receiver.id : undefined);
			toast.success(
				receiver
					? t('users.handed_over_named', { username: user.username, to: receiver.username })
					: t('users.deleted_named', { username: user.username })
			);
			ondeleted();
		} catch (err) {
			error = err instanceof Error && err.message ? err.message : t('errors.server_error');
		} finally {
			busy = false;
		}
	}
</script>

{#snippet lines(items: Line[])}
	<ul class="delete-lines">
		{#each items as l (l.key)}
			<li>
				<span>{t(l.key)}</span>{#if l.count !== undefined}<span class="delete-count">{l.count}</span>{/if}
				{#if l.moves && receiver}<span class="delete-moves"
						>{t('users.delete_moves', { username: receiver.username })}</span
					>{/if}
			</li>
		{/each}
	</ul>
{/snippet}

<Dialog title={t('users.delete_title')} beforeclose={() => !busy} {onclose}>
	<p class="delete-intro">{t(deleteConfirmKey(user), { username: user.username })}</p>
	{#if loadError}
		<States kind="error" message={loadError} onretry={() => void load()} />
	{:else if !preview}
		<States kind="loading" />
	{:else}
		<section class="delete-part" aria-labelledby="delete-goes-title">
			<h3 id="delete-goes-title">{t('users.delete_goes_title')}</h3>
			{@render lines(goes)}
		</section>
		{#if stays.length}
			<section class="delete-part" aria-labelledby="delete-stays-title">
				<h3 id="delete-stays-title">{t('users.delete_stays_title')}</h3>
				{@render lines(stays)}
			</section>
		{/if}
		{#if preview.candidates.length}
			<Field
				label={t('users.delete_handover_label')}
				hint={preview.organizations_move
					? t('users.delete_handover_hint')
					: t('users.delete_handover_hint_no_orgs')}
			>
				{#snippet children(f)}
					<Select {...f} width="medium" {options} bind:value={handover} />
				{/snippet}
			</Field>
		{/if}
		{#if !receiver && (preview.counts.saved_reports || preview.counts.open_tickets)}
			<p class="delete-note">{t('users.delete_without_handover', { username: user.username })}</p>
		{/if}
	{/if}
	<FormStatus message={error} tone="error" />
	{#snippet footer()}
		<Button onclick={onclose} disabled={busy}>{t('actions.cancel')}</Button>
		<Button variant="danger" {busy} disabled={!preview} onclick={() => void remove()}
			>{t('users.delete_action')}</Button
		>
	{/snippet}
</Dialog>

<style>
	.delete-intro,
	.delete-note {
		margin: 0 0 var(--space-4);
	}
	.delete-note {
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	.delete-part {
		margin: 0 0 var(--space-4);
	}
	.delete-part h3 {
		margin: 0 0 var(--space-2);
		font-size: var(--text-md);
	}
	.delete-lines {
		margin: 0;
		padding: 0;
		list-style: none;
		border-top: 1px solid var(--rule);
	}
	.delete-lines li {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-1) var(--space-3);
		padding-block: var(--space-2);
		border-bottom: 1px solid var(--rule);
	}
	.delete-lines li > span:first-child {
		flex: 1 1 12rem;
	}
	.delete-count {
		font-variant-numeric: tabular-nums;
		font-weight: 600;
	}
	.delete-moves {
		flex-basis: 100%;
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
</style>
