<!--
  A slider for a setting row: the row names it, the value shows beside it
  ("12 px") and is said with it. The value is a number.

    <SettingRow id="bg-blur" label=... help=...>
        {#snippet control(f)}<RangeField {...f} unit="px" min={0} max={30} bind:value={...} />{/snippet}
    </SettingRow>
-->
<script lang="ts">
	interface Props {
		id: string;
		describedby?: string | undefined;
		unit: string;
		value: number;
		min: number;
		max: number;
		step?: number;
		disabled?: boolean;
	}

	let { id, describedby, unit, value = $bindable(), min, max, step = 1, disabled = false }: Props = $props();
</script>

<div class="range-field">
	<input
		{id}
		type="range"
		class="form-range"
		{min}
		{max}
		{step}
		{disabled}
		aria-describedby={describedby}
		aria-valuetext="{value} {unit}"
		bind:value
	/>
	<output class="num" for={id}>{value} {unit}</output>
</div>

<style>
	.range-field {
		display: flex;
		align-items: center;
		gap: var(--space-3);
		max-width: 420px;
	}
	.form-range {
		flex: 1;
		min-width: 0;
		margin: 0;
		accent-color: var(--accent);
	}
	.form-range:focus-visible {
		outline: var(--focus-ring);
		outline-offset: var(--focus-offset);
	}
	@media (pointer: coarse) {
		.form-range {
			height: 44px;
		}
	}
	.num {
		flex: none;
		min-width: 5ch;
		font-variant-numeric: tabular-nums;
		text-align: end;
	}
</style>
