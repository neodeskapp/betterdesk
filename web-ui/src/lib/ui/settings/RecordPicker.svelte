<!--
  Choose several existing records (PATTERN.md rule 4): one checkbox per
  record with its name and, when given, a sentence. A stored value that no
  record has any more stays in the list as "(no longer exists)" so saving
  never drops it unseen; unticking it removes it. RolePicker is this with
  the server's roles
  (pickers.ts roleOptions); RelayPicker is ListEditor with relayOptions as
  its one-click records.

    <RecordPicker id="ops" legend={t('...')} options={[{ value, label, hint }]} value={list} onchange={...} />
-->
<script module lang="ts">
	export interface PickOption {
		value: string;
		label: string;
		hint?: string;
	}
</script>

<script lang="ts">
	import { tf } from '#lib/i18n/i18n.svelte.ts';
	import Checkbox from '../Checkbox.svelte';

	interface Props {
		id: string;
		legend: string;
		options: readonly PickOption[];
		value: readonly string[];
		onchange: (value: string[]) => void;
		describedby?: string;
		disabled?: boolean;
	}

	let { id, legend, options, value, onchange, describedby, disabled = false }: Props = $props();

	const known = $derived(new Set(options.map((o) => o.value)));
	const all = $derived([
		...options,
		...value
			.filter((v) => !known.has(v))
			.map((v) => ({
				value: v,
				label: tf('settings_ui.gone_record', '{name} (no longer exists)', { name: v }),
				hint: undefined
			}))
	]);

	function toggle(v: string, on: boolean) {
		const next = on ? [...value.filter((x) => x !== v), v] : value.filter((x) => x !== v);
		// Keep the order of the options
		const order = all.map((o) => o.value);
		onchange(next.sort((a, b) => order.indexOf(a) - order.indexOf(b)));
	}
</script>

<fieldset class="record-picker" {id} aria-describedby={describedby}>
	<legend class="sr-only">{legend}</legend>
	{#each all as o (o.value)}
		<Checkbox
			id={`${id}-${o.value}`}
			label={o.label}
			hint={o.hint}
			checked={value.includes(o.value)}
			{disabled}
			onchange={(e) => toggle(o.value, (e.currentTarget as HTMLInputElement).checked)}
		/>
	{/each}
</fieldset>

<style>
	.record-picker {
		display: flex;
		flex-direction: column;
		gap: var(--space-2);
		margin: 0;
		padding: 0;
		border: 0;
		min-width: 0;
	}
</style>
