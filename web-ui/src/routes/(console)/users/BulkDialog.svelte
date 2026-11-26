<!--
  An action on several accounts (the selection of the users list). It first
  asks the server what the action would do to each account (a dry run of
  the same route, with the same checks), and says how many accounts it
  changes, which are so already, and which it leaves out and why. The
  action runs only after Confirm; then it says per account what happened.
  The role and the organization actions ask for the role or organization
  first. Closing after a run reloads the list.
-->
<script lang="ts">
	import { untrack } from 'svelte';
	import type { Organization } from '#lib/api/orgs.ts';
	import { runBulk, type BulkKind, type BulkResult, type ConsoleUser } from '#lib/api/users.ts';
	import { plural, t } from '#lib/i18n/i18n.svelte.ts';
	import { summarize, type BulkSummary } from '#lib/people/userList.ts';
	import Button from '#lib/ui/Button.svelte';
	import Dialog from '#lib/ui/Dialog.svelte';
	import Field from '#lib/ui/Field.svelte';
	import Notice from '#lib/ui/Notice.svelte';
	import Select from '#lib/ui/Select.svelte';

	interface Props {
		kind: BulkKind;
		users: ConsoleUser[];
		/** The roles the signed-in person may give */
		roles: string[];
		roleLabel: (role: string) => string;
		orgs: Organization[];
		onclose: () => void;
		/** The action ran: the list loads again */
		ondone: () => void;
	}

	let { kind, users, roles, roleLabel, orgs, onclose, ondone }: Props = $props();

	/** The part of the text keys for each action */
	const KEYS: Record<BulkKind, string> = {
		role: 'role',
		'organization-add': 'org_add',
		'organization-remove': 'org_remove',
		'two-factor-required': 'two_factor',
		'require-password-change': 'password_change',
		disable: 'disable',
		enable: 'enable',
		'password-reset-link': 'reset_link',
		delete: 'delete'
	};
	const key = $derived(KEYS[kind]);
	const danger = $derived(kind === 'disable' || kind === 'delete' || kind === 'organization-remove');
	const asks = $derived(kind === 'role' ? 'role' : kind.startsWith('organization') ? 'org' : null);

	let role = $state('');
	let orgId = $state('');
	const params = $derived<Record<string, unknown>>(
		kind === 'role'
			? { role }
			: asks === 'org'
				? { org_id: orgId }
				: kind === 'two-factor-required' || kind === 'require-password-change'
					? { required: true }
					: {}
	);
	const ready = $derived(asks === 'role' ? !!role : asks === 'org' ? !!orgId : true);
	const ids = untrack(() => users.map((u) => u.id));

	let phase = $state<'choose' | 'checking' | 'preview' | 'running' | 'done'>('choose');
	let preview = $state.raw<BulkSummary | null>(null);
	let result = $state.raw<BulkSummary | null>(null);
	let error = $state('');

	const message = (err: unknown) =>
		err instanceof Error && err.message ? err.message : t('errors.server_error');

	// What the action would do: asked again when the role or organization changes
	$effect(() => {
		if (!ready) return;
		const p = params;
		let stale = false;
		untrack(() => {
			phase = 'checking';
			error = '';
			preview = null;
		});
		runBulk(kind, ids, p, true).then(
			(r) => {
				if (stale) return;
				preview = summarize(r);
				phase = 'preview';
			},
			(err) => {
				if (stale) return;
				error = message(err);
				phase = 'choose';
			}
		);
		return () => {
			stale = true;
		};
	});

	async function run() {
		phase = 'running';
		error = '';
		try {
			result = summarize(await runBulk(kind, ids, params, false));
			phase = 'done';
		} catch (err) {
			error = message(err);
			phase = 'preview';
		}
	}

	function close() {
		if (result) ondone();
		else onclose();
	}

	const names = (list: BulkResult[]) => list.map((r) => r.username || String(r.id)).join(', ');
	const roleOptions = $derived([
		{ value: '', label: t('users.bulk_choose_role'), disabled: true },
		...roles.map((r) => ({ value: r, label: roleLabel(r) }))
	]);
	const orgOptions = $derived([
		{ value: '', label: t('users.bulk_choose_org'), disabled: true },
		...orgs.map((o) => ({ value: String(o.id), label: o.name || String(o.id) }))
	]);
