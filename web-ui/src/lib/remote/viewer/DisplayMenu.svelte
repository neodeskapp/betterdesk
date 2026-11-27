<!--
  The Displays menu: a small map of the displays where they sit on the
  device's desktop to pick one, and the device's virtual displays. A device
  that streams several displays at once (RustDesk 1.2.4 and later, with at
  least two displays) also gets the layout (one at a time, as arranged,
  grid), the picked display's zoom and its own window, all displays in
  windows of their own (on each screen of this computer where the browser
  can place windows), and how the displays not worked on stream. Older
  devices and a single display get a one-column menu with the map and the
  virtual displays only.

  Shortcuts on the remote screen (also listed at the end of the menu):
  Ctrl+Alt+Shift with 1 to 9 picks a display, L the next layout, Z the zoom
  of the display worked on, P opens it in a window of its own.
-->
<script lang="ts">
	import { tf } from '#lib/i18n/i18n.svelte.ts';
	import { arrangedTiles, DISPLAY_LAYOUTS } from '#lib/remote/client/display-layout.ts';
	import { BACKGROUND_MODES } from '#lib/remote/client/display-streams.ts';
	import type { BackgroundMode, DisplayLayout } from '#lib/remote/rdclient.ts';
	import type { Viewer, ViewerSession } from '#lib/remote/viewer.svelte.ts';
	import Icon from '#lib/ui/Icon.svelte';
	import MenuDivider from '#lib/ui/MenuDivider.svelte';
	import MenuItem from '#lib/ui/MenuItem.svelte';
	import MenuChoice from './MenuChoice.svelte';
	import ToolMenu from './ToolMenu.svelte';

	let { viewer, session: s }: { viewer: Viewer; session: ViewerSession } = $props();

	const uid = $props.id();
	const MAP = { w: 232, h: 96 };
	/** The device streams several displays at once: layouts, windows and background streams */
	const together = $derived(s.together);
	const layout = $derived(viewer.layoutOf(s));
	const tiles = $derived(layout !== 'single');
	/** The display picked in the map: shown (single layout) or worked on (tiles) */
	const picked = $derived(
		!together
			? (s.monitors.find((m) => m.current)?.idx ?? 0)
			: tiles
				? s.activeDisplay
				: viewer.singleDisplay(s)
	);
	const map = $derived(arrangedTiles(s.displays, MAP, 4));
	const mapHeight = $derived(Math.max(...[...map.values()].map((r) => r.y + r.h), 0) + 2);
	const vd = $derived(s.virtualDisplays);
	const anyVirtual = $derived(
		vd.impl === 'amyuni_idd' ? (vd.amyuniCount ?? 0) > 0 : (vd.rustdeskDisplays ?? []).length > 0
	);
	const pickedOut = $derived(s.popouts.includes(picked));
	const zoom = $derived(s.display.zoom[picked] ?? 'fit');

	const layoutLabel = (l: DisplayLayout) =>
		l === 'single'
			? tf('remote_viewer.layout_single', 'One at a time')
			: l === 'arranged'
				? tf('remote_viewer.layout_arranged', 'As arranged')
				: tf('remote_viewer.layout_grid', 'Grid');
	const backgroundLabel = (m: BackgroundMode) =>
		m === 'live'
			? tf('remote_viewer.background_live', 'Live')
			: m === 'slow'
				? tf('remote_viewer.background_slow', 'A new picture every few seconds')
				: tf('remote_viewer.background_paused', 'Paused');

	function displayLabel(idx: number): string {
		const d = s.displays[idx];
		const n = tf('remote_viewer.display_n', 'Display {n}', { n: idx + 1 });
		if (!d) return n;
		const parts = [n];
		if (d.name && d.name !== n) parts.push(d.name);
		if (d.width && d.height) parts.push(`${d.width} × ${d.height}`);
		if (s.popouts.includes(idx)) parts.push(tf('remote_viewer.in_own_window', 'in its own window'));
		return parts.join(', ');
	}
</script>

<ToolMenu
	label={tf('remote_viewer.displays', 'Displays')}
	icon="screenshot_monitor"
	menuClass={together ? 'displays-menu' : 'displays-menu is-compact'}
	onopen={() => viewer.refreshMonitors(s)}
