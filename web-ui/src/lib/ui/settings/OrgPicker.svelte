<!--
  The organization a page's settings are for (PATTERN.md rule 10: the
  scope picker first). A labelled select of the organizations the server
  lists, with a sentence that says what the choice changes.
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
	}

	let { label, hint, orgs, value, onchange, id = 'scope-org' }: Props = $props();

	const options = $derived(orgs.map((o) => ({ value: o.id, label: o.name || o.id })));
</script>

<Field {label} {hint} {id} width="host">
	{#snippet children(f)}
		<Select
			{...f}
			{options}
			{value}
			onchange={(e) => onchange((e.currentTarget as HTMLSelectElement).value)}
		/>
	{/snippet}
</Field>
