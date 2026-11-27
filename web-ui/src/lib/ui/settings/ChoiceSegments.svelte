<!--
  A few short choices side by side, one click to pick (settings-pass-2.md
  rule B): "Not set / On / Off" for a setting that can inherit, instead of
  a switch behind a "Set here" button. Native radios in a radiogroup
  (arrow keys move the choice, Tab enters and leaves the group), drawn as
  one segmented control; the picked one has the accent tint and outline.
  The group is named by the setting row's name (`labelledby`) and
  described by its help (`describedby`).

    <SettingRow id="pol-audio" label="Allow sound" group>
        {#snippet control(f)}
            <ChoiceSegments id={f.id} labelledby={f.labelledby} describedby={f.describedby}
                options={[{ value: '', label: 'Not set' }, { value: 'on', label: 'On' }, { value: 'off', label: 'Off' }]}
                value={v} onchange={(v) => ...} />
        {/snippet}
    </SettingRow>

  The first radio carries the `id`, so a label or the settings search that
  focuses <id> lands in the group.
-->
<script module lang="ts">
	export interface Segment {
		value: string;
		label: string;
	}
</script>

<script lang="ts">
	interface Props {
		id: string;
		options: readonly Segment[];
		value: string;
		labelledby?: string;
		label?: string;
		describedby?: string;
		disabled?: boolean;
		invalid?: boolean;
		onchange: (value: string) => void;
	}

	let {
		id,
		options,
		value,
		labelledby,
		label,
		describedby,
		disabled = false,
		invalid = false,
		onchange
	}: Props = $props();
</script>

<div
	class="choice-segments"
	role="radiogroup"
	aria-labelledby={labelledby}
	aria-label={labelledby ? undefined : label}
	aria-describedby={describedby}
	aria-invalid={invalid || undefined}
>
	{#each options as o, i (o.value)}
		{@const optId = i === 0 ? id : `${id}-${i}`}
		<span class="segment">
			<input
				type="radio"
				id={optId}
				name={id}
				value={o.value}
				checked={value === o.value}
				{disabled}
				onchange={() => onchange(o.value)}
			/>
			<label for={optId}>{o.label}</label>
		</span>
	{/each}
</div>

<style>
	/* One line: four short words fit at 320 px; the row puts Reset under it
	   when they don't fit beside. Longer words or a bigger font wrap to a
	   second line instead of pushing the page sideways. */
	.choice-segments {
		display: inline-flex;
		flex-wrap: wrap;
		max-width: 100%;
	}
	.segment {
		position: relative;
		display: inline-flex;
	}
	/* The radio stays in the page for the keyboard and screen readers; the label is what shows */
	.segment input {
		position: absolute;
		inset: 0;
		width: 100%;
		height: 100%;
		margin: 0;
		opacity: 0;
		cursor: pointer;
	}
	.segment label {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		min-height: var(--control-h);
		min-width: 3.75rem;
		padding: 0 var(--control-pad-x);
		border: 1px solid var(--border);
		background: var(--bg);
		color: var(--text-muted);
		font-weight: var(--control-weight);
		line-height: var(--control-leading);
		white-space: nowrap;
		cursor: pointer;
		transition:
			background-color var(--motion),
			border-color var(--motion);
	}
	/* Neighbours share one border line */
	.segment + .segment label {
		margin-inline-start: -1px;
	}
	.segment:first-child label {
		border-start-start-radius: var(--radius);
		border-end-start-radius: var(--radius);
	}
	.segment:last-child label {
		border-start-end-radius: var(--radius);
		border-end-end-radius: var(--radius);
	}
	.segment input:hover:not(:disabled) + label {
		background: var(--bg-subtle);
		color: var(--text);
	}
	.segment input:checked + label {
		position: relative;
		z-index: 1;
		border-color: var(--accent);
		background: var(--accent-tint);
		color: var(--text);
	}
	.segment input:focus-visible + label {
		z-index: 2;
		outline: var(--focus-ring);
		outline-offset: var(--focus-offset);
	}
	.segment input:disabled + label {
		cursor: not-allowed;
		opacity: 0.6;
	}
	.segment input:disabled {
		cursor: not-allowed;
	}
	@media (pointer: coarse) {
		.segment label {
			min-height: 44px;
		}
	}
	/* Narrow phones: less room around each word */
	@media (max-width: 399.98px) {
		.segment label {
			min-width: 3rem;
			padding: 0 var(--control-pad-x-sm);
		}
	}
	/* Windows contrast themes drop the tint and the accent border: the
	   picked one takes the system's selection colours */
	@media (forced-colors: active) {
		.segment input:checked + label {
			border-color: Highlight;
			background: Highlight;
			color: HighlightText;
			forced-color-adjust: none;
		}
	}
</style>
