<!--
  The filter field and the section links of a long settings page
  (settings-pass-2.md rule H): one row, the field first, the links in
  normal text after it; they wrap under it on a narrow screen. The field
  filters the rows of the page through the SettingsFilter it gets; the
  links jump to the groups. When nothing matches it says so with what was
  typed. The field sits in a search landmark named like it.
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
	const name = $derived(label ?? tf('settings_ui.filter', 'Filter settings'));
</script>

<div class="settings-filter" data-search-skip>
	<!-- A search landmark: the field finds settings and is none itself -->
	<div class="filter-field" role="search" aria-label={name}>
		<SearchField label={name} placeholder={name} bind:value={filter.query} />
	</div>
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
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-2) var(--space-5);
		max-width: var(--settings-max, 72rem);
	}
	.filter-field {
		flex: 0 1 20rem;
		min-width: min(100%, 14rem);
	}
	.section-links {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-1) var(--space-4);
	}
	.section-links a {
		display: inline-flex;
		align-items: center;
		min-height: 32px;
		text-decoration: none;
	}
	.section-links a:hover {
		text-decoration: underline;
	}
	@media (pointer: coarse) {
		.section-links a {
			min-height: 44px;
		}
	}
	.filter-result {
		flex-basis: 100%;
		margin: 0;
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	.filter-result:empty {
		display: none;
	}
</style>
