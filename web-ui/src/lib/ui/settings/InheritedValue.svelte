<!--
  A control for a setting that can come from somewhere else, outside a
  SettingRow (settings-pass-2.md rule B): the control is always there.
  While the setting inherits, a short muted hint beside it says what
  applies and where that is set; once a value is set here, a small link
  beside it goes back to the inherited value. Inside a SettingRow, use the
  row's `source` and `onreset` instead.
  `name` (the setting's name) goes into the link's accessible name, for
  pages with many such controls: "Use the server value: Allow sound".

    <InheritedValue id="pol-relay" overridden={has} inherited={t('...server: Direct first')}
        href="/settings#..." linkLabel={t('...Settings, General')}
        inheritLabel={t('...Use the server value')} oninherit={() => ...}>
        <Select ... />
    </InheritedValue>
-->
<script lang="ts">
	import type { Snippet } from 'svelte';
	import Button from '../Button.svelte';

	interface Props {
		id: string;
		/** The setting's name, for the link's accessible name */
		name?: string;
		overridden: boolean;
		/** What applies while nothing is set here, as a short sentence */
		inherited: string;
		href?: string;
		linkLabel?: string;
		inheritLabel: string;
		oninherit: () => void;
		disabled?: boolean;
		children: Snippet;
	}

	let {
		id,
		name,
		overridden,
		inherited,
		href,
		linkLabel,
		inheritLabel,
		oninherit,
		disabled = false,
		children
	}: Props = $props();
</script>

<div class={['inherited-value', overridden && 'is-overridden']} {id}>
	<div class="inherited-control">{@render children()}</div>
	{#if overridden}
		<Button
			variant="link"
			size="sm"
			{disabled}
			aria-label={name ? `${inheritLabel}: ${name}` : undefined}
			onclick={oninherit}>{inheritLabel}</Button
		>
	{:else}
		<p class="inherited-text">
			{inherited}
			<!-- An address inside the console, as the page gave it -->
			<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -->
			{#if href}<a {href}>{linkLabel}</a>{/if}
		</p>
	{/if}
</div>

<style>
	.inherited-value {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-1) var(--space-3);
		min-width: 0;
	}
	.inherited-control {
		min-width: 0;
	}
	.inherited-text {
		margin: 0;
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	.inherited-text a {
		margin-inline-start: var(--space-1);
	}
	@media (pointer: coarse) {
		.inherited-value > :global(.btn) {
			min-height: 44px;
		}
	}
</style>
