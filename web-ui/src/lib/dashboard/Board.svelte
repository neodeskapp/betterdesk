<!--
  One dashboard: its widgets on a 12-column grid in reading order, each a
  third, half, two thirds or the whole row wide (fewer columns on narrow
  screens, one on phones). Each widget's rows follow its height, so a tall
  widget beside short ones leaves no hole under them.

  While editing, every widget gets a bar with its name and controls: move
  earlier or later (also by dragging the bar), width, settings, remove.
  The widget itself is inert then, so a click on it doesn't leave the page.
-->
<script lang="ts">
	import type { Component } from 'svelte';
	import { tf } from '#lib/i18n/i18n.svelte.ts';
	import { can } from '#lib/state/session.svelte.ts';
	import IconButton from '#lib/ui/IconButton.svelte';
	import MenuButton from '#lib/ui/MenuButton.svelte';
	import MenuItem from '#lib/ui/MenuItem.svelte';
	import {
		kindOf,
		METRIC_INFO,
		metricOf,
		SIZE_LABELS,
		SIZES,
		visibleTo,
		type Board,
		type Widget,
		type WidgetSize
	} from './model.ts';
	import Attention from './widgets/Attention.svelte';
	import Boards from './widgets/Boards.svelte';
	import ClientSetup from './widgets/ClientSetup.svelte';
	import Devices from './widgets/Devices.svelte';
	import Links from './widgets/Links.svelte';
	import Note from './widgets/Note.svelte';
	import ServerStatus from './widgets/ServerStatus.svelte';
	import Stat from './widgets/Stat.svelte';
	import Trends from './widgets/Trends.svelte';
	import type { WidgetProps } from './widgets/types.ts';

	interface Props extends Omit<WidgetProps, 'widget'> {
		board: Board;
		editing?: boolean;
		onmove?: (id: string, steps: number) => void;
		ondrop?: (id: string, to: number) => void;
		onresize?: (id: string, size: WidgetSize) => void;
		onconfigure?: (id: string) => void;
		onremove?: (id: string) => void;
	}

	let {
		board,
		editing = false,
		onmove,
		ondrop,
		onresize,
		onconfigure,
		onremove,
		data,
		actions,
		boards
	}: Props = $props();

	const COMPONENTS: Record<string, Component<WidgetProps>> = {
		attention: Attention,
		devices: Devices,
		stat: Stat,
		trends: Trends,
		'client-setup': ClientSetup,
		'server-status': ServerStatus,
		note: Note,
		links: Links,
		boards: Boards
	};

	const shown = $derived(editing ? board.widgets : board.widgets.filter((w) => visibleTo(w, can)));

	function nameOf(w: Widget): string {
		const kind = kindOf(w.type);
		if (!kind) return tf('dashboard.widget.unknown', 'Unknown widget');
		if (w.title) return w.title;
		// A number is known by what it counts
		if (w.type === 'stat') {
			const info = METRIC_INFO[metricOf(w)];
			return tf(info.key, info.label);
		}
		return tf(kind.nameKey, kind.name);
	}

	/* ── Each widget's height in grid rows (ROW px each) ── */

	const ROW = 8;
	let spans = $state<Record<string, number>>({});

	function measure(node: HTMLElement, id: string) {
		let current = id;
		const set = () => {
			const n = Math.max(1, Math.ceil(node.getBoundingClientRect().height / ROW));
			if (spans[current] !== n) spans[current] = n;
		};
		set();
		if (typeof ResizeObserver === 'undefined') return { update: (next: string) => (current = next) };
		const ro = new ResizeObserver(set);
		ro.observe(node);
		return {
			update(next: string) {
				current = next;
				set();
			},
			destroy: () => ro.disconnect()
		};
	}

	/* ── Dragging a widget by its bar ── */

	let dragging = $state<string | null>(null);
	let dropAt = $state<number | null>(null);

	function dragstart(e: DragEvent, id: string) {
		dragging = id;
		if (e.dataTransfer) {
			e.dataTransfer.effectAllowed = 'move';
			e.dataTransfer.setData('text/plain', id);
		}
	}

	function dragover(e: DragEvent, index: number) {
		if (!dragging) return;
		e.preventDefault();
		const box = (e.currentTarget as HTMLElement).getBoundingClientRect();
		const rtl = getComputedStyle(e.currentTarget as HTMLElement).direction === 'rtl';
		// Over the first half (in reading direction): before this widget, else after it
		const before =
			box.width > box.height * 1.5
				? rtl
					? e.clientX > box.left + box.width / 2
					: e.clientX < box.left + box.width / 2
				: e.clientY < box.top + box.height / 2;
		dropAt = before ? index : index + 1;
	}

	function drop(e: DragEvent) {
		e.preventDefault();
		if (dragging && dropAt !== null) {
			const from = board.widgets.findIndex((w) => w.id === dragging);
			// Removing the widget first moves the later places one up
			ondrop?.(dragging, dropAt > from ? dropAt - 1 : dropAt);
		}
		dragend();
	}

	function dragend() {
		dragging = null;
		dropAt = null;
	}
</script>

