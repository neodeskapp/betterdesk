<!--
  A list setting with one row per entry (PATTERN.md rule 4): each row is a
  field with a Remove button, Add puts an empty row at the end and moves
  the focus there, and a wrong entry says what is wrong under its own row.
  Empty rows are dropped from the value. Examples go into the row's help
  (the SettingRow around it), never as a placeholder.

    <ListEditor id="relays" items={v} onchange={(list) => draft.set(k, list)}
        itemLabel={(n) => t('x.relay_n', { n })} check={(s) => isRelay(s) ? '' : t('x.bad_relay')}
        addLabel={t('x.add_relay')} />

  `quick`: existing records to add with one click ("Add relay1.example.com"),
  shown while they are not in the list yet.
-->
<script lang="ts">
	import { tick, untrack } from 'svelte';
	import { tf } from '#lib/i18n/i18n.svelte.ts';
	import { ICONS } from '#lib/util/display.ts';
	import Button from '../Button.svelte';
	import IconButton from '../IconButton.svelte';
	import Input from '../Input.svelte';

	interface Props {
		id: string;
		items: readonly string[];
		onchange: (items: string[]) => void;
		/** The accessible name of row n (1-based): "Relay server 2" */
		itemLabel: (n: number) => string;
		/** What is wrong with an entry; '' when it is fine */
		check?: (entry: string) => string;
		addLabel: string;
		/** Records to add with one click: value and the words on the button */
		quick?: readonly { value: string; label: string }[];
		describedby?: string;
		disabled?: boolean;
		inputmode?: 'text' | 'url' | 'numeric' | 'decimal' | 'email';
	}

	let {
		id,
		items,
		onchange,
		itemLabel,
		check = () => '',
		addLabel,
		quick = [],
		describedby,
		disabled = false,
		inputmode = 'text'
	}: Props = $props();

	/** The rows as typed, with empty ones, so an added row stays while it is empty */
	let rows = $state<string[]>([]);
	let last = '';

	$effect(() => {
		const incoming = JSON.stringify(items);
		if (incoming === last) return;
		last = incoming;
		const typed = untrack(() => rows.map((r) => r.trim()).filter(Boolean));
		if (JSON.stringify(typed) !== incoming) rows = [...items];
	});

	function emit() {
		const clean = rows.map((r) => r.trim()).filter(Boolean);
		last = JSON.stringify(clean);
		onchange(clean);
	}

	async function add(value = '') {
		rows = [...rows, value];
		if (value) emit();
		await tick();
		if (!value) document.getElementById(`${id}-${rows.length - 1}`)?.focus();
	}

	async function remove(i: number) {
		rows = rows.filter((_, n) => n !== i);
		emit();
		await tick();
		const next =
			document.getElementById(`${id}-${Math.min(i, rows.length - 1)}`) ??
			document.getElementById(`${id}-add`);
		next?.focus();
	}

	const offered = $derived(quick.filter((q) => !rows.some((r) => r.trim() === q.value)));
</script>

<div class="list-editor" {id} role="group" aria-describedby={describedby}>
	{#if rows.length}
		<ol class="list-rows">
			{#each rows as row, i (i)}
				{@const problem = row.trim() ? check(row.trim()) : ''}
				<li class="list-row">
					<div class="list-field">
						<Input
							id={`${id}-${i}`}
							value={row}
							{inputmode}
							{disabled}
							aria-label={itemLabel(i + 1)}
							invalid={!!problem}
							describedby={problem ? `${id}-${i}-error` : undefined}
							oninput={(e) => {
								rows[i] = (e.currentTarget as HTMLInputElement).value;
								emit();
							}}
						/>
						{#if problem}<p class="list-error" id={`${id}-${i}-error`}>{problem}</p>{/if}
					</div>
					<IconButton
						icon={ICONS.delete}
						label={`${tf('settings_ui.remove', 'Remove')}: ${itemLabel(i + 1)}`}
						{disabled}
						onclick={() => void remove(i)}
					/>
				</li>
			{/each}
		</ol>
	{:else}
		<p class="list-empty">{tf('settings_ui.list_empty', 'No entries.')}</p>
	{/if}
	<div class="list-add">
		<Button id={`${id}-add`} icon="add" size="sm" {disabled} onclick={() => void add()}>{addLabel}</Button>
		{#each offered as q (q.value)}
			<Button size="sm" variant="ghost" icon="add" {disabled} onclick={() => void add(q.value)}
				>{q.label}</Button
			>
		{/each}
	</div>
</div>

<style>
	.list-editor {
		display: flex;
		flex-direction: column;
		gap: var(--space-2);
	}
	.list-rows {
		display: flex;
		flex-direction: column;
		gap: var(--space-2);
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.list-row {
		display: flex;
		align-items: flex-start;
		gap: var(--space-2);
	}
	.list-field {
		flex: 1;
		min-width: 0;
		display: flex;
		flex-direction: column;
		gap: var(--space-1);
	}
	.list-error {
		margin: 0;
		color: var(--danger);
		font-size: var(--text-sm);
	}
	.list-empty {
		margin: 0;
		color: var(--text-muted);
	}
	.list-add {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
	}
	@media (pointer: coarse) {
		.list-row :global(.icon-btn),
		.list-add :global(.btn) {
			min-width: 44px;
			min-height: 44px;
		}
	}
</style>
