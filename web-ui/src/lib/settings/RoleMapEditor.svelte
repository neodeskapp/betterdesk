<!--
  A group to role map with one row per entry (PATTERN.md rule 4): the
  group as the directory or provider names it, the role it gets from a
  list of the server's roles, Remove, and Add at the end. A wrong row says
  what is wrong under itself (lib/settings/signin.ts roleMapProblems); an
  empty row is left out when saving.

    <RoleMapEditor id="ldap-group-role-map" rows={v} roles={options} onchange={(rows) => draft.set(k, rows)}
        groupLabel={(n) => t('...group n')} roleLabel={(n) => t('...role n')} addLabel={t('...')} problems={p} />
-->
<script lang="ts">
	import { tick } from 'svelte';
	import { tf } from '#lib/i18n/i18n.svelte.ts';
	import { ICONS } from '#lib/util/display.ts';
	import Button from '#lib/ui/Button.svelte';
	import IconButton from '#lib/ui/IconButton.svelte';
	import Input from '#lib/ui/Input.svelte';
	import Select from '#lib/ui/Select.svelte';
	import type { PickOption } from '#lib/ui/settings/RecordPicker.svelte';
	import type { RoleMapRow } from './signin.ts';

	interface Props {
		id: string;
		rows: readonly RoleMapRow[];
		roles: readonly PickOption[];
		onchange: (rows: RoleMapRow[]) => void;
		/** The accessible names of row n (1-based) */
		groupLabel: (n: number) => string;
		roleLabel: (n: number) => string;
		addLabel: string;
		/** What is wrong with each row, '' for a fine one */
		problems?: readonly string[];
		describedby?: string;
		disabled?: boolean;
	}

	let {
		id,
		rows,
		roles,
		onchange,
		groupLabel,
		roleLabel,
		addLabel,
		problems = [],
		describedby,
		disabled = false
	}: Props = $props();

	// A stored role the list doesn't have stays choosable, so nothing changes unseen
	const options = $derived([
		{ value: '', label: tf('settings.signin.pick_role', 'Pick a role') },
		...roles.map((r) => ({ value: r.value, label: r.label })),
		...rows
			.map((r) => r.role.trim())
			.filter((r, i, all) => r && all.indexOf(r) === i && !roles.some((o) => o.value === r))
			.map((r) => ({
				value: r,
				label: tf('settings_ui.gone_record', '{name} (no longer exists)', { name: r })
			}))
	]);

	function update(i: number, patch: Partial<RoleMapRow>) {
		onchange(rows.map((r, n) => (n === i ? { ...r, ...patch } : { ...r })));
	}

	async function add() {
		onchange([...rows.map((r) => ({ ...r })), { group: '', role: '' }]);
		await tick();
		document.getElementById(`${id}-${rows.length}-group`)?.focus();
	}

	async function remove(i: number) {
		onchange(rows.filter((_, n) => n !== i).map((r) => ({ ...r })));
		await tick();
		const next =
			document.getElementById(`${id}-${Math.min(i, rows.length - 1)}-group`) ??
			document.getElementById(`${id}-add`);
		next?.focus();
	}
</script>

<div class="role-map" {id} role="group" aria-describedby={describedby}>
	{#if rows.length}
		<ol class="map-rows">
			{#each rows as row, i (i)}
				{@const problem = problems[i] ?? ''}
				<li class="map-row">
					<div class="map-fields">
						<Input
							id={`${id}-${i}-group`}
							class="mono"
							value={row.group}
							{disabled}
							spellcheck={false}
							autocomplete="off"
							aria-label={groupLabel(i + 1)}
							invalid={!!problem}
							describedby={problem ? `${id}-${i}-error` : undefined}
							oninput={(e) => update(i, { group: (e.currentTarget as HTMLInputElement).value })}
						/>
						<Select
							id={`${id}-${i}-role`}
							{options}
							value={row.role}
							{disabled}
							aria-label={roleLabel(i + 1)}
							invalid={!!problem}
							describedby={problem ? `${id}-${i}-error` : undefined}
							onchange={(e) => update(i, { role: (e.currentTarget as HTMLSelectElement).value })}
						/>
						<IconButton
							icon={ICONS.delete}
							label={`${tf('settings_ui.remove', 'Remove')}: ${groupLabel(i + 1)}`}
							{disabled}
							onclick={() => void remove(i)}
						/>
					</div>
					{#if problem}<p class="map-error" id={`${id}-${i}-error`}>{problem}</p>{/if}
				</li>
			{/each}
		</ol>
	{:else}
		<p class="map-empty">{tf('settings_ui.list_empty', 'No entries.')}</p>
	{/if}
	<div>
		<Button id={`${id}-add`} icon="add" size="sm" {disabled} onclick={() => void add()}>{addLabel}</Button>
	</div>
</div>

<style>
	.role-map {
		display: flex;
		flex-direction: column;
		gap: var(--space-2);
		min-width: 0;
	}
	.map-rows {
		display: flex;
		flex-direction: column;
		gap: var(--space-2);
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.map-row {
		display: flex;
		flex-direction: column;
		gap: var(--space-1);
	}
	.map-fields {
		display: grid;
		grid-template-columns: minmax(0, 1fr) minmax(0, 12rem) auto;
		gap: var(--space-2);
		align-items: center;
	}
	/* On a phone the group takes the row; the role and Remove share the next */
	@media (max-width: 480px) {
		.map-fields {
			grid-template-columns: minmax(0, 1fr) auto;
		}
		.map-fields > :global(:first-child) {
			grid-column: 1 / -1;
		}
	}
	.map-error {
		margin: 0;
		color: var(--danger);
		font-size: var(--text-sm);
	}
	.map-empty {
		margin: 0;
		color: var(--text-muted);
	}
	@media (pointer: coarse) {
		.map-row :global(.icon-btn),
		.role-map :global(.btn) {
			min-width: 44px;
			min-height: 44px;
		}
	}
</style>
