<!--
  The one template of a settings page (PATTERN.md rule 10):

    title and one-line purpose
    the save-model line ("Changes are saved when you press Save.")
    the scope picker (role, organization), before anything it changes
    the summary (side column from 1200 px; under the scope before that)
    the filter and section links (pages with many settings)
    the groups
    the ChangesBar

  It is SettingsPageFrame inside: the access check, #id links and the
  search scroll to a setting, and the question before leaving with
  unsaved changes.

    <SettingsPage id="permissions" title=... description=... saveModel="draft" filter={f} sections={...}>
        {#snippet scope()}<RolePicker .../>{/snippet}
        {#snippet summary()}...{/snippet}
        <SettingsGroup ...>...</SettingsGroup>
        {#snippet bar()}<ChangesBar {draft} onsave={save} />{/snippet}
    </SettingsPage>

  saveModel: "draft" (edit, then save), "instant" (the page's controls
  apply at once; Your account > Preferences only) or "none" (the page has
  only actions and facts).
-->
<script lang="ts">
	import type { Snippet } from 'svelte';
	import { tf } from '#lib/i18n/i18n.svelte.ts';
	import SettingsPageFrame from '#lib/settings/SettingsPageFrame.svelte';
	import { provideFilter, type SettingsFilter as Filter } from './filter.svelte.ts';
	import SettingsFilter from './SettingsFilter.svelte';

	interface Props {
		id: string;
		title: string;
		description: string;
		need?: string;
		allowed?: boolean;
		saveModel?: 'draft' | 'instant' | 'none';
		/** A filter for the rows; pages with more than about 15 settings */
		filter?: Filter;
		filterLabel?: string;
		sections?: readonly { id: string; label: string }[];
		scope?: Snippet;
		summary?: Snippet;
		bar?: Snippet;
		children: Snippet;
	}

	let {
		id,
		title,
		description,
		need,
		allowed,
		saveModel = 'draft',
		filter,
		filterLabel,
		sections = [],
		scope,
		summary,
		bar,
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

<SettingsPageFrame {id} {title} {description} {need} {allowed}>
	{#if modeLine}<p class="save-model">{modeLine}</p>{/if}
	<div class={['settings-body', summary && 'has-summary']}>
		{#if scope}<div class="settings-scope">{@render scope()}</div>{/if}
		{#if summary}<aside class="settings-summary" aria-label={tf('settings_ui.summary', 'Summary')}>
				{@render summary()}
			</aside>{/if}
		<div class="settings-main">
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
		</div>
	</div>
	{@render bar?.()}
</SettingsPageFrame>

<style>
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
	/* From 1200 px the summary is a side column next to the groups, beside the scope */
	@media (min-width: 1200px) {
		.settings-body.has-summary {
			grid-template-columns: minmax(0, 1fr) minmax(280px, 340px);
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
			position: sticky;
			top: var(--space-5);
		}
	}
</style>
