<!--
  A menu item that keeps the menu open: an option of a group
  (menuitemradio, the default), a switch (menuitemcheckbox) or an action
  (menuitem), so several viewer settings can be changed in one go, as in
  the old Display and Screens menus. The shared MenuItem's radio closes its
  menu on select. hint: a line under the label that says what the choice
  does (the item's description, not part of its name). expanded: an action
  that shows or hides more choices (aria-expanded).
-->
<script lang="ts">
	import type { Snippet } from 'svelte';
	import Icon from '#lib/ui/Icon.svelte';

	interface Props {
		kind?: 'radio' | 'checkbox' | 'action';
		checked?: boolean;
		icon?: string;
		disabled?: boolean;
		title?: string;
		hint?: string;
		expanded?: boolean;
		onselect: () => void;
		children: Snippet;
	}

	let {
		kind = 'radio',
		checked = false,
		icon,
		disabled = false,
		title,
		hint,
		expanded,
		onselect,
		children
	}: Props = $props();
	const uid = $props.id();
	const role = $derived(
		kind === 'radio' ? 'menuitemradio' : kind === 'checkbox' ? 'menuitemcheckbox' : 'menuitem'
	);
</script>

<button
	type="button"
	class="menu-choice"
	{role}
	aria-checked={kind === 'action' ? undefined : checked}
	tabindex="-1"
	{disabled}
	{title}
	aria-expanded={expanded}
	aria-labelledby={hint ? `${uid}-label` : undefined}
	aria-describedby={hint ? `${uid}-hint` : undefined}
	onclick={() => {
		if (!disabled) onselect();
	}}
>
	{#if icon}<Icon name={icon} class="lead" />{/if}{#if hint}<span class="choice-text"
			><span class="menu-item-label" id="{uid}-label">{@render children()}</span><span
				class="choice-hint"
				id="{uid}-hint">{hint}</span
			></span
		>{:else}<span class="menu-item-label">{@render children()}</span>{/if}
	{#if kind !== 'action'}<Icon name="check" class={checked ? 'tick' : 'tick is-off'} />{/if}
</button>

<style>
	.menu-choice {
		display: flex;
		align-items: center;
		gap: var(--space-2);
		box-sizing: border-box;
		width: 100%;
		min-height: var(--control-h);
		padding: var(--space-1) var(--space-3);
		border: 0;
		background: transparent;
		color: var(--text);
		font: inherit;
		text-align: start;
		white-space: normal;
		cursor: pointer;
	}
	.menu-choice:hover,
	.menu-choice:focus-visible {
		background: var(--hover);
	}
	.menu-choice:focus-visible {
		outline: 2px solid var(--focus);
		outline-offset: -2px;
	}
	.menu-choice[aria-checked='true'] {
		font-weight: 600;
	}
	.menu-choice:disabled {
		color: var(--text-muted);
		cursor: not-allowed;
	}
	.menu-item-label {
		flex: 1;
		min-width: 0;
	}
	.choice-text {
		display: flex;
		flex: 1;
		flex-direction: column;
		min-width: 0;
		padding-block: 2px;
	}
	.choice-hint {
		color: var(--text-muted);
		font-size: var(--text-xs);
		font-weight: 400;
		line-height: 1.35;
	}
	.menu-choice :global(.lead) {
		font-size: 20px;
		color: var(--text-muted);
	}
	.menu-choice :global(.tick) {
		margin-inline-start: auto;
		font-size: 18px;
		color: var(--accent);
	}
	.menu-choice :global(.tick.is-off) {
		visibility: hidden;
	}
</style>
