<!--
  A new password for a local account, in a centred dialog: a link by email
  where the person sets it (when the console can send mail and the account
  has an address), or a password set here, with the server's rule of at
  least 8 characters.
-->
<script lang="ts">
	import { untrack } from 'svelte';
	import { resetUserPassword, sendResetLink, type ConsoleUser } from '#lib/api/users.ts';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import PasswordField from '#lib/people/PasswordField.svelte';
	import { passwordProblem } from '#lib/people/users.ts';
	import Button from '#lib/ui/Button.svelte';
	import ChoiceList from '#lib/ui/ChoiceList.svelte';
	import Dialog from '#lib/ui/Dialog.svelte';
	import FormStatus from '#lib/ui/FormStatus.svelte';
	import { createForm } from '#lib/ui/form.svelte.ts';
	import { toast } from '#lib/ui/toasts.svelte.ts';

	let {
		user,
		mailReady = false,
		onclose
	}: { user: ConsoleUser; mailReady?: boolean; onclose: () => void } = $props();

	const uid = $props.id();
	const canLink = $derived(mailReady && !!user.email);

	const form = createForm({
		initial: untrack(() => ({ mode: mailReady && user.email ? 'link' : 'set', password: '' })),
		validate: (v) => {
			const problem = v.mode === 'link' ? null : passwordProblem(v.password, true);
			return { password: problem ? t(problem) : undefined };
		},
		submit: async (v) => {
			if (v.mode === 'link') {
				await sendResetLink(user.id);
				toast.success(t('users.reset_link_sent_named', { username: user.username }));
			} else {
				await resetUserPassword(user.id, v.password);
				toast.success(t('users.password_reset_named', { username: user.username }));
			}
			onclose();
		}
	});

	const choices = $derived([
		{
			value: 'link',
			title: t('users.reset_mode_link'),
			description: !user.email
				? t('users.reset_link_no_email', { username: user.username })
				: mailReady
					? t('users.reset_mode_link_desc', { username: user.username, email: user.email })
					: t('users.invite_needs_email'),
			disabled: !canLink
		},
		{ value: 'set', title: t('users.reset_mode_set'), description: t('users.reset_mode_set_desc') }
	]);
</script>

<Dialog
	title={t('users.reset_password_for', { username: user.username })}
	beforeclose={() => !form.busy}
	{onclose}
>
	<form id={form.id} onsubmit={form.onsubmit} novalidate>
		<ChoiceList {choices} label={t('users.reset_password')} name="{uid}-mode" bind:value={form.values.mode} />
		{#if form.values.mode === 'set'}
			<PasswordField
				label={t('users.new_password')}
				hint={t('users.password_hint')}
				error={form.errors.password}
				autofocus
				bind:value={form.values.password}
			/>
		{/if}
		<FormStatus status={form.status} />
	</form>
	{#snippet footer()}
		<Button onclick={onclose}>{t('actions.cancel')}</Button>
		<Button variant="primary" type="submit" form={form.id} busy={form.busy}
			>{form.values.mode === 'link' ? t('users.send_reset_link') : t('users.set_password')}</Button
		>
	{/snippet}
</Dialog>
