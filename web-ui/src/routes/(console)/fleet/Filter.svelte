<!--
  A select with its label on one line, for the toolbars of the Fleet tabs
  (old .fleet-filter): "Organization [Demo Lab ▾]".
-->
<script lang="ts" generics="V extends string">
	import Select, { type SelectOption } from '#lib/ui/Select.svelte';

	interface Props {
		label: string;
		options: SelectOption<V>[];
		value: V;
		disabled?: boolean;
		describedby?: string | undefined;
		onchange?: () => void;
	}

	let { label, options, value = $bindable(), disabled = false, describedby, onchange }: Props = $props();
</script>

<label class="filter">
	<span class="filter-label">{label}</span>
	<Select {options} bind:value {disabled} {describedby} {onchange} />
</label>

<style>
	.filter {
		display: inline-flex;
		align-items: center;
		gap: var(--space-2);
		min-width: 0;
	}
	.filter-label {
		color: var(--text-muted);
		white-space: nowrap;
	}
	.filter :global(.form-select) {
		width: auto;
		max-width: 100%;
	}
	/* Narrow: the label over its select, so the selects of a stack share their start edge */
	@media (max-width: 767px) {
		.filter {
			flex-direction: column;
			align-items: flex-start;
			gap: var(--space-1);
		}
	}
</style>
