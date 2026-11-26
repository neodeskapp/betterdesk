<!--
  The settings template without the page frame (PATTERN.md rule 10): the
  save-model line, the scope picker, the summary column, the filter and
  section links, the groups and the save bar. SettingsPage puts it in a
  page of its own; a tab of Settings uses it directly, since the Settings
  page is the frame there (title, purpose, access, #links, leave guard).

    <SettingsBody saveModel="draft" filter={f} sections={...} summaryLabel="Preview">
        {#snippet summary()}<ThemePreview ... />{/snippet}
        <SettingsGroup ...>...</SettingsGroup>
        {#snippet bar()}<ChangesBar {draft} onsave={save} />{/snippet}
    </SettingsBody>

  `top` holds what comes before the filter and the groups (a read-only
  note, a warning about the current values). The summary is a side column
  from 1200 px, sticky while the groups scroll on a window at least 1000 px tall; `summaryLabel` names it.
  Below 1200 px it comes first, or after the groups with `summaryLast`.
-->
<script lang="ts">
	import type { Snippet } from 'svelte';
	import { tf } from '#lib/i18n/i18n.svelte.ts';
	import { provideFilter, type SettingsFilter as Filter } from './filter.svelte.ts';
	import SettingsFilter from './SettingsFilter.svelte';

	interface Props {
		saveModel?: 'draft' | 'instant' | 'none';
		/** A filter for the rows; pages with more than about 15 settings */
		filter?: Filter;
		filterLabel?: string;
		sections?: readonly { id: string; label: string }[];
		scope?: Snippet;
		summary?: Snippet;
		/** The side column's name for screen readers; "Summary" by default */
		summaryLabel?: string;
		/** Below 1200 px the summary follows the groups (a preview of what they change) instead of coming first */
		summaryLast?: boolean;
		top?: Snippet;
		bar?: Snippet;
		class?: string;
		children: Snippet;
	}

	let {
		saveModel = 'draft',
		filter,
		filterLabel,
		sections = [],
		scope,
		summary,
		summaryLabel,
		summaryLast = false,
		top,
		bar,
		class: cls = '',
		children
	}: Props = $props();

	// The filter is fixed for the page's life
	// svelte-ignore state_referenced_locally
	if (filter) provideFilter(filter);

	const modeLine = $derived(
		saveModel === 'draft'
			? tf(
					'settings_ui.model_draft',
					'Changes are saved when you press Save. Leaving the page with unsaved changes asks first.'
				)
			: saveModel === 'instant'
				? tf('settings_ui.model_instant', 'Changes here apply at once, only for you.')
				: ''
	);
</script>

<!-- One element: a page that spaces its children apart must not space the save-model line from the rest -->
<div class={['settings-tab-body', cls]}>
	{#if modeLine}<p class="save-model">{modeLine}</p>{/if}
	<div
		class={['settings-body', summary && 'has-summary', scope && 'has-scope', summaryLast && 'summary-last']}
	>
		{#if scope}<div class="settings-scope">{@render scope()}</div>{/if}
		{#if summary}<aside
				class="settings-summary"
				aria-label={summaryLabel ?? tf('settings_ui.summary', 'Summary')}
				data-search-skip
			>
				{@render summary()}
			</aside>{/if}
		<div class="settings-main">
			{@render top?.()}
			{#if filter || sections.length}
				{#if filter}
					<SettingsFilter {filter} label={filterLabel} {sections} />
				{:else}
					<!-- Section links only -->
					<nav class="section-links" aria-label={tf('settings_ui.sections', 'Sections on this page')}>
						<!-- eslint-disable svelte/no-navigation-without-resolve -->
						{#each sections as s (s.id)}<a href={`#${s.id}`}>{s.label}</a>{/each}
						<!-- eslint-enable svelte/no-navigation-without-resolve -->
					</nav>
				{/if}
			{/if}
			{@render children()}
			{@render bar?.()}
		</div>
	</div>
</div>

<style>
	.settings-tab-body {
		min-width: 0;
	}
	.save-model {
		margin: calc(-1 * var(--space-3)) 0 var(--space-5);
		max-width: var(--measure);
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	.settings-body {
		display: grid;
		grid-template-columns: minmax(0, 1fr);
		gap: var(--space-6);
		align-items: start;
	}
	.settings-scope {
		max-width: var(--measure);
	}
	.settings-main {
		display: flex;
		flex-direction: column;
		gap: var(--space-6);
		min-width: 0;
		container: settings-main / inline-size;
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
	.summary-last .settings-summary {
		order: 1;
	}
	/* From 1200 px the summary is a side column next to the groups, beside the scope */
	@media (min-width: 1200px) {
		.settings-body.has-summary {
			grid-template-columns: minmax(0, 1fr) minmax(280px, 340px);
			grid-template-areas: 'main summary';
		}
		/* The scope above the groups, the summary beside both */
		.settings-body.has-summary.has-scope {
			grid-template-areas:
				'scope summary'
				'main summary';
		}
		.has-summary .settings-scope {
			grid-area: scope;
		}
		.has-summary .settings-main {
			grid-area: main;
		}
		.has-summary .settings-summary {
			grid-area: summary;
		}
	}
	/* Sticky only on a window tall enough for the whole column: a sticky
	   column taller than the window would hold its last links out of view
	   when they get focus (WCAG 2.4.11) */
	@media (min-width: 1200px) and (min-height: 1000px) {
		.has-summary .settings-summary {
			position: sticky;
			top: var(--space-5);
		}
	}
</style>
