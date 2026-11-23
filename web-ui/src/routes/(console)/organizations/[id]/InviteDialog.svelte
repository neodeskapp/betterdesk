<!--
  A new invitation: email (optional) and how long it is valid (two fields,
  a centred dialog). Once it is made the dialog shows the invitation's
  link to copy: the server keeps only a hash of its code, so this is the
  one time the link is shown. Closing the dialog then counts as saved, so
  the list shows the new invitation.
-->
<script lang="ts">
	import { createInvitation } from '#lib/api/orgs.ts';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import { emailOk, invitationLink } from '#lib/people/orgs.ts';
	import Button from '#lib/ui/Button.svelte';
	import Dialog from '#lib/ui/Dialog.svelte';
	import Field from '#lib/ui/Field.svelte';
	import FormStatus from '#lib/ui/FormStatus.svelte';
	import Input from '#lib/ui/Input.svelte';
	import Notice from '#lib/ui/Notice.svelte';
	import ValueCopy from '#lib/ui/ValueCopy.svelte';
	import { createForm } from '#lib/ui/form.svelte.ts';
	import { toast } from '#lib/ui/toasts.svelte.ts';

	let { orgId, onclose, onsaved }: { orgId: string; onclose: () => void; onsaved: () => void } = $props();

	let link = $state('');

	const form = createForm({
		initial: { email: '', hours: 72 as number | string },
		validate: (v) => {
			const n = Number(v.hours);
			const email = v.email.trim();
			return {
				email: !email || emailOk(email) ? undefined : t('organizations.email_invalid'),
				hours: Number.isInteger(n) && n >= 1 && n <= 720 ? undefined : t('organizations.expires_hours_hint')
			};
		},
		submit: async (v) => {
			const created = await createInvitation(orgId, {
				email: v.email.trim(),
				expires_in_hours: Number(v.hours) || 72
			});
			link = invitationLink(location.origin, created.token);
			toast.success(t('organizations.invitation_created'));
		}
	});
</script>

<Dialog
	title={t('organizations.create_invitation')}
	beforeclose={() => !form.busy}
	onclose={() => (link ? onsaved() : onclose())}
>
	{#if link}
		<div class="invite-link">
			<Notice message={t('organizations.invitation_link_note')} />
			<p class="link-label">{t('organizations.invitation_link_label')}</p>
			<span class="link-value"
				><ValueCopy value={link} label={t('organizations.invitation_link_label')} /></span
			>
		</div>
	{:else}
		<form id={form.id} onsubmit={form.onsubmit} novalidate>
			<Field label={t('organizations.email')} hint={t('organizations.optional')} error={form.errors.email}>
				{#snippet children(f)}
					<Input {...f} type="email" width="host" autocomplete="off" bind:value={form.values.email} />
				{/snippet}
			</Field>
			<Field
				label={t('organizations.expires_hours')}
				hint={t('organizations.expires_hours_hint')}
				error={form.errors.hours}
			>
				{#snippet children(f)}
					<Input {...f} type="number" width="short" min={1} max={720} bind:value={form.values.hours} />
				{/snippet}
			</Field>
			<FormStatus status={form.status} />
		</form>
	{/if}
	{#snippet footer()}
		{#if link}
			<Button variant="primary" class="invite-done" onclick={onsaved}>{t('common.close')}</Button>
		{:else}
			<Button onclick={onclose}>{t('actions.cancel')}</Button>
			<Button variant="primary" type="submit" form={form.id} busy={form.busy}
				>{t('organizations.create_invitation')}</Button
			>
		{/if}
	{/snippet}
</Dialog>

<style>
	.invite-link {
		display: flex;
		flex-direction: column;
		gap: var(--space-2);
	}
	.link-label {
		margin: var(--space-2) 0 0;
		font-weight: 600;
	}
	.link-value {
		display: block;
		max-width: 100%;
		overflow-wrap: anywhere;
	}
</style>
