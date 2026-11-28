<!--
  Create one token, or edit one (name, uses allowed, folder, policies,
  note; the expiry can't change afterwards). A new token is for one
  computer or for many (a rollout token, A278): many needs the number of
  computers (100 to start with) and an expiry (30 days to start with,
  never "No expiration"); the kind can't change afterwards. kind picks the
  choice a new token starts on (the Receiver app tab starts on many). A quick create with plain
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
	import {
		EXPIRY_CHOICES,
		EXPIRY_LABELS,
		ROLLOUT_DEFAULT_COMPUTERS,
		ROLLOUT_EXPIRY_CHOICES,
		ROLLOUT_EXPIRY_LABELS,
		ROLLOUT_MAX_COMPUTERS,
		isRollout,
		rolloutLimitOk,
		usesOk,
		usesValue
	} from '#lib/clients/tokens.ts';
	import { formatNumber } from '#lib/i18n/format.ts';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import ChoiceList from '#lib/ui/ChoiceList.svelte';
	import Dialog from '#lib/ui/Dialog.svelte';
	import Field from '#lib/ui/Field.svelte';
	import FormStatus from '#lib/ui/FormStatus.svelte';
	import Input from '#lib/ui/Input.svelte';
	import Select from '#lib/ui/Select.svelte';
	import { FormError, createForm } from '#lib/ui/form.svelte.ts';
	import { folderOptions, keepKnown, loadPlaces, orgOptions, type Places } from './placement.ts';

	let {
		token,
		kind = 'device',
		onclose,
		onsaved
	}: {
		token: DeviceToken | null;
		/** The choice a new token starts on */
		kind?: 'device' | 'rollout';
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
			kind: (token ? (isRollout(token) ? 'rollout' : 'device') : kind) as 'device' | 'rollout',
			name: token?.name ?? '',
			max_uses: String(token && !isRollout(token) ? token.max_uses : 1),
			computers: String(token && isRollout(token) ? token.max_uses : ROLLOUT_DEFAULT_COMPUTERS),
			expires_in: 0 as number,
			rollout_expires_in: ROLLOUT_EXPIRY_CHOICES[0] as number,
			note: token?.note ?? '',
			folder: String(token?.folder_id ?? 0),
			org: token?.org_id ?? ''
		})),
		validate: (v) => ({
			name: v.name.trim() ? undefined : t('common.name_required'),
			max_uses: v.kind === 'rollout' || usesOk(v.max_uses) ? undefined : t('tokens.max_uses_invalid'),
			computers:
				v.kind !== 'rollout' || rolloutLimitOk(v.computers)
					? undefined
					: t('tokens.rollout_limit_invalid', { max: formatNumber(ROLLOUT_MAX_COMPUTERS) })
		}),
		submit: async (v) => {
			const many = v.kind === 'rollout';
			const body: TokenInput = {
				name: v.name.trim(),
				max_uses: usesValue(many ? v.computers : v.max_uses),
				note: v.note.trim()
			};
			// Only what a picker showed: a list the account can't read leaves the token's as it is
			if (places.folders.length) body.folder_id = Number(keepKnown(v.folder, folderChoices, '0')) || 0;
			if (places.orgs.length) body.org_id = keepKnown(v.org, orgChoices, '');
			try {
				if (token) {
					await updateToken(token.id, body);
					onsaved(null);
				} else if (many) {
					onsaved(await createToken({ ...body, kind: 'rollout', expires_in: Number(v.rollout_expires_in) }));
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
	const rolloutExpiryOptions = $derived(
		ROLLOUT_EXPIRY_CHOICES.map((s) => ({ value: s as number, label: t(`tokens.${ROLLOUT_EXPIRY_LABELS[s]}`) }))
	);
	const kindChoices = $derived([
		{ value: 'device' as const, title: t('tokens.kind_one'), description: t('tokens.kind_one_desc') },
		{ value: 'rollout' as const, title: t('tokens.kind_many'), description: t('tokens.kind_many_desc') }
	]);
	const many = $derived(form.values.kind === 'rollout');
</script>

<Dialog title={editing ? t('tokens.edit') : t('tokens.create')} class="token-dialog" {onclose}>
	<form id={form.id} onsubmit={form.onsubmit} novalidate>
		{#if !editing}
			<fieldset class="token-kind">
				<legend id="token-kind-label">{t('tokens.kind')}</legend>
				<ChoiceList choices={kindChoices} labelledby="token-kind-label" name="token_kind" bind:value={form.values.kind} />
			</fieldset>
		{/if}
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
		{#if many}
			<Field
				label={t('tokens.rollout_limit')}
				hint={editing ? t('tokens.rollout_limit_edit_hint') : t('tokens.rollout_limit_hint')}
				error={form.errors.computers}
				width="short"
			>
				{#snippet children(f)}
					<Input
						{...f}
						type="number"
						min={1}
						max={ROLLOUT_MAX_COMPUTERS}
						step={1}
						inputmode="numeric"
						required
						bind:value={form.values.computers}
					/>
				{/snippet}
			</Field>
			{#if !editing}
				<Field label={t('tokens.expires_in')} hint={t('tokens.rollout_expires_hint')}>
					{#snippet children(f)}
						<Select {...f} width="medium" options={rolloutExpiryOptions} bind:value={form.values.rollout_expires_in} />
					{/snippet}
				</Field>
			{/if}
		{:else}
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

<style>
	.token-kind {
		margin: 0 0 var(--space-4);
		padding: 0;
		border: 0;
		min-width: 0;
	}
	.token-kind legend {
		padding: 0;
		margin-bottom: var(--space-2);
		font-weight: 600;
	}
</style>
