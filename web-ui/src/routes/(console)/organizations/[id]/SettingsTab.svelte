<!--
  Settings tab: every value stored with the organization, in words, and
  deleting the organization, with org.delete. The values are changed where
  they are set (PATTERN.md rule 2): the policy groups on this
  organization's Policies page, the shared address book on the Address
  book tab; this tab says what applies now and where it comes from. Each
  policy group shows whether it is set for this organization, with each
  value it holds named in the Policies page's words ("Session approval:
  Attended"), or that no rule applies. Values the console has no words
  for stay listed with their key. Delete organization is in a danger zone at the end (rule 8).
-->
<script lang="ts">
	import { untrack } from 'svelte';
	import { deleteOrg, listOrgSettings, type OrgSetting, type Organization } from '#lib/api/orgs.ts';
	import { t, tf } from '#lib/i18n/i18n.svelte.ts';
	import { link } from '#lib/nav.ts';
	import { go } from '#lib/people/go.ts';
	import { orgSettingsView } from '#lib/people/org-settings.ts';
	import { SECTION_TITLE } from '#lib/rules/policies.ts';
	import { fieldLabel, valueWords } from '#lib/rules/policy-words.ts';
	import { can } from '#lib/state/session.svelte.ts';
	import DangerZone from '#lib/ui/DangerZone.svelte';
	import States from '#lib/ui/States.svelte';
	import Table from '#lib/ui/Table.svelte';
	import { confirm } from '#lib/ui/dialogs.svelte.ts';
	import SettingRow from '#lib/ui/settings/SettingRow.svelte';
	import SettingsGroup from '#lib/ui/settings/SettingsGroup.svelte';
	import type { Column } from '#lib/ui/table.ts';
	import { toast } from '#lib/ui/toasts.svelte.ts';
	import { ICONS } from '#lib/util/display.ts';

	let { org, canPolicies }: { org: Organization; canPolicies: boolean } = $props();

	const canDelete = can('org.delete');

	let rows = $state.raw<OrgSetting[] | null>(null);
	let loadError = $state('');
	let deleting = $state(false);

	const view = $derived(rows ? orgSettingsView(rows) : null);
	const policiesHref = $derived(link(`/policies/${encodeURIComponent(org.id)}`));

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

	/** A group with rules is marked set (said to screen readers); one without says so in its value column */
	const policySource = (set: boolean) =>
		set
			? { kind: 'here' as const, text: tf('organizations.policy_set', 'Set for this organization.') }
			: null;

	const rawColumns = $derived<Column<OrgSetting>[]>([
		{ id: 'key', label: t('organizations.key'), cell: keyCell, sortValue: (r) => r.key, class: 'col-key' },
		{ id: 'value', label: t('organizations.value'), cell: valueCell, class: 'col-value' }
	]);
</script>