</script>

{#snippet reasons(title: string, list: BulkResult[])}
	{#if list.length}
		<h3 class="bulk-heading">{title}</h3>
		<ul class="bulk-list">
			{#each list as r (String(r.id))}
				<li>
					<strong>{r.username || r.id}</strong>{#if r.error}: {r.error}{/if}
				</li>
			{/each}
		</ul>
	{/if}
{/snippet}

<Dialog
	title={t(`users.bulk_${key}_title`)}
	message={t(`users.bulk_${key}_effect`)}
	class="bulk-dialog"
	blocking={phase === 'running'}
	onclose={close}
>
	{#if asks === 'role'}
		<Field label={t('users.bulk_role_label')}>
			{#snippet children(f)}<Select
					{...f}
					class="bulk-role"
					options={roleOptions}
					bind:value={role}
				/>{/snippet}
		</Field>
	{:else if asks === 'org'}
		{#if orgs.length}
			<Field label={t('users.bulk_org_label')}>
				{#snippet children(f)}<Select
						{...f}
						class="bulk-org"
						options={orgOptions}
						bind:value={orgId}
					/>{/snippet}
			</Field>
		{:else}
			<Notice tone="warning" message={t('users.bulk_no_orgs')} />
		{/if}
	{/if}

	<div class="bulk-body" aria-live="polite">
		{#if error}
			<Notice tone="danger" role="alert" message={error} />
		{/if}
		{#if phase === 'checking'}
			<p class="text-muted">{t('users.bulk_checking')}</p>
		{:else if phase === 'done' && result}
			<p class="bulk-done">
				{plural(result.changed.length, { one: t('users.bulk_done_one'), other: t('users.bulk_done_many') })}
			</p>
			{#if result.unchanged.length}
				<p class="text-muted">
					{plural(result.unchanged.length, {
						one: t('users.bulk_already_one'),
						other: t('users.bulk_already_many')
					})}
				</p>
			{/if}
			{@render reasons(t('users.bulk_left_out'), result.refused)}
			{@render reasons(t('users.bulk_failed'), result.failed)}
		{:else if preview}
			{#if preview.changed.length}
				<p class="bulk-changes">
					{plural(preview.changed.length, {
						one: t('users.bulk_changes_one'),
						other: t('users.bulk_changes_many')
					})}
					<span class="text-muted">{names(preview.changed)}</span>
				</p>
			{:else}
				<Notice message={t('users.bulk_nothing')} />
			{/if}
			{#if preview.unchanged.length}
				<p class="text-muted">
					{plural(preview.unchanged.length, {
						one: t('users.bulk_already_one'),
						other: t('users.bulk_already_many')
					})}
				</p>
			{/if}
			{@render reasons(t('users.bulk_left_out'), [...preview.refused, ...preview.failed])}
		{/if}
	</div>

	{#snippet footer()}
		{#if phase === 'done'}
			<Button variant="primary" onclick={close}>{t('actions.close')}</Button>
		{:else}
			<Button onclick={close} disabled={phase === 'running'}>{t('actions.cancel')}</Button>
			<Button
				class="bulk-confirm"
				variant={danger ? 'danger' : 'primary'}
				busy={phase === 'running'}
				disabled={phase !== 'preview' || !preview?.changed.length}
				onclick={() => void run()}
				>{plural(preview?.changed.length ?? 0, {
					one: t(`users.bulk_${key}_confirm_one`),
					other: t(`users.bulk_${key}_confirm_many`)
				})}</Button
			>
		{/if}
	{/snippet}
</Dialog>

<style>
	.bulk-body {
		display: flex;
		flex-direction: column;
		gap: var(--space-2);
		margin-top: var(--space-3);
	}
	.bulk-body p {
		margin: 0;
	}
	.bulk-changes .text-muted {
		display: block;
		font-size: var(--text-sm);
		overflow-wrap: anywhere;
	}
	.bulk-heading {
		margin: var(--space-2) 0 0;
		font-size: var(--text-sm);
		font-weight: 600;
	}
	.bulk-list {
		margin: 0;
		padding-inline-start: var(--space-5);
		font-size: var(--text-sm);
	}
	.bulk-list li + li {
		margin-top: var(--space-1);
	}
</style>
