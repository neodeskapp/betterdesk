<!--
  Create one token, or edit one (name, uses allowed, folder, policies,
  note; the expiry can't change afterwards). A quick create with plain
  fields: a centred dialog. Creating shows the new token right after
  (CreatedDialog). Folder and policies are optional: a computer that joins
  with the token goes into the folder and gets the organization's
  policies; a picker whose list the account can't read stays away.
-->
<script lang="ts">
	import { onMount, untrack } from 'svelte';
	import {
		createToken,
		updateToken,
		type DeviceToken,
		type NewToken,
		type TokenInput
	} from '#lib/api/tokens.ts';
	import { EXPIRY_CHOICES, EXPIRY_LABELS, usesOk, usesValue } from '#lib/clients/tokens.ts';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import Dialog from '#lib/ui/Dialog.svelte';
	import Field from '#lib/ui/Field.svelte';
	import FormStatus from '#lib/ui/FormStatus.svelte';
	import Input from '#lib/ui/Input.svelte';
	import Select from '#lib/ui/Select.svelte';
	import { FormError, createForm } from '#lib/ui/form.svelte.ts';
	import { folderOptions, keepKnown, loadPlaces, orgOptions, type Places } from './placement.ts';

	let {
		token,
		onclose,
		onsaved
	}: {
		token: DeviceToken | null;
		onclose: () => void;
		/** The new token (create), or null (edit, or the server sent no value) */
		onsaved: (created: NewToken | null) => void;
	} = $props();

	let places = $state<Places>({ folders: [], orgs: [] });
	onMount(() => {
		const ctl = new AbortController();
		void loadPlaces(ctl.signal).then((p) => (places = p));
		return () => ctl.abort();
	});
	const folderChoices = $derived(folderOptions(places.folders, t('onboard.invite.no_folder')));
	const orgChoices = $derived(orgOptions(places.orgs, t('tokens.no_policies')));

	const editing = $derived(!!token);
	/** An unlimited token (0) stays unlimited: the field keeps 0 and says what it means */
	const unlimited = $derived(!!token && !token.max_uses);

	const form = createForm({
		initial: untrack(() => ({
			name: token?.name ?? '',
			max_uses: String(token ? token.max_uses : 1),
			expires_in: 0 as number,
			note: token?.note ?? '',
			folder: String(token?.folder_id ?? 0),
			org: token?.org_id ?? ''
		})),
		validate: (v) => ({
			name: v.name.trim() ? undefined : t('common.name_required'),
			max_uses: usesOk(v.max_uses) ? undefined : t('tokens.max_uses_invalid')
		}),
		submit: async (v) => {
			const body: TokenInput = { name: v.name.trim(), max_uses: usesValue(v.max_uses), note: v.note.trim() };
			// Only what a picker showed: a list the account can't read leaves the token's as it is
			if (places.folders.length) body.folder_id = Number(keepKnown(v.folder, folderChoices, '0')) || 0;
			if (places.orgs.length) body.org_id = keepKnown(v.org, orgChoices, '');
			try {
				if (token) {
					await updateToken(token.id, body);
					onsaved(null);
				} else {
					onsaved(await createToken({ ...body, expires_in: Number(v.expires_in) || 0 }));
				}
			} catch (err) {
				throw new FormError(`${t('tokens.save_failed')} ${err instanceof Error ? err.message : ''}`.trim());
			}
		}
	});

	const expiryOptions = $derived(
		EXPIRY_CHOICES.map((s) => ({ value: s as number, label: t(`tokens.${EXPIRY_LABELS[s]}`) }))
	);
</script>

<Dialog title={editing ? t('tokens.edit') : t('tokens.create')} class="token-dialog" {onclose}>
	<form id={form.id} onsubmit={form.onsubmit} novalidate>
		<Field label={t('tokens.name')} hint={t('tokens.name_hint')} error={form.errors.name}>
			{#snippet children(f)}
				<Input
					{...f}
					maxlength={128}
					required
					placeholder={t('tokens.name_placeholder')}
					autocomplete="off"
					bind:value={form.values.name}
				/>
			{/snippet}
		</Field>
		<Field
			label={t('tokens.max_uses')}
			hint={editing && !unlimited ? t('tokens.max_uses_edit_hint') : t('tokens.max_uses_hint')}
			error={form.errors.max_uses}
			width="short"
		>
			{#snippet children(f)}
				<Input {...f} type="number" min={0} step={1} inputmode="numeric" bind:value={form.values.max_uses} />
			{/snippet}
		</Field>
		{#if !editing}
			<Field label={t('tokens.expires_in')}>
				{#snippet children(f)}
					<Select {...f} width="medium" options={expiryOptions} bind:value={form.values.expires_in} />
				{/snippet}
			</Field>
		{/if}
		{#if places.folders.length}
			<Field label={t('onboard.invite.folder')} hint={t('tokens.folder_hint')}>
				{#snippet children(f)}
					<Select {...f} width="medium" options={folderChoices} bind:value={form.values.folder} />
				{/snippet}
			</Field>
		{/if}
		{#if places.orgs.length}
			<Field label={t('tokens.policies')} hint={t('tokens.policies_hint')}>
				{#snippet children(f)}
					<Select {...f} width="medium" options={orgChoices} bind:value={form.values.org} />
				{/snippet}
			</Field>
		{/if}
		<Field label={t('tokens.note')} hint={t('tokens.note_hint')}>
			{#snippet children(f)}
				<Input {...f} maxlength={512} autocomplete="off" bind:value={form.values.note} />
			{/snippet}
		</Field>
		<FormStatus status={form.status} />
	</form>
	{#snippet footer()}
		<Button onclick={onclose}>{t('common.cancel')}</Button>
		<Button variant="primary" type="submit" form={form.id} busy={form.busy} class="token-save"
			>{editing ? t('tokens.save') : t('tokens.create')}</Button
		>
	{/snippet}
</Dialog>
