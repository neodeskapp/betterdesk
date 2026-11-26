<!--
  The user's page › Edit profile: first and last name, which make the
  display name, and the time zone the person's mails give times in (three
  fields, a centred dialog; PUT /api/users/{id}/profile, user.edit). For an
  LDAP or single sign-on account it says that the directory or the
  identity provider sets the name again at the next sign-in.
-->
<script lang="ts">
	import { untrack } from 'svelte';
	import { saveUserProfile, type UserDetail } from '#lib/api/users.ts';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import { timeZoneOptions } from '#lib/people/time-zones.ts';
	import { providerOf } from '#lib/people/users.ts';
	import Button from '#lib/ui/Button.svelte';
	import Dialog from '#lib/ui/Dialog.svelte';
	import Field from '#lib/ui/Field.svelte';
	import FormStatus from '#lib/ui/FormStatus.svelte';
	import Input from '#lib/ui/Input.svelte';
	import Notice from '#lib/ui/Notice.svelte';
	import Select from '#lib/ui/Select.svelte';
	import { createForm } from '#lib/ui/form.svelte.ts';
	import { toast } from '#lib/ui/toasts.svelte.ts';

	interface Props {
		user: UserDetail;
		providerLabel: (provider: string) => string;
		onclose: () => void;
		onsaved: (user: UserDetail) => void;
	}

	let { user, providerLabel, onclose, onsaved }: Props = $props();

	const provider = $derived(providerOf(user));
	const zones = $derived(timeZoneOptions(t('users.time_zone_none'), user.time_zone));

	const form = createForm({
		initial: untrack(() => ({ first: user.first_name, last: user.last_name, zone: user.time_zone })),
		submit: async (v) => {
			const saved = await saveUserProfile(user.id, {
				first_name: v.first.trim(),
				last_name: v.last.trim(),
				time_zone: v.zone
			});
			toast.success(t('users.profile_saved', { username: saved.display_name || saved.username }));
			onsaved(saved);
		}
	});
</script>

<Dialog
	title={t('users.edit_profile_title', { username: user.username })}
	beforeclose={() => !form.busy}
	{onclose}
>
	<form id={form.id} onsubmit={form.onsubmit} novalidate>
		{#if provider !== 'local'}
			<Notice tone="info" message={t('users.profile_from_provider', { provider: providerLabel(provider) })} />
		{/if}
		<div class="dialog-cols">
			<Field label={t('users.first_name')}>
				{#snippet children(f)}
					<Input {...f} maxlength={200} autocomplete="off" bind:value={form.values.first} />
				{/snippet}
			</Field>
			<Field label={t('users.last_name')}>
				{#snippet children(f)}
					<Input {...f} maxlength={200} autocomplete="off" bind:value={form.values.last} />
				{/snippet}
			</Field>
		</div>
		<p class="form-hint">{t('users.display_name_hint', { username: user.username })}</p>
		<Field label={t('users.time_zone')} hint={t('users.time_zone_hint')}>
			{#snippet children(f)}
				<Select {...f} width="medium" options={zones} bind:value={form.values.zone} />
			{/snippet}
		</Field>
		<FormStatus status={form.status} />
	</form>
	{#snippet footer()}
		<Button onclick={onclose}>{t('actions.cancel')}</Button>
		<Button variant="primary" type="submit" form={form.id} busy={form.busy}>{t('actions.save')}</Button>
	{/snippet}
</Dialog>

<style>
	.form-hint {
		margin: 0 0 var(--space-4);
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
</style>
