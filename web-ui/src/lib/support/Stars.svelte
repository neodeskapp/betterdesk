<!--
  Stars from 1 to 5, drawn: `value` filled (a fraction fills part of a
  star, for an average), the rest outlined. Screen readers hear the label
  ("4 of 5 stars") instead of five pictures.
-->
<script lang="ts">
	let { value, label, size = 16 }: { value: number; label: string; size?: number } = $props();

	const uid = $props.id();
	const filled = $derived(Math.max(0, Math.min(5, value)));
	const PATH = 'M10 1.5l2.6 5.3 5.9.9-4.25 4.15 1 5.85L10 14.95 4.75 17.7l1-5.85L1.5 7.7l5.9-.9z';
</script>

<span class="stars" role="img" aria-label={label}>
	{#each [0, 1, 2, 3, 4] as i (i)}
		{@const part = Math.max(0, Math.min(1, filled - i))}
		<svg width={size} height={size} viewBox="0 0 20 20" aria-hidden="true" focusable="false">
			{#if part > 0 && part < 1}
				<defs>
					<clipPath id="{uid}-{i}"><rect x="0" y="0" width={20 * part} height="20" /></clipPath>
				</defs>
			{/if}
			<path class="star-empty" d={PATH} />
			{#if part > 0}
				<path class="star-full" d={PATH} clip-path={part < 1 ? `url(#${uid}-${i})` : undefined} />
			{/if}
		</svg>
	{/each}
</span>

<style>
	.stars {
		display: inline-flex;
		gap: 2px;
		vertical-align: middle;
		line-height: 0;
	}
	.star-empty {
		fill: none;
		stroke: var(--text-muted);
		stroke-width: 1.2;
		stroke-linejoin: round;
	}
	.star-full {
		fill: var(--warning);
		stroke: var(--warning);
		stroke-width: 1.2;
		stroke-linejoin: round;
	}
	@media (forced-colors: active) {
		.star-full {
			fill: CanvasText;
			stroke: CanvasText;
		}
		.star-empty {
			stroke: CanvasText;
		}
	}
</style>
