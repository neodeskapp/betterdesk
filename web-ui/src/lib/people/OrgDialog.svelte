<!--
  New or edit an organization: name, slug and logo address (three fields,
  a centred dialog). For a new one the slug follows the name until someone
  types in the slug field. A record form with its own Save (PATTERN.md
  rules 5, 7 and 11): each field says what it is for, a changed slug says
  what that changes, and the toast after saving an edit names what was
  saved.
-->
<script lang="ts">
	import { untrack } from 'svelte';
	import { createOrg, updateOrg, type Organization } from '#lib/api/orgs.ts';
	import { t, tf } from '#lib/i18n/i18n.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import Dialog from '#lib/ui/Dialog.svelte';
	import Field from '#lib/ui/Field.svelte';
	import FormStatus from '#lib/ui/FormStatus.svelte';
	import Input from '#lib/ui/Input.svelte';
	import { savedMessage, type Change } from '#lib/ui/settings/draft.svelte.ts';
	import { createForm } from '#lib/ui/form.svelte.ts';
	import { toast } from '#lib/ui/toasts.svelte.ts';
	import { logoUrlOk, slugify } from './orgs.ts';

	interface Props {
		org: Organization | null;
		onclose: () => void;
		onsaved: () => void;
	}

	let { org, onclose, onsaved }: Props = $props();

	let slugTouched = untrack(() => !!org);

	const form = createForm({
		initial: untrack(() => ({ name: org?.name ?? '', slug: org?.slug ?? '', logo: org?.logo_url ?? '' })),
		validate: (v) => {
			const missing = t('organizations.name_slug_required');
			return {
				name: v.name.trim() ? undefined : missing,
				slug: v.slug.trim() ? undefined : missing,
				logo: logoUrlOk(v.logo.trim()) ? undefined : t('organizations.logo_url_invalid')
			};
		},
		submit: async (v) => {
			const body = { name: v.name.trim(), slug: v.slug.trim().toLowerCase(), logo_url: v.logo.trim() };
			if (org) {
				const saved = edits(org, body);
				await updateOrg(org.id, body);
				toast.success(saved.length ? savedMessage(saved) : t('organizations.org_updated'));
			} else {
				await createOrg(body);
				toast.success(t('organizations.org_created'));
			}
			onsaved();
		}
	});

	/** What an edit changes, in the words of the fields, for the toast */
	function edits(before: Organization, after: { name: string; slug: string; logo_url: string }): Change[] {
		const fields: [string, string, string][] = [
			[t('organizations.name'), before.name ?? '', after.name],
			[t('organizations.slug'), before.slug ?? '', after.slug],
			[t('organizations.logo_url'), before.logo_url ?? '', after.logo_url]
		];
		return fields
			.filter(([, from, to]) => from !== to)
			.map(([label, from, to]) => ({
				field: label,
				label,
				from,
				to: to || tf('settings_ui.empty', 'empty'),
				fromValue: from,
				toValue: to
			}));
	}

	const slugChanged = $derived(!!org && form.values.slug.trim().toLowerCase() !== (org.slug ?? ''));

	/** A new organization's slug follows the name until the slug is typed in */
	function onName(e: Event) {
		if (!slugTouched) form.values.slug = slugify((e.currentTarget as HTMLInputElement).value);
	}
</script>

<Dialog
	title={org ? t('organizations.edit') : t('organizations.create')}
	beforeclose={() => !form.busy}
	{onclose}
>
	<form id={form.id} onsubmit={form.onsubmit} novalidate>
		<Field
			label={t('organizations.name')}
			hint={tf('organizations.name_hint', 'Shown on this organization’s page, in lists and to its members.')}
			error={form.errors.name}
		>
			{#snippet children(f)}
				<Input
					{...f}
					maxlength={120}
					autocomplete="off"
					required
					bind:value={form.values.name}
					oninput={onName}
				/>
			{/snippet}
		</Field>
		<Field label={t('organizations.slug')} hint={t('organizations.slug_hint')} error={form.errors.slug}>
			{#snippet children(f)}
				<Input
					{...f}
					width="medium"
					maxlength={64}
					autocomplete="off"
					spellcheck="false"
					required
					bind:value={form.values.slug}
					oninput={() => (slugTouched = true)}
				/>
			{/snippet}
		</Field>
		<p class="slug-effect" aria-live="polite">
			{#if slugChanged}{tf(
					'organizations.slug_changed',
					'Sign-ins that name the organization by its short name need the new one after you save.'
				)}{/if}
		</p>
		<Field
			label={t('organizations.logo_url')}
			hint={t('organizations.logo_url_hint')}
			error={form.errors.logo}
		>
			{#snippet children(f)}
				<Input
					{...f}
					type="url"
					width="host"
					autocomplete="off"
					spellcheck="false"
					bind:value={form.values.logo}
				/>
			{/snippet}
		</Field>
		<FormStatus status={form.status} />
	</form>
	{#snippet footer()}
		<Button onclick={onclose}>{t('actions.cancel')}</Button>
		<Button variant="primary" type="submit" form={form.id} busy={form.busy}
			>{org ? t('actions.save') : t('organizations.create')}</Button
		>
	{/snippet}
</Dialog>

<style>
	.slug-effect {
		margin: calc(-1 * var(--space-2)) 0 var(--space-3);
		color: var(--warning);
		font-size: var(--text-sm);
	}
	.slug-effect:empty {
		display: none;
	}
</style>
