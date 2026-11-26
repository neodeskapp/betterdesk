<!--
  A new password for an account, in a centred dialog, one of three ways:
  a link by email where the person sets it (when the console can send mail
  and the account has an address), a temporary password (with
  user.security: typed or made up, shown once with a copy button; the person
  chooses a new one at the next sign-in), or a password set here, with the
  server's rule of at least 8 characters.

  A reset link that is still waiting is named with until when it works, and
  can be cancelled. An LDAP or single sign-on account gets no form: the
  dialog says to reset the password with its provider.
-->
<script lang="ts">
	import { untrack } from 'svelte';
	import {
		cancelResetLink,
		resetUserPassword,
		sendResetLink,
		setTemporaryPassword,
		type ConsoleUser
	} from '#lib/api/users.ts';
	import { formatDate } from '#lib/i18n/format.ts';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import PasswordField from '#lib/people/PasswordField.svelte';
	import { passwordProblem, providerOf } from '#lib/people/users.ts';
	import Button from '#lib/ui/Button.svelte';
	import ChoiceList from '#lib/ui/ChoiceList.svelte';
	import Dialog from '#lib/ui/Dialog.svelte';
	import FormStatus from '#lib/ui/FormStatus.svelte';
	import Notice from '#lib/ui/Notice.svelte';
	import ValueCopy from '#lib/ui/ValueCopy.svelte';
	import { createForm } from '#lib/ui/form.svelte.ts';
	import { toast } from '#lib/ui/toasts.svelte.ts';

	interface Props {
		user: ConsoleUser;
		/** The console has email settings, so links can go out */
		mailReady?: boolean;
		/** The signed-in person has user.security: a temporary password is offered */
		security?: boolean;
		/** Names the account's provider ("LDAP/AD") */
		providerLabel?: (provider: string) => string;
		onclose: () => void;
		/** Something about the account changed (a link sent or cancelled, a temporary password) */
		onchanged?: () => void;
	}

	let {
		user,
		mailReady = false,
		security = false,
		providerLabel = (p) => p,
		onclose,
		onchanged
	}: Props = $props();

	const uid = $props.id();
	const provider = $derived(providerOf(user));
	const canLink = $derived(mailReady && !!user.email);
	/** The temporary password the server set, shown once */
	let shownPassword = $state('');
	/** Until when the waiting reset link works; '' when none waits or it was cancelled */
	let pendingUntil = $state(untrack(() => user.password_reset_until ?? ''));
	let cancelling = $state(false);

	type Mode = 'link' | 'temporary' | 'set';
	const firstMode: Mode = untrack(() => (mailReady && user.email ? 'link' : security ? 'temporary' : 'set'));

	const form = createForm({
		initial: { mode: firstMode as Mode, password: '' },
		validate: (v) => {
			const problem =
				v.mode === 'link'
					? null
					: v.mode === 'temporary'
						? passwordProblem(v.password, false)
						: passwordProblem(v.password, true);
			return { password: problem ? t(problem) : undefined };
		},
		submit: async (v) => {
			if (v.mode === 'link') {
				const { expires_at } = await sendResetLink(user.id);
				toast.success(
					expires_at
						? t('users.reset_link_sent_until', {
								username: user.username,
								date: formatDate(expires_at, {}, '')
							})
						: t('users.reset_link_sent_named', { username: user.username })
				);
				onchanged?.();
				onclose();
			} else if (v.mode === 'temporary') {
				shownPassword = await setTemporaryPassword(user.id, v.password || undefined);
				onchanged?.();
			} else {
				await resetUserPassword(user.id, v.password);
				toast.success(t('users.password_reset_named', { username: user.username }));
				onclose();
			}
		}
	});

	const choices = $derived([
		{
			value: 'link' as Mode,
			title: t('users.reset_mode_link'),
			description: !user.email
				? t(security ? 'users.reset_link_no_email' : 'users.reset_link_no_email_plain', {
						username: user.username
					})
				: mailReady
					? t('users.reset_mode_link_desc', { username: user.username, email: user.email })
					: t('users.invite_needs_email'),
			disabled: !canLink
		},
		...(security
			? [
					{
						value: 'temporary' as Mode,
						title: t('users.reset_mode_temp'),
						description: t('users.reset_mode_temp_desc', { username: user.username })
					}
				]
			: []),
		{ value: 'set' as Mode, title: t('users.reset_mode_set'), description: t('users.reset_mode_set_desc') }
	]);

	async function cancelLink() {
		cancelling = true;
		try {
			await cancelResetLink(user.id);
			pendingUntil = '';
			toast.success(t('users.reset_cancelled_named', { username: user.username }));
			onchanged?.();
		} catch (err) {
			toast.error(err instanceof Error ? err.message : t('errors.server_error'));
		} finally {
			cancelling = false;
		}
	}

	const submitLabel = $derived(
		form.values.mode === 'link'
			? t('users.send_reset_link')
			: form.values.mode === 'temporary'
				? t('users.temp_set_button')
				: t('users.set_password')
	);
</script>

<Dialog
	title={t('users.reset_password_for', { username: user.username })}
	beforeclose={() => !form.busy}
	{onclose}
>
	{#if provider !== 'local'}
		<Notice
			message={t('users.reset_external', { username: user.username, provider: providerLabel(provider) })}
		/>
	{:else if shownPassword}
		<div class="shown">
			<p>{t('users.temp_shown_once', { username: user.username })}</p>
			<div class="shown-value" aria-label={t('users.temp_password')}>
				<ValueCopy value={shownPassword} label={t('users.temp_password')} />
			</div>
		</div>
	{:else}
		{#if pendingUntil}
			<div class="pending">
				<Notice
					tone="info"
					message={t('users.reset_pending', {
						username: user.username,
						date: formatDate(pendingUntil, {}, '')
					})}
				/>
				<div>
					<Button size="sm" class="cancel-link" busy={cancelling} onclick={() => void cancelLink()}
						>{t('users.reset_cancel')}</Button
					>
				</div>
			</div>
		{/if}
		<form id={form.id} onsubmit={form.onsubmit} novalidate>
			<ChoiceList
				{choices}
				label={t('users.reset_password')}
				name="{uid}-mode"
				bind:value={form.values.mode}
			/>
			{#if form.values.mode === 'set'}
				<PasswordField
					label={t('users.new_password')}
					hint={t('users.password_hint')}
					error={form.errors.password}
					autofocus
					bind:value={form.values.password}
				/>
			{:else if form.values.mode === 'temporary'}
				<PasswordField
					label={t('users.temp_password')}
					hint={t('users.temp_password_hint')}
					error={form.errors.password}
					bind:value={form.values.password}
				/>
			{/if}
			<FormStatus status={form.status} />
		</form>
	{/if}
	{#snippet footer()}
		{#if provider !== 'local' || shownPassword}
			<Button variant={shownPassword ? 'primary' : 'secondary'} onclick={onclose}>{t('actions.close')}</Button
			>
		{:else}
			<Button onclick={onclose}>{t('actions.cancel')}</Button>
			<Button variant="primary" type="submit" form={form.id} busy={form.busy}>{submitLabel}</Button>
		{/if}
	{/snippet}
</Dialog>

<style>
	.pending,
	.shown {
		display: flex;
		flex-direction: column;
		gap: var(--space-2);
		margin-bottom: var(--space-4);
	}
	.shown p {
		margin: 0;
	}
	.shown-value {
		font-size: var(--text-md);
	}
</style>
