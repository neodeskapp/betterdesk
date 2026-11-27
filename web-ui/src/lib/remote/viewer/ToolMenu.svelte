<!--
  A control in the viewer's session bar that opens a menu: icon, and under
  it the label with a caret (visually hidden in the narrowest viewers, kept
  as the name). hint is the tooltip: what the menu holds. iconOnly keeps
  the label for the name and tooltip only and leaves out the caret. buttonId names the button, so a side sheet
  opened from the menu can give focus back to it. Same keyboard as MenuButton: Enter, Space or the arrows open it,
  Escape closes and returns focus.
-->
<script lang="ts">
	import type { Snippet } from 'svelte';
	import Button from '#lib/ui/Button.svelte';
	import Icon from '#lib/ui/Icon.svelte';
	import Menu from '#lib/ui/Menu.svelte';
	import { menuItems } from '#lib/ui/menu.ts';

	interface Props {
		label: string;
		/** The tooltip: what is in the menu (the label when left out) */
		hint?: string;
		icon: string;
		menuClass?: string;
		iconOnly?: boolean;
		buttonId?: string;
		onopen?: () => void;
		children: Snippet;
	}

	let { label, hint, icon, menuClass = '', iconOnly = false, buttonId, onopen, children }: Props = $props();

	const uid = $props.id();
	const btnId = $derived(buttonId ?? `${uid}-btn`);
	let open = $state(false);
	let button = $state<HTMLElement | null>(null);
	let initialFocus = $state<'first' | 'last'>('first');

	function show(where: 'first' | 'last') {
		if (open) {
			const menu = document.getElementById(`${uid}-menu`);
			const list = menu ? menuItems(menu) : [];
			(where === 'last' ? list.at(-1) : list[0])?.focus();
			return;
		}
		initialFocus = where;
		onopen?.();
		open = true;
	}
</script>

<span class="tool-anchor">
	<Button
		class={iconOnly ? 'toolbar-btn is-icon' : 'toolbar-btn'}
		{icon}
		bind:element={button}
		id={btnId}
		title={hint ?? label}
		aria-haspopup="menu"
		aria-expanded={open}
		aria-controls={open ? `${uid}-menu` : undefined}
		onclick={() => {
			if (open) {
				open = false;
				button?.focus();
			} else show('first');
		}}
		onkeydown={(e: KeyboardEvent) => {
			if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
				e.preventDefault();
				show(e.key === 'ArrowUp' ? 'last' : 'first');
			}
		}}
	>
		{#if iconOnly}<span class="sr-only">{label}</span>{:else}<span class="tb-label"
				>{label}<Icon name="expand_more" class="btn-caret" /></span
			>{/if}
	</Button>
	<Menu
		bind:open
		anchor={button}
		{initialFocus}
		id="{uid}-menu"
		labelledby={btnId}
		class={menuClass}
		onclose={(returnFocus) => {
			if (returnFocus) button?.focus();
		}}
	>
		{@render children()}
	</Menu>
</span>

<style>
	.tool-anchor {
		position: relative;
		display: inline-flex;
	}
	.tool-anchor :global(.btn-caret) {
		margin-inline-start: -1px;
		color: var(--text-muted);
		font-size: 16px;
	}
</style>
