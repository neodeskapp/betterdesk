<!--
  The filter field and the section links of a long settings page
  (PATTERN.md rule 9). The field filters the rows of the page through the
  SettingsFilter it gets; the links jump to the groups. When nothing
  matches it says so with what was typed.
-->
<script lang="ts">
	import { tf } from '#lib/i18n/i18n.svelte.ts';
	import SearchField from '../SearchField.svelte';
	import type { SettingsFilter } from './filter.svelte.ts';

	interface Props {
		filter: SettingsFilter;
		/** The field's label: "Filter permissions" */
		label?: string;
		sections?: readonly { id: string; label: string }[];
	}

	let { filter, label, sections = [] }: Props = $props();

	const none = $derived(filter.active && filter.shown.size > 0 && filter.shownCount === 0);
</script>

<!-- Not a setting: the check that every field has a row in the settings list skips it -->
<div class="settings-filter" data-search-skip>
	<SearchField
		label={label ?? tf('settings_ui.filter', 'Filter settings')}
		placeholder={label ?? tf('settings_ui.filter', 'Filter settings')}
		bind:value={filter.query}
	/>
	{#if sections.length && !filter.active}
		<nav class="section-links" aria-label={tf('settings_ui.sections', 'Sections on this page')}>
			<!-- Links within this page -->
			<!-- eslint-disable svelte/no-navigation-without-resolve -->
			{#each sections as s (s.id)}<a href={`#${s.id}`}>{s.label}</a>{/each}
			<!-- eslint-enable svelte/no-navigation-without-resolve -->
		</nav>
	{/if}
	<p class="filter-result" aria-live="polite">
		{#if none}{tf('settings_ui.filter_none', 'Nothing matches "{query}". Try fewer or other words.', {
				query: filter.query.trim()
			})}{:else if filter.active}{filter.shownCount === 1
				? tf('settings_ui.filter_one', '1 setting matches.')
				: tf('settings_ui.filter_n', '{count} settings match.', { count: filter.shownCount })}{/if}
	</p>
</div>

<style>
	.settings-filter {
		display: flex;
		flex-direction: column;
		gap: var(--space-2);
	}
	.settings-filter :global(.search-field) {
		max-width: 420px;
	}
	.section-links {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-1) var(--space-4);
		font-size: var(--text-sm);
	}
	.section-links a {
		display: inline-flex;
		align-items: center;
		min-height: 28px;
	}
	@media (pointer: coarse) {
		.section-links a {
			min-height: 44px;
		}
	}
	.filter-result {
		margin: 0;
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	.filter-result:empty {
		display: none;
	}
</style>
