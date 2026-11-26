<!--
  How many copies to keep, without a number that means something else
  (PATTERN.md rule 4): a switch "Keep all" and, while it is off, the
  number. The value is { all, count }; the page maps it to what the
  server stores (often 0 for all). The number keeps what was typed when
  the switch goes on and back off. In a SettingRow with `group`, pass the
  row's `labelledby` so the row's name names the switch and number.
-->
<script lang="ts">
	import Input from '../Input.svelte';
	import Switch from '../Switch.svelte';

	interface Props {
		id: string;
		all: boolean;
		count: number;
		onchange: (value: { all: boolean; count: number }) => void;
		allLabel: string;
		countLabel: string;
		min?: number;
		max?: number;
		/** Why the number is refused; '' when fine */
		error?: string;
		disabled?: boolean;
		describedby?: string;
		/** The id of the name of the pair (a SettingRow's `labelledby`) */
		labelledby?: string;
	}

	let {
		id,
		all,
		count,
		onchange,
		allLabel,
		countLabel,
		min = 1,
		max = 1000,
		error = '',
		disabled = false,
		describedby,
		labelledby
	}: Props = $props();
</script>

<div class="keep-count" role="group" aria-labelledby={labelledby} aria-describedby={describedby}>
	<Switch
		id={`${id}-all`}
		label={allLabel}
		checked={all}
		{disabled}
		onchange={(e) => onchange({ all: (e.currentTarget as HTMLInputElement).checked, count })}
	/>
	{#if !all}
		<div class="keep-number">
			<label class="keep-label" for={`${id}-count`}>{countLabel}</label>
			<Input
				id={`${id}-count`}
				type="number"
				width="short"
				{min}
				{max}
				step="1"
				value={count}
				{disabled}
				invalid={!!error}
				describedby={error ? `${id}-error` : undefined}
				oninput={(e) => onchange({ all, count: Number((e.currentTarget as HTMLInputElement).value) })}
			/>
			{#if error}<p class="keep-error" id={`${id}-error`}>{error}</p>{/if}
		</div>
	{/if}
</div>

<style>
	.keep-count {
		display: flex;
		flex-direction: column;
		gap: var(--space-2);
	}
	.keep-number {
		display: flex;
		flex-direction: column;
		gap: var(--space-1);
		padding-inline-start: calc(var(--check-size) + var(--check-gap));
	}
	.keep-label {
		font-weight: 600;
	}
	.keep-error {
		margin: 0;
		color: var(--danger);
		font-size: var(--text-sm);
	}
</style>
