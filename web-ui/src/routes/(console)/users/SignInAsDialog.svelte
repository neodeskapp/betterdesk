<!--
  Sign in as… (users row menu, access group): what it means, then the
  admin's own password (and two-factor code when that is on) and a reason,
  and Start. The server says first whether this account may be viewed
  (GET /api/users/{id}/impersonation): when not, the dialog says why and
  offers no Start. After Start the console loads again as the account,
  with the bar at the top; the other tabs of the browser load again too.
-->
<script lang="ts">
	import { untrack } from 'svelte';
	import { ApiError } from '#lib/api/client.ts';
	import {
		announceImpersonationChange,
		checkImpersonation,
		startImpersonation,
		type ImpersonationCheck
	} from '#lib/api/impersonation.ts';
	import type { ConsoleUser } from '#lib/api/users.ts';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import Dialog from '#lib/ui/Dialog.svelte';
	import Field from '#lib/ui/Field.svelte';
	import FormStatus from '#lib/ui/FormStatus.svelte';
	import Input from '#lib/ui/Input.svelte';
	import Notice from '#lib/ui/Notice.svelte';
	import States from '#lib/ui/States.svelte';
	import Textarea from '#lib/ui/Textarea.svelte';
	import { createForm, FormError } from '#lib/ui/form.svelte.ts';

	let { user, onclose }: { user: ConsoleUser; onclose: () => void } = $props();

	let check = $state.raw<ImpersonationCheck | null>(null);
	let checkError = $state<string | null>(null);

	async function load() {
		checkError = null;
		try {
			check = await checkImpersonation(user.id);
		} catch (err) {
			checkError = err instanceof ApiError ? err.message : t('errors.server_error');
		}
	}
	$effect(() => {
		untrack(() => void load());
	});

	/** Where the server's refusal of a value goes */
	const FIELD_OF: Record<string, 'password' | 'code' | 'reason'> = {
		impersonation_password_required: 'password',
		impersonation_wrong_password: 'password',
		impersonation_code_required: 'code',
		impersonation_wrong_code: 'code',
		impersonation_reason_required: 'reason'
	};

	const form = createForm({
		initial: { password: '', code: '', reason: '' },
		validate: (v) => ({
			password: check?.needs.password && !v.password ? t('impersonation.password_required') : undefined,
			code: check?.needs.code && !v.code.trim() ? t('impersonation.code_required') : undefined,
			reason: v.reason.trim().length < 3 ? t('impersonation.reason_required') : undefined
		}),
		submit: async (v) => {
			try {
				await startImpersonation(user.id, {
					password: check?.needs.password ? v.password : undefined,
					code: check?.needs.code ? v.code.trim() : undefined,
					reason: v.reason.trim()
				});
			} catch (err) {
				const field = err instanceof ApiError && err.code ? FIELD_OF[err.code] : undefined;
				if (field) throw new FormError(err instanceof Error ? err.message : '', field);
				throw err;
			}
			announceImpersonationChange();
			location.assign('/');
		}
	});
</script>

<Dialog
	title={t('impersonation.dialog_title', { username: user.username })}
	beforeclose={() => !form.busy}
	{onclose}
>
	{#if check === null && !checkError}
		<States kind="loading" />
	{:else if checkError}
		<States kind="error" message={checkError} onretry={() => void load()} />
	{:else if check && !check.allowed}
		<Notice tone="warning" message={check.error} />
	{:else if check}
		<form id={form.id} onsubmit={form.onsubmit} novalidate>
			<p>{t('impersonation.dialog_intro', { username: user.username, minutes: check.minutes })}</p>
			<p class="note">
				{check.notice_mail
					? t('impersonation.dialog_mail', { username: user.username })
					: t('impersonation.dialog_no_mail', { username: user.username })}
			</p>
			{#if check.needs.password}
				<Field label={t('impersonation.your_password')} error={form.errors.password}>
					{#snippet children(f)}<Input
							{...f}
							type="password"
							autocomplete="current-password"
							data-autofocus
							bind:value={form.values.password}
						/>{/snippet}
				</Field>
			{/if}
			{#if check.needs.code}
				<Field label={t('impersonation.your_code')} error={form.errors.code} width="short">
					{#snippet children(f)}<Input
							{...f}
							inputmode="numeric"
							autocomplete="one-time-code"
							maxlength={6}
							data-autofocus={!check?.needs.password || undefined}
							bind:value={form.values.code}
						/>{/snippet}
				</Field>
			{/if}
			<Field
				label={t('impersonation.reason')}
				hint={t('impersonation.reason_hint')}
				error={form.errors.reason}
			>
				{#snippet children(f)}<Textarea
						{...f}
						rows={2}
						maxlength={300}
						bind:value={form.values.reason}
					/>{/snippet}
			</Field>
			<FormStatus status={form.status} />
		</form>
	{/if}
	{#snippet footer()}
		<Button onclick={onclose}>{t('actions.cancel')}</Button>
		{#if check?.allowed}
			<Button variant="primary" type="submit" form={form.id} busy={form.busy}
				>{t('impersonation.start')}</Button
			>
		{/if}
	{/snippet}
</Dialog>

<style>
	form {
		display: flex;
		flex-direction: column;
		gap: var(--space-4);
	}
	p {
		margin: 0;
	}
	.note {
		color: var(--text-muted);
	}
</style>
