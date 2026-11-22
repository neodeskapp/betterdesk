<!--
  Settings tab: every value stored with the organization (the shared
  address book's switch, policies saved before the Policies page had their
  own place) and deleting the organization, with org.delete. Connection
  rules for devices are set on the Policies page and in the Client
  features switches, so the note links there.
-->
<script lang="ts">
	import { untrack } from 'svelte';
	import { deleteOrg, listOrgSettings, type OrgSetting, type Organization } from '#lib/api/orgs.ts';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import { link } from '#lib/nav.ts';
	import { go } from '#lib/people/go.ts';
	import { can } from '#lib/state/session.svelte.ts';
	import DangerZone from '#lib/ui/DangerZone.svelte';
	import States from '#lib/ui/States.svelte';
	import Table from '#lib/ui/Table.svelte';
	import { confirm } from '#lib/ui/dialogs.svelte.ts';
	import type { Column } from '#lib/ui/table.ts';
	import { toast } from '#lib/ui/toasts.svelte.ts';
	import { ICONS } from '#lib/util/display.ts';

	let { org, canPolicies }: { org: Organization; canPolicies: boolean } = $props();

	const canDelete = can('org.delete');
	const uid = $props.id();

	let rows = $state.raw<OrgSetting[] | null>(null);
	let loadError = $state('');
	let deleting = $state(false);

	async function load() {
		try {
			rows = await listOrgSettings(org.id);
			loadError = '';
		} catch (err) {
			loadError = err instanceof Error ? err.message : t('organizations.loading_failed');
		}
	}

	$effect(() => {
		untrack(() => void load());
	});

	async function removeOrg() {
		const ok = await confirm({
			title: t('organizations.delete_org'),
			message: t('organizations.delete_confirm_named', { name: org.name || org.id }),
			confirmLabel: t('organizations.delete_org'),
			confirmIcon: ICONS.delete,
			danger: true
		});
		if (!ok) return;
		deleting = true;
		try {
			await deleteOrg(org.id);
			toast.success(t('organizations.org_deleted'));
			go(link('/organizations'));
		} catch (err) {
			toast.error(`${t('organizations.delete_failed')} ${err instanceof Error ? err.message : ''}`.trim());
		} finally {
			deleting = false;
		}
	}

	const rawColumns = $derived<Column<OrgSetting>[]>([
		{ id: 'key', label: t('organizations.key'), cell: keyCell, sortValue: (r) => r.key, class: 'col-key' },
		{ id: 'value', label: t('organizations.value'), cell: valueCell, class: 'col-value' }
	]);
</script>

{#snippet keyCell(r: OrgSetting)}<span class="mono">{r.key}</span>{/snippet}
{#snippet valueCell(r: OrgSetting)}<span class="mono">{r.value}</span>{/snippet}

<section class="section settings" aria-labelledby="{uid}-raw">
	<h2 id="{uid}-raw">{t('organizations.raw_settings')}</h2>
	<p class="section-note">
		{t('organizations.raw_settings_note')}
		{t('organizations.connection_rules_note')}
		<a href={link(canPolicies ? `/policies/${encodeURIComponent(org.id)}` : '/policies')}
			>{t('organizations.policies_link')}</a
		>
	</p>
	{#if rows && rows.length}
		<div class="raw">
			<Table {rows} columns={rawColumns} key={(r) => r.key} label={t('organizations.raw_settings')} />
		</div>
	{:else if rows}
		<States message={t('organizations.no_settings')} />
	{:else if loadError}
		<States kind="error" message={loadError} onretry={() => void load()} />
	{:else}
		<States kind="loading" />
	{/if}
</section>

{#if canDelete}
	<DangerZone
		title={t('common.delete')}
		rows={[
			{
				title: t('organizations.delete_org'),
				consequence: t('organizations.delete_org_desc'),
				action: t('organizations.delete_org_button'),
				onclick: () => void removeOrg(),
				busy: deleting
			}
		]}
	/>
{/if}

<style>
	.settings {
		max-width: var(--measure);
	}
	.raw :global(td.col-value) {
		overflow-wrap: anywhere;
	}
	.raw :global(td.col-key) {
		white-space: nowrap;
	}
</style>