>
	<div class={['displays-grid', !together && 'is-compact']}>
		<div>
			{#if together}<div role="group" aria-labelledby="{uid}-layout">
					<div class="menu-label" id="{uid}-layout">{tf('remote_viewer.layout', 'Layout')}</div>
					{#each DISPLAY_LAYOUTS as l (l)}
						<MenuChoice
							checked={layout === l}
							disabled={l !== 'single' && !viewer.wide}
							onselect={() => viewer.setLayout(l)}>{layoutLabel(l)}</MenuChoice
						>
					{/each}
					{#if !viewer.wide}
						<p class="menu-note">
							{tf(
								'remote_viewer.layout_needs_width',
								'As arranged and Grid need a window at least 900 pixels wide and a mouse.'
							)}
						</p>
					{/if}
				</div>{/if}
			{#if s.displays.length}<div role="group" aria-labelledby="{uid}-map">
					<div class="menu-label" id="{uid}-map">
						{tiles ? tf('remote_viewer.work_on', 'Work on') : tf('remote_viewer.show_display', 'Show')}
					</div>
					<div class="display-map" style:height="{mapHeight}px">
						{#each s.displays as d (d.idx)}
							{@const r = map.get(d.idx)}
							{#if r}
								<button
									type="button"
									role="menuitemradio"
									tabindex="-1"
									class={[
										'map-display',
										picked === d.idx && 'is-picked',
										s.popouts.includes(d.idx) && 'is-out'
									]}
									style:left="{r.x}px"
									style:top="{r.y}px"
									style:width="{r.w}px"
									style:height="{r.h}px"
									aria-checked={picked === d.idx}
									aria-label={displayLabel(d.idx)}
									title={displayLabel(d.idx)}
									onclick={() => viewer.switchMonitor(d.idx)}
									><span aria-hidden="true">{d.idx + 1}</span>{#if s.popouts.includes(d.idx)}<Icon
											name="open_in_new"
											class="map-out"
										/>{/if}</button
								>
							{/if}
						{/each}
					</div>
				</div>{/if}
			{#if together}<div role="group" aria-labelledby="{uid}-picked">
					<div class="menu-label" id="{uid}-picked">
						{tf('remote_viewer.display_n', 'Display {n}', { n: picked + 1 })}
					</div>
					{#if tiles || pickedOut}
						<MenuChoice
							kind="checkbox"
							icon="fit_screen"
							checked={zoom === '1:1'}
							onselect={() => viewer.setDisplayZoom(s, picked, zoom === '1:1' ? 'fit' : '1:1')}
							>{tf('remote.scale_original', 'Original size')}</MenuChoice
						>
					{/if}
					<MenuItem
						icon="open_in_new"
						onselect={() => {
							if (!viewer.popOut(s, picked)) viewer.windowBlocked();
						}}
						>{pickedOut
							? tf('remote_viewer.show_own_window', 'Go to its window')
							: tf('remote_viewer.open_own_window', 'Open in a window of its own')}</MenuItem
					>
				</div>{/if}
		</div>
		<div>
			{#if together}<div role="group" aria-labelledby="{uid}-windows">
					<div class="menu-label" id="{uid}-windows">{tf('remote_viewer.windows', 'Windows')}</div>
					<MenuItem icon="dashboard" onselect={() => void viewer.spreadDisplays()}
						>{viewer.screensExtended
							? tf('remote_viewer.spread_screens', 'One display on each of my screens')
							: tf('remote_viewer.open_all_windows', 'Each display in a window of its own')}</MenuItem
					>
					{#if s.popouts.length}
						<MenuItem icon="close" onselect={() => viewer.returnAllDisplays(s)}
							>{tf('remote_viewer.bring_displays_back', 'Bring the displays back')}</MenuItem
						>
					{/if}
				</div>
				<div role="group" aria-labelledby="{uid}-bg">
					<div class="menu-label" id="{uid}-bg">
						{tf('remote_viewer.background_displays', 'Displays you are not working on')}
					</div>
					{#each BACKGROUND_MODES as m (m)}
						<MenuChoice checked={s.display.background === m} onselect={() => viewer.setBackgroundDisplays(m)}
							>{backgroundLabel(m)}</MenuChoice
						>
					{/each}
				</div>{/if}
			{#if vd.supported && vd.impl === 'rustdesk_idd'}
				<div role="group" aria-labelledby="{uid}-vd">
					<div class="menu-label" id="{uid}-vd">{tf('remote.virtual_displays', 'Virtual displays')}</div>
					{#each [1, 2, 3, 4] as idx (idx)}
						{@const on = (vd.rustdeskDisplays ?? []).includes(idx)}
						<MenuChoice
							kind="checkbox"
							icon="monitor"
							checked={on}
							onselect={() => viewer.toggleVirtualDisplay(idx, !on)}
							>{tf('remote.virtual_display', 'Virtual display')} {idx}</MenuChoice
						>
					{/each}
					{@render plugOutAll()}
				</div>
			{:else if vd.supported && vd.impl === 'amyuni_idd'}
				{@const count = vd.amyuniCount ?? 0}
				<div role="group" aria-labelledby="{uid}-vd">
					<div class="menu-label" id="{uid}-vd">{tf('remote.virtual_displays', 'Virtual displays')}</div>
					<MenuChoice
						kind="action"
						icon="remove"
						disabled={count <= 0}
						onselect={() => viewer.toggleVirtualDisplay(0, false)}
						>{tf('remote_viewer.fewer_displays', 'Remove a virtual display')}</MenuChoice
					>
					<MenuChoice
						kind="action"
						icon="add"
						disabled={count >= 4}
						onselect={() => viewer.toggleVirtualDisplay(0, true)}
						>{tf('remote_viewer.more_displays', 'Add a virtual display')}</MenuChoice
					>
					{@render plugOutAll()}
				</div>
			{/if}
		</div>
	</div>
	{#if together}
		<MenuDivider />
		<p class="menu-note">
			{tf(
				'remote_viewer.display_shortcuts',
				'On the screen, Ctrl+Alt+Shift and a number from 1 to 9 picks a display, L changes the layout, Z the zoom, and P opens the display in a window of its own.'
			)}
		</p>
	{/if}
</ToolMenu>

{#snippet plugOutAll()}
	<MenuChoice
		kind="action"
		icon="power_off"
		disabled={!anyVirtual}
		onselect={() => viewer.toggleVirtualDisplay(-1, false)}
		>{tf('remote.plug_out_all', 'Plug out all')}</MenuChoice
	>
{/snippet}

<style>
	:global(.displays-menu) {
		width: min(560px, calc(100vw - 2 * var(--space-4)));
	}
	/* Two columns: long labels (German, French) wrap instead of being cut */
	:global(.displays-menu .menu-item) {
		white-space: normal;
	}
	.displays-grid {
		display: grid;
		grid-template-columns: repeat(2, minmax(0, 1fr));
		gap: var(--space-1) var(--space-3);
	}
	/* Without layouts and windows: the map and the virtual displays in one column */
	:global(.displays-menu.is-compact) {
		width: min(280px, calc(100vw - 2 * var(--space-4)));
	}
	.displays-grid.is-compact {
		grid-template-columns: minmax(0, 1fr);
	}
	@media (max-width: 720px) {
		.displays-grid {
			grid-template-columns: minmax(0, 1fr);
		}
	}
	.menu-label {
		padding: var(--space-2) var(--space-3) var(--space-1);
		color: var(--text-muted);
		font-size: var(--text-xs);
		font-weight: 600;
		text-transform: uppercase;
		letter-spacing: 0.04em;
	}
	.menu-note {
		margin: 0;
		padding: var(--space-1) var(--space-3) var(--space-2);
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	/* The map: the displays where they sit on the device's desktop */
	.display-map {
		position: relative;
		margin: var(--space-1) var(--space-3) var(--space-2);
		width: 232px;
		max-width: calc(100% - 2 * var(--space-3));
	}
	.map-display {
		position: absolute;
		display: flex;
		align-items: center;
		justify-content: center;
		gap: 2px;
		box-sizing: border-box;
		padding: 0;
		border: 1px solid var(--border);
		border-radius: 2px;
		background: var(--bg-subtle);
		color: var(--text);
		font: inherit;
		font-size: var(--text-sm);
		font-variant-numeric: tabular-nums;
		cursor: pointer;
	}
	.map-display:hover {
		background: var(--hover);
	}
	.map-display:focus-visible {
		outline: 2px solid var(--focus);
		outline-offset: 1px;
	}
	.map-display.is-picked {
		border: 2px solid var(--accent);
		background: var(--accent-tint);
		font-weight: 600;
	}
	.map-display.is-out {
		border-style: dashed;
	}
	.map-display :global(.map-out) {
		font-size: 14px;
		color: var(--text-muted);
	}
</style>
