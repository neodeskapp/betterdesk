<!--
  The one template of a settings page (PATTERN.md rule 10):

    title and one-line purpose
    the save-model line ("Changes are saved when you press Save.")
    the scope picker (role, organization), before anything it changes
    the summary (side column from 1200 px; under the scope before that)
    the filter and section links (pages with many settings)
    the groups
    the ChangesBar

  It is SettingsPageFrame around SettingsBody: the frame does the access
  check, #id links and the search scroll to a setting, and the question
  before leaving with unsaved changes. A tab of Settings uses SettingsBody
  alone, as the Settings page is its frame.

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
	import SettingsPageFrame from '#lib/settings/SettingsPageFrame.svelte';
	import type { SettingsFilter as Filter } from './filter.svelte.ts';
	import SettingsBody from './SettingsBody.svelte';

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
		summaryLabel?: string;
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
		summaryLabel,
		bar,
		children
	}: Props = $props();
</script>

<SettingsPageFrame {id} {title} {description} {need} {allowed}>
	<SettingsBody {saveModel} {filter} {filterLabel} {sections} {scope} {summary} {summaryLabel} {bar}>
		{@render children()}
	</SettingsBody>
</SettingsPageFrame>
