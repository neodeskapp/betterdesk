<!--
  Your account › Email address (#email): the signed-in person's address,
  where password links and notices about the account go. A new address
  gets a confirmation link and changes once it is used; until then the
  old one stays, the part shows the new one as Waiting for confirmation,
  with Send the link again and Cancel the change. Without the console's
  email settings a new address takes effect at once. A directory or
  single sign-on account gets its address from there: the part says so
  and has no form.
-->
<script lang="ts">
	import { untrack } from 'svelte';
	import {
		cancelOwnEmailChange,
		changeOwnEmail,
		getOwnEmail,
		resendOwnEmailChange,
		type OwnEmail
	} from '#lib/api/users.ts';
	import { formatDate } from '#lib/i18n/format.ts';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import Field from '#lib/ui/Field.svelte';
	import FormStatus from '#lib/ui/FormStatus.svelte';
	import Input from '#lib/ui/Input.svelte';
	import SaveBar from '#lib/ui/SaveBar.svelte';
	import States from '#lib/ui/States.svelte';
	import Status from '#lib/ui/Status.svelte';
	import { createForm } from '#lib/ui/form.svelte.ts';

	let current = $state.raw<OwnEmail | null>(null);
	let loadError = $state('');
	let busy = $state(false);
	let line = $state<{ message: string; tone: 'neutral' | 'ok' | 'error' }>({ message: '', tone: 'neutral' });

	const errText = (err: unknown) =>
		err instanceof Error && err.message ? err.message : t('errors.server_error');

	async function load() {
		try {
			current = await getOwnEmail();
			loadError = '';
		} catch (err) {
			loadError = errText(err);
		}
	}

	$effect(() => {
		untrack(() => void load());
	});

	const external = $derived(!!current && current.auth_provider !== '' && current.auth_provider !== 'local');

	const form = createForm({
		initial: { email: '' },
		validate: (v) => ({
			email:
				v.email.trim() && !/^[^\s@]+@[^\s@]+$/.test(v.email.trim()) ? t('users.invalid_email') : undefined
		}),
		submit: async (v) => {
			const next = await changeOwnEmail(v.email.trim());
			current = next;
			line = next.email_pending
				? { message: t('users.email_change_sent_named', { email: next.email_pending.email }), tone: 'ok' }
				: { message: t('ui_form.saved'), tone: 'ok' };
			return { email: '' };
		}
	});

	async function act(run: () => Promise<OwnEmail>, done: string) {
		busy = true;
		try {
			current = await run();
			line = { message: done, tone: 'ok' };
		} catch (err) {
			line = { message: errText(err), tone: 'error' };
		} finally {
			busy = false;
		}
	}
</script>

<section class="section" id="email" aria-labelledby="email-title">
	<h2 id="email-title">{t('account.email_title')}</h2>
	<p class="section-note">{t('account.email_note')}</p>
	{#if loadError}
		<States kind="error" message={loadError} onretry={() => void load()} />
	{:else if !current}
		<States kind="loading" />
	{:else}
		<p class="email-now">
			{#if current.email}<span class="email-value">{current.email}</span>{:else}<span class="text-muted"
					>{t('account.email_none')}</span
				>{/if}
		</p>
		{#if current.email_pending}
			{@const pending = current.email_pending}
			<div class="email-pending">
				<Status
					tone={pending.expired ? 'warning' : 'waiting'}
					label={t('users.email_pending', { email: pending.email })}
				/>
				<p class="form-hint">
					{pending.expired
						? t('users.email_pending_expired')
						: t('users.email_pending_until', { date: formatDate(pending.expires_at, {}, '') })}
				</p>
				<div class="email-actions">
					{#if current.mail_ready}
						<Button
							size="sm"
							disabled={busy}
							onclick={() =>
								void act(resendOwnEmailChange, t('users.email_change_sent_named', { email: pending.email }))}
							>{t('users.email_change_resend')}</Button
						>
					{/if}
					<Button
						size="sm"
						variant="danger-quiet"
						disabled={busy}
						onclick={() => void act(cancelOwnEmailChange, t('users.email_change_cancelled'))}
						>{t('users.email_change_cancel')}</Button
					>
				</div>
			</div>
		{/if}
		{#if external}
			<p class="form-hint">
				{t('users.email_from_provider_hint', {
					provider: t(`users.provider_${current.auth_provider.trim().toLowerCase()}`)
				})}
			</p>
		{:else}
			<form id={form.id} class="email-form" onsubmit={form.onsubmit} novalidate>
				<Field
					id="email-new"
					label={t('account.email_new')}
					hint={current.mail_ready ? t('users.email_hint_confirm') : t('users.email_hint_no_mail')}
					error={form.errors.email}
				>
					{#snippet children(f)}
						<Input
							{...f}
							type="email"
							width="host"
							autocomplete="email"
							spellcheck="false"
							bind:value={form.values.email}
						/>
					{/snippet}
				</Field>
				<SaveBar {form} label={t('account.email_change')} />
			</form>
		{/if}
		<FormStatus message={line.message} tone={line.tone} />
	{/if}
</section>

<style>
	.email-now {
		margin: 0 0 var(--space-3);
	}
	.email-value {
		overflow-wrap: anywhere;
		font-weight: 600;
	}
	.email-pending {
		display: flex;
		flex-direction: column;
		gap: var(--space-2);
		max-width: var(--measure);
		margin-bottom: var(--space-4);
	}
	.email-pending .form-hint {
		margin: 0;
	}
	.email-actions {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
	}
	.email-form {
		display: flex;
		flex-direction: column;
		gap: var(--space-2);
		max-width: var(--measure);
	}
	.form-hint {
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
</style>
