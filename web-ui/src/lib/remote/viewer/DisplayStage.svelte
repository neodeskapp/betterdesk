<!--
  The displays of one session side by side: as they are arranged on the
  device's desktop, or in a grid (display-layout.ts). Each tile is a canvas
  of its own that the client draws its display into (client.showDisplay),
  with the display's number over its corner. The tile being worked on has
  the accent border and streams live; the others follow the background
  setting, and say so when they are paused or slow. Displays open in a
  window of their own leave the stage; when all of them are, the stage
  says so and offers to bring them back.

  Keys go to the device from any tile, as from the single screen; F8 leaves.
-->
<script lang="ts">
	import { untrack } from 'svelte';
	import { tf } from '#lib/i18n/i18n.svelte.ts';
	import { layoutTiles } from '#lib/remote/client/display-layout.ts';
	import type { Viewer, ViewerSession } from '#lib/remote/viewer.svelte.ts';
	import Button from '#lib/ui/Button.svelte';

	interface Props {
		viewer: Viewer;
		session: ViewerSession;
		/** The id of the line that says how to leave the screen */
		describedby: string;
		onscreenfocus: () => void;
		onscreenblur: () => void;
		onscreenpress: () => void;
	}

	let { viewer, session: s, describedby, onscreenfocus, onscreenblur, onscreenpress }: Props = $props();

	let area = $state({ w: 0, h: 0 });
	const shown = $derived(s.displays.filter((d) => !s.popouts.includes(d.idx)));
	const tiles = $derived(layoutTiles(viewer.layoutOf(s), shown, area, 8));
	const name = $derived(s.label);

	function observe(node: HTMLElement) {
		const measure = () => {
			const r = node.getBoundingClientRect();
			if (r.width !== area.w || r.height !== area.h) area = { w: r.width, h: r.height };
		};
		measure();
		if (typeof ResizeObserver !== 'function') return undefined;
		const ro = new ResizeObserver(measure);
		ro.observe(node);
		return () => ro.disconnect();
	}

	// New tile sizes: each canvas takes its tile's size on the next frame
	$effect(() => {
		void tiles;
		const id = requestAnimationFrame(() => s.client?.resizeDisplays?.());
		return () => cancelAnimationFrame(id);
	});

	/**
	 * The tile's canvas is in the page: its display draws there until the
	 * tile goes. Untracked: the display worked on or its zoom changing must
	 * not make a new screen (it would be blank until the next picture).
	 */
	function screen(idx: number) {
		return (canvas: HTMLCanvasElement) => {
			untrack(() => viewer.attachTile(s, idx, canvas));
			return () => untrack(() => viewer.detachTile(s, idx));
		};
	}

	function stateOf(idx: number): string {
		if (idx === s.activeDisplay) return '';
		if (s.display.background === 'paused') return tf('remote_viewer.display_paused', 'Paused');
		if (s.display.background === 'slow') return tf('remote_viewer.display_slow', 'Every few seconds');
		return '';
	}
</script>

<div class="display-stage" {@attach observe}>
	{#each shown as d (d.idx)}
		{@const r = tiles.get(d.idx)}
		{@const state = stateOf(d.idx)}
		{#if r}
			<div
				class={['tile', d.idx === s.activeDisplay && 'is-active']}
				style:left="{r.x}px"
				style:top="{r.y}px"
				style:width="{r.w}px"
				style:height="{r.h}px"
			>
				<canvas
					{@attach screen(d.idx)}
					class="tile-canvas"
					tabindex="0"
					data-display={d.idx}
					aria-label={tf('remote_viewer.display_of', 'Display {n} of {name}', { n: d.idx + 1, name })}
					aria-describedby={describedby}
					onpointerdown={() => {
						onscreenpress();
						viewer.focusDisplay(s, d.idx);
					}}
					onfocus={() => {
						viewer.focusDisplay(s, d.idx);
						onscreenfocus();
					}}
					onblur={onscreenblur}
				></canvas>
				<span class="tile-label" aria-hidden="true">{d.idx + 1}</span>
				{#if state}<span class="tile-state">{state}</span>{/if}
			</div>
		{/if}
	{:else}
		<div class="all-out">
			<p>{tf('remote_viewer.all_in_windows', 'Every display is open in a window of its own.')}</p>
			<Button onclick={() => viewer.returnAllDisplays(s)}
				>{tf('remote_viewer.bring_displays_back', 'Bring the displays back')}</Button
			>
		</div>
	{/each}
</div>

<style>
	/* The letterbox stays black in every theme, as around the single screen */
	.display-stage {
		position: absolute;
		inset: 0;
		z-index: 1;
		overflow: hidden;
		background: #000000;
	}
	.tile {
		position: absolute;
		box-sizing: border-box;
		border: 2px solid #3a3f42;
	}
	.tile.is-active {
		border-color: var(--accent);
	}
	.tile-canvas {
		display: block;
		width: 100%;
		height: 100%;
		outline: none;
		cursor: crosshair;
	}
	.tile-canvas:focus-visible {
		outline: 2px solid var(--focus);
		outline-offset: -4px;
	}
	.tile-label,
	.tile-state {
		position: absolute;
		top: var(--space-1);
		padding: 0 var(--space-2);
		border-radius: var(--radius);
		background: rgba(0, 0, 0, 0.72);
		color: #ffffff;
		font-size: var(--text-sm);
		font-variant-numeric: tabular-nums;
		line-height: 1.6;
		pointer-events: none;
	}
	.tile-label {
		inset-inline-start: var(--space-1);
		font-weight: 600;
	}
	.tile-state {
		inset-inline-end: var(--space-1);
	}
	.all-out {
		display: flex;
		flex-direction: column;
		align-items: center;
		justify-content: center;
		gap: var(--space-3);
		height: 100%;
		padding: var(--space-4);
		color: #ffffff;
		text-align: center;
	}
	.all-out p {
		margin: 0;
	}
</style>