{#snippet keyCell(r: OrgSetting)}<span class="mono">{r.key}</span>{/snippet}
{#snippet valueCell(r: OrgSetting)}<span class="mono">{r.value}</span>{/snippet}

{#snippet storedValues(values: { field: string; known: boolean; value: unknown }[])}
	<ul class="stored">
		{#each values as v (v.field)}
			<li>
				<span class={['stored-name', !v.known && 'mono']}>{v.known ? fieldLabel(v.field) : v.field}:</span>
				{v.known
					? valueWords(v.field, v.value)
					: typeof v.value === 'string'
						? v.value
						: JSON.stringify(v.value)}
			</li>
		{/each}
	</ul>
{/snippet}

<div class="org-settings">
	<p class="tab-note">
		{tf(
			'organizations.settings_tab_note',
			'What applies to this organization now. Each value is changed where it is set: policies on the Policies page, the shared address book on the Address book tab.'
		)}
	</p>

	{#if !view && loadError}
		<States kind="error" message={loadError} onretry={() => void load()} />
	{:else if !view}
		<States kind="loading" />
	{:else}
		<SettingsGroup
			id="org-settings-policies"
			title={t('nav.policies')}
			description={tf(
				'organizations.policies_group_desc',
				'How remote sessions work on this organization’s devices.'
			)}
			consequence={tf(
				'organizations.policies_group_effect',
				'Devices get a change the next time they ask the server for their policy.'
			)}
		>
			{#snippet actions()}
				{#if canPolicies}
					<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -->
					<a class="group-link" href={policiesHref}
						>{tf('organizations.open_org_policies', 'Open this organization’s policies')}</a
					>
				{/if}
			{/snippet}
			{#each view.policies as p (p.key)}
				<SettingRow
					id={`org-setting-${p.key}`}
					label={t(SECTION_TITLE[p.section])}
					help={t(`policies.${p.section}_desc`)}
					keyName={p.key}
					source={policySource(p.set)}
				>
					{#snippet control(f)}
						<div id={f.id} aria-describedby={f.describedby} class={['state', p.set && 'is-set']}>
							{#if p.values.length}{@render storedValues(p.values)}{:else if !p.set}{tf(
									'policies.not_set_option',
									'Not set (each device decides)'
								)}{/if}
						</div>
					{/snippet}
				</SettingRow>
			{/each}
		</SettingsGroup>

		<SettingsGroup
			id="org-settings-address-book"
			title={t('organizations.address_book')}
			description={tf(
				'organizations.book_group_desc',
				'Whether members see this organization’s shared contacts in their address book.'
			)}
		>
			<SettingRow
				id="org-setting-shared_address_book_enabled"
				label={t('organizations.address_book_enabled')}
				keyName="shared_address_book_enabled"
				source={view.addressBook
					? {
							kind: 'here',
							text: tf('organizations.book_set_here', 'Set for this organization.'),
							href: '#address-book',
							linkLabel: tf('organizations.book_change', 'Change on the Address book tab')
						}
					: {
							kind: 'default',
							text: tf('organizations.book_default', 'Built-in default: on until it is turned off.'),
							href: '#address-book',
							linkLabel: tf('organizations.book_change', 'Change on the Address book tab')
						}}
			>
				{#snippet control(f)}
					<output id={f.id} aria-describedby={f.describedby} class="state is-set"
						>{(view.addressBook?.on ?? true)
							? tf('organizations.state_on', 'On.')
							: tf('organizations.state_off', 'Off.')}</output
					>
				{/snippet}
			</SettingRow>
		</SettingsGroup>

		{#if view.other.length}
			<SettingsGroup
				id="org-settings-other"
				title={tf('organizations.other_values', 'Other stored values')}
				description={tf(
					'organizations.other_values_desc',
					'Values saved for this organization that have no place of their own in the console.'
				)}
			>
				<div class="raw">
					<Table
						rows={view.other}
						columns={rawColumns}
						key={(r) => r.key}
						label={tf('organizations.other_values', 'Other stored values')}
					/>
				</div>
			</SettingsGroup>
		{/if}
	{/if}

	{#if canDelete}
		<DangerZone
			title={tf('organizations.danger_zone', 'Danger zone')}
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
</div>

<style>
	.org-settings {
		display: flex;
		flex-direction: column;
		gap: var(--space-6);
		max-width: var(--measure);
	}
	.tab-note {
		margin: 0;
		color: var(--text-muted);
	}
	.state {
		display: block;
	}
	.state:not(.is-set) {
		color: var(--text-muted);
		font-weight: 400;
	}
	.group-link {
		display: inline-flex;
		align-items: center;
		min-height: 28px;
		font-size: var(--text-sm);
	}
	@media (pointer: coarse) {
		.group-link {
			min-height: 44px;
		}
	}
	.stored {
		margin: 0;
		padding: 0;
		list-style: none;
		display: flex;
		flex-direction: column;
		gap: var(--space-1);
		overflow-wrap: anywhere;
	}
	.stored-name {
		color: var(--text-muted);
	}
	/* The group's rows already reach its edges; the table's own pull to the edges would go past them */
	.raw {
		margin-inline: var(--row-pad-x);
	}
	.raw :global(td.col-value) {
		overflow-wrap: anywhere;
	}
	.raw :global(td.col-key) {
		white-space: nowrap;
	}
</style>
