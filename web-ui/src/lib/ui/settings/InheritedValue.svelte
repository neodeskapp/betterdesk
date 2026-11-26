<!--
  A setting that can come from somewhere else (PATTERN.md rule 2): while
  it inherits, it shows what applies and where that is set, and a button
  to set it here; once set here, the control and a button to go back.
  Both are explicit actions; nothing is overridden by touching a field.

    <InheritedValue id="pol-relay" overridden={has} inherited={t('...server: Direct first')}
        href="/settings#..." linkLabel={t('...Settings, General')}
        overrideLabel={t('...Set for this organization')} inheritLabel={t('...Use the server value')}
        onoverride={() => ...} oninherit={() => ...}>
        <Switch ... />
    </InheritedValue>
-->
<script lang="ts">
	import type { Snippet } from 'svelte';
	import Button from '../Button.svelte';

	interface Props {
		id: string;
		overridden: boolean;
		/** What applies while nothing is set here, as a sentence */
		inherited: string;
		href?: string;
		linkLabel?: string;
		overrideLabel: string;
		inheritLabel: string;
		onoverride: () => void;
		oninherit: () => void;
		disabled?: boolean;
		children: Snippet;
	}

	let {
		id,
		overridden,
		inherited,
		href,
		linkLabel,
		overrideLabel,
		inheritLabel,
		onoverride,
		oninherit,
		disabled = false,
		children
	}: Props = $props();
</script>

<div class={['inherited-value', overridden && 'is-overridden']} {id}>
	{#if overridden}
		<div class="inherited-control">{@render children()}</div>
		<Button variant="link" size="sm" {disabled} onclick={oninherit}>{inheritLabel}</Button>
	{:else}
		<p class="inherited-text">
			{inherited}
			<!-- An address inside the console, as the page gave it -->
			<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -->
			{#if href}<a {href}>{linkLabel}</a>{/if}
		</p>
		<Button size="sm" {disabled} onclick={onoverride}>{overrideLabel}</Button>
	{/if}
</div>

<style>
	.inherited-value {
		display: flex;
		flex-direction: column;
		align-items: flex-start;
		gap: var(--space-2);
		min-width: 0;
	}
	.inherited-control {
		align-self: stretch;
		min-width: 0;
	}
	.inherited-text {
		margin: 0;
		color: var(--text-muted);
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