<div class={['board', editing && 'is-editing']}>
	<div class="board-grid">
		{#each shown as w, i (w.id)}
			{@const kind = kindOf(w.type)}
			{@const View = COMPONENTS[w.type]}
			{@const allowed = visibleTo(w, can)}
			<div
				class={[
					'board-item',
					`size-${w.size}`,
					dragging === w.id && 'is-dragging',
					editing && dropAt === i && 'drop-before',
					editing && dropAt === i + 1 && i === shown.length - 1 && 'drop-after'
				]}
				style:grid-row-end="span {spans[w.id] ?? 20}"
				ondragover={editing ? (e) => dragover(e, i) : undefined}
				ondrop={editing ? drop : undefined}
				role={editing ? 'group' : undefined}
				aria-label={editing ? nameOf(w) : undefined}
			>
				<div class="board-item-inner" use:measure={w.id}>
					{#if editing}
						<div
							class="item-bar"
							draggable="true"
							ondragstart={(e) => dragstart(e, w.id)}
							ondragend={dragend}
							role="presentation"
						>
							<span class="item-name">{nameOf(w)}</span>
							<IconButton
								icon="expand_less"
								label={tf('dashboard.move_earlier', 'Move earlier')}
								disabled={i === 0}
								onclick={() => onmove?.(w.id, -1)}
							/>
							<IconButton
								icon="expand_more"
								label={tf('dashboard.move_later', 'Move later')}
								disabled={i === shown.length - 1}
								onclick={() => onmove?.(w.id, 1)}
							/>
							{#if kind}
								<MenuButton kebab label={tf('dashboard.widget_options', 'Widget options')} align="end">
									{#each SIZES.filter((s) => kind.sizes.includes(s)) as s (s)}
										<MenuItem icon={w.size === s ? 'check' : undefined} onselect={() => onresize?.(w.id, s)}
											>{tf(...SIZE_LABELS[s])}</MenuItem
										>
									{/each}
									{#if kind.configurable || !kind.untitled}
										<MenuItem icon="tune" onselect={() => onconfigure?.(w.id)}
											>{tf('dashboard.widget_settings', 'Settings')}</MenuItem
										>
									{/if}
									<MenuItem icon="delete" onselect={() => onremove?.(w.id)}
										>{tf('dashboard.remove_widget', 'Remove')}</MenuItem
									>
								</MenuButton>
							{:else}
								<IconButton
									icon="delete"
									label={tf('dashboard.remove_widget', 'Remove')}
									onclick={() => onremove?.(w.id)}
								/>
							{/if}
						</div>
					{/if}
					<div class="item-body" inert={editing}>
						{#if View && allowed}
							<View widget={w} {data} {actions} {boards} />
						{:else if editing}
							<p class="item-missing">
								{kind
									? tf('dashboard.widget.not_allowed', 'Your account can’t see this.')
									: tf('dashboard.widget.unknown_hint', 'This console version doesn’t know this widget.')}
							</p>
						{/if}
					</div>
				</div>
			</div>
		{:else}
			<p class="board-empty">
				{editing
					? tf('dashboard.empty_editing', 'This dashboard is empty. Add a widget to start.')
					: tf('dashboard.empty', 'This dashboard is empty.')}
			</p>
		{/each}
	</div>
</div>

<style>
	.board {
		container-type: inline-size;
	}

	/* Rows of 8 px: each widget spans as many as its height needs, so
	   columns fill up independently and reading order stays the DOM order */
	.board-grid {
		display: grid;
		/* Phones: one column. Twelve with their gaps would be wider than the screen. */
		grid-template-columns: minmax(0, 1fr);
		grid-auto-rows: 8px;
		grid-auto-flow: row dense;
		align-items: start;
	}

	.board-item {
		grid-column: 1 / -1;
		min-width: 0;
	}
	/* The gap under each widget is part of its height, so the rows don't add their own */
	.board-item-inner {
		padding-bottom: var(--space-6);
	}

	@container (min-width: 640px) {
		.board-grid {
			grid-template-columns: repeat(12, minmax(0, 1fr));
			column-gap: var(--space-6);
		}
		.size-s,
		.size-m {
			grid-column: span 6;
		}
	}
	@container (min-width: 1000px) {
		.board-grid {
			column-gap: var(--space-7);
		}
		.size-s {
			grid-column: span 4;
		}
		.size-m {
			grid-column: span 6;
		}
		.size-l {
			grid-column: span 8;
		}
	}

	/* Editing: a frame per widget, the bar on top */
	.is-editing .board-item-inner {
		padding-bottom: var(--space-4);
	}
	.is-editing .board-item-inner > .item-body,
	.is-editing .item-bar {
		border: 1px dashed var(--border);
	}
	.item-bar {
		display: flex;
		align-items: center;
		gap: var(--space-1);
		padding: var(--space-1) var(--space-1) var(--space-1) var(--space-3);
		border-bottom: 0;
		border-start-start-radius: var(--radius);
		border-start-end-radius: var(--radius);
		background: var(--bg-subtle);
		cursor: grab;
	}
	.item-bar:active {
		cursor: grabbing;
	}
	.item-name {
		flex: 1;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		font-weight: 600;
	}
	.is-editing .item-body {
		padding: var(--space-3);
		border-end-start-radius: var(--radius);
		border-end-end-radius: var(--radius);
		opacity: 0.75;
		/* A tall widget stays short while it is arranged */
		max-height: 18rem;
		overflow: hidden;
	}
	.is-dragging {
		opacity: 0.4;
	}
	.drop-before > .board-item-inner {
		box-shadow: inset 0 3px 0 var(--accent);
	}
	.drop-after > .board-item-inner {
		box-shadow: inset 0 -3px 0 var(--accent);
	}
	.item-missing,
	.board-empty {
		margin: 0;
		color: var(--text-muted);
	}
	.board-empty {
		grid-column: 1 / -1;
		grid-row: span 6;
	}
</style>
