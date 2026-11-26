<!--
  New or edit a user group: a name and a note (two fields, a centred
  dialog). Groups limit which devices their members see. The dialog says
  what a group does and where its members and access are set (PATTERN.md
  rules 5 and 6).
-->
<script lang="ts">
	import { untrack } from 'svelte';
	import { saveUserGroup, type UserGroupRow } from '#lib/api/users.ts';
	import { t, tf } from '#lib/i18n/i18n.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import Dialog from '#lib/ui/Dialog.svelte';
	import Field from '#lib/ui/Field.svelte';
	import FormStatus from '#lib/ui/FormStatus.svelte';
	import Input from '#lib/ui/Input.svelte';
	import Textarea from '#lib/ui/Textarea.svelte';
	import { createForm } from '#lib/ui/form.svelte.ts';
	import { toast } from '#lib/ui/toasts.svelte.ts';

	interface Props {
		group: UserGroupRow | null;
		onclose: () => void;
		onsaved: () => void;
	}

	let { group, onclose, onsaved }: Props = $props();

	const form = createForm({
		initial: untrack(() => ({ name: group?.name ?? '', note: group?.note ?? '' })),
		validate: (v) => ({ name: v.name.trim() ? undefined : t('users.group_name_required') }),
		submit: async (v) => {
			const name = v.name.trim();
			await saveUserGroup(group?.guid ?? null, { name, note: v.note.trim() });
			toast.success(t(group ? 'users.group_updated_named' : 'users.group_created_named', { name }));
			onsaved();
		}
	});
</script>

<Dialog
	title={group ? t('users.edit_user_group') : t('users.create_user_group_title')}
	beforeclose={() => !form.busy}
	{onclose}
>
	<form id={form.id} onsubmit={form.onsubmit} novalidate>
		<p class="intro">
			{tf(
				'users.group_dialog_intro',
				'Members of a group share its console access and the device groups that allow it. Add people in their user sheet; allow the group in a device group’s access settings.'
			)}
		</p>
		<Field
			label={t('users.group_name')}
			hint={tf(
				'users.group_name_hint',
				'Shown in the user list, in each member’s user sheet and in access settings.'
			)}
			error={form.errors.name}
		>
			{#snippet children(f)}
				<Input {...f} maxlength={80} autocomplete="off" required bind:value={form.values.name} />
			{/snippet}
		</Field>
		<Field label={t('users.group_note')} hint={t('users.group_note_hint')}>
			{#snippet children(f)}
				<Textarea {...f} maxlength={500} rows={3} bind:value={form.values.note} />
			{/snippet}
		</Field>
		<FormStatus status={form.status} />
	</form>
	{#snippet footer()}
		<Button onclick={onclose}>{t('actions.cancel')}</Button>
		<Button variant="primary" type="submit" form={form.id} busy={form.busy}
			>{group ? t('actions.save') : t('users.create_user_group')}</Button
		>
	{/snippet}
</Dialog>

<style>
	.intro {
		margin: 0 0 var(--space-4);
		max-width: var(--measure);
		color: var(--text-muted);
	}
</style>
