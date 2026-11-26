<!--
  The organization a page's settings are for (PATTERN.md rule 10: the
  scope picker first). A labelled select of the organizations the server
  lists, with a sentence that says what the choice changes. With
  `placeholder`, the first option is that text with the value '', so a
  page with no organization chosen yet says so instead of showing the
  first one.
-->
<script lang="ts">
	import Field from '../Field.svelte';
	import Select from '../Select.svelte';

	interface Props {
		label: string;
		hint?: string;
		orgs: readonly { id: string; name: string }[];
		value: string;
		onchange: (id: string) => void;
		id?: string;
		placeholder?: string;
		disabled?: boolean;
	}

	let {
		label,
		hint,
		orgs,
		value,
		onchange,
		id = 'scope-org',
		placeholder,
		disabled = false
	}: Props = $props();

	const options = $derived([
		...(placeholder ? [{ value: '', label: placeholder }] : []),
		...orgs.map((o) => ({ value: o.id, label: o.name || o.id }))
	]);
</script>

<Field {label} {hint} {id} width="host">
	{#snippet children(f)}
		<Select
			{...f}
			{options}
			{value}
			{disabled}
			onchange={(e) => onchange((e.currentTarget as HTMLSelectElement).value)}
		/>
	{/snippet}
</Field>
