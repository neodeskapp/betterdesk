<!--
  Make an API token: a name, read only or read and write, the permissions
  (only the ones the person holds can be picked), the expiry and the
  addresses it may be used from. The token is shown once after this
  (ShownOnceDialog).
-->
<script lang="ts">
	import { untrack } from 'svelte';
	import { createApiToken, tokenPermissions } from '#lib/api/apiTokens.ts';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import {
		EXPIRY_DAYS,
		badAllowEntries,
		expiryDate,
		latestExpiry,
		parseAllowList
	} from '#lib/people/apiTokens.ts';
	import { groupTitle, permLabel, permissionGroups } from '#lib/people/permissions.ts';
	import Button from '#lib/ui/Button.svelte';
	import Checkbox from '#lib/ui/Checkbox.svelte';
	import Dialog from '#lib/ui/Dialog.svelte';
	import Field from '#lib/ui/Field.svelte';
	import FormStatus from '#lib/ui/FormStatus.svelte';
	import Input from '#lib/ui/Input.svelte';
	import Radio from '#lib/ui/Radio.svelte';
	import Select from '#lib/ui/Select.svelte';
	import States from '#lib/ui/States.svelte';
	import Textarea from '#lib/ui/Textarea.svelte';
	import { FormError, createForm } from '#lib/ui/form.svelte.ts';

	let { onclose, oncreated }: { onclose: () => void; oncreated: (token: string, name: string) => void } =
		$props();

	let held = $state.raw<string[]>([]);
	let loadError = $state<string | null>(null);
	let loaded = $state(false);

	async function loadPermissions(): Promise<void> {
		try {
			held = (await tokenPermissions()).held;
		} catch (err) {
			loadError = `${t('api_tokens.permissions_failed')} ${err instanceof Error ? err.message : ''}`.trim();
		} finally {
			loaded = true;
		}
	}

	$effect(() => untrack(() => void loadPermissions()));

	const groups = $derived(permissionGroups(held));
	let picked = $state<Record<string, boolean>>({});
	const pickedList = $derived(held.filter((p) => picked[p]));

	const form = createForm({
		initial: {
			name: '',
			access: 'read' as 'read' | 'write',
			expiry: '30' as string,
			date: '',
			allow: ''
		},
		validate: (v) => {
			const bad = badAllowEntries(parseAllowList(v.allow));
			return {
				name: v.name.trim() ? undefined : t('common.name_required'),
				date: v.expiry === 'date' && !v.date ? t('api_tokens.date_required') : undefined,
				allow: bad.length ? t('api_tokens.allow_bad', { list: bad.join(', ') }) : undefined
			};
		},
		submit: async (v) => {
			if (!pickedList.length) throw new FormError(t('api_tokens.pick_one'));
			try {
				const made = await createApiToken({
					name: v.name.trim(),
					permissions: pickedList,
					read_only: v.access === 'read',
					expires_at: v.expiry === 'date' ? v.date : expiryDate(Number(v.expiry)),
					allow_list: parseAllowList(v.allow)
				});
				oncreated(made.token, made.info.name);
			} catch (err) {
				throw new FormError(
					`${t('api_tokens.create_failed')} ${err instanceof Error ? err.message : ''}`.trim()
				);
			}
		}
	});

	const expiryOptions = $derived([
		...EXPIRY_DAYS.map((d) => ({ value: String(d), label: t('api_tokens.expires_days', { count: d }) })),
		{ value: 'date', label: t('api_tokens.expires_date') }
	]);
	const today = new Date().toISOString().slice(0, 10);

	function pickAll(on: boolean) {
		const next: Record<string, boolean> = {};
		for (const p of held) next[p] = on;
		picked = next;
	}
</script>

<Dialog title={t('api_tokens.create')} size="large" class="api-token-dialog" {onclose}>
	{#if !loaded}
		<States kind="loading" message={t('api_tokens.loading')} />
	{:else if loadError}
		<States kind="error" message={loadError} />
	{:else}
		<form id={form.id} onsubmit={form.onsubmit} novalidate>
			<Field label={t('api_tokens.name')} hint={t('api_tokens.name_hint')} error={form.errors.name}>
				{#snippet children(f)}
					<Input {...f} maxlength={80} required autocomplete="off" bind:value={form.values.name} />
				{/snippet}
			</Field>

			<fieldset class="group">
				<legend>{t('api_tokens.access')}</legend>
				<!-- The fieldset is the group and its legend the name; native radios with one name move with the arrows -->
				<div>
					<Radio
						name="access"
						value="read"
						label={t('api_tokens.read_only')}
						hint={t('api_tokens.read_only_hint')}
						bind:group={form.values.access}
					/>
					<Radio
						name="access"
						value="write"
						label={t('api_tokens.read_write')}
						hint={t('api_tokens.read_write_hint')}
						bind:group={form.values.access}
					/>
				</div>
			</fieldset>

			<fieldset class="group perms">
				<legend>{t('api_tokens.permissions')}</legend>
				<p class="form-hint">{t('api_tokens.permissions_hint')}</p>
				<div class="perm-actions">
					<Button size="sm" onclick={() => pickAll(true)}>{t('api_tokens.pick_all')}</Button>
					<Button size="sm" onclick={() => pickAll(false)}>{t('api_tokens.pick_none')}</Button>
				</div>
				{#each groups as group (group.id)}
					<div class="perm-group">
						<h3>{groupTitle(group.id, t)}</h3>
						{#each group.permissions as perm (perm)}
							<Checkbox
								inline
								label={permLabel(perm, t)}
								checked={!!picked[perm]}
								onchange={(e) => (picked = { ...picked, [perm]: e.currentTarget.checked })}
							/>
						{/each}
					</div>
				{/each}
			</fieldset>

			<Field label={t('api_tokens.expires')} hint={t('api_tokens.expires_hint')}>
				{#snippet children(f)}
					<Select {...f} width="medium" options={expiryOptions} bind:value={form.values.expiry} />
				{/snippet}
			</Field>
			{#if form.values.expiry === 'date'}
				<Field label={t('api_tokens.expires_on')} error={form.errors.date} width="medium">
					{#snippet children(f)}
						<Input {...f} type="date" min={today} max={latestExpiry()} bind:value={form.values.date} />
					{/snippet}
				</Field>
			{/if}

			<Field
				label={t('api_tokens.allow_list')}
				hint={t('api_tokens.allow_list_hint')}
				error={form.errors.allow}
			>
				{#snippet children(f)}
					<Textarea {...f} rows={3} spellcheck={false} autocomplete="off" bind:value={form.values.allow} />
				{/snippet}
			</Field>
			<FormStatus status={form.status} />
		</form>
	{/if}
	{#snippet footer()}
		<Button onclick={onclose}>{t('common.cancel')}</Button>
		<Button
			variant="primary"
			type="submit"
			form={form.id}
			busy={form.busy}
			disabled={!loaded || !!loadError}
			class="api-token-save">{t('api_tokens.create_button')}</Button
		>
	{/snippet}
</Dialog>

<style>
	.group {
		border: 0;
		margin: 0 0 var(--space-4);
		padding: 0;
	}
	legend {
		font-weight: 600;
		margin-bottom: var(--space-2);
	}
	.perms .form-hint {
		margin: 0 0 var(--space-2);
		max-width: var(--measure);
	}
	.perm-actions {
		display: flex;
		gap: var(--space-2);
		margin-bottom: var(--space-3);
	}
	.perm-group {
		margin-bottom: var(--space-3);
	}
	.perm-group h3 {
		font-size: var(--text-sm);
		margin: 0 0 var(--space-1);
		color: var(--text-muted);
	}
</style>
