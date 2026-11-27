<!--
  The one template of a settings page (PATTERN.md rule 10, with
  settings-pass-2.md rules G and H):

    title and one-line purpose
    the scope picker (role, organization) and its facts, before anything it changes
    the summary (side column from 1200 px; under the scope before that)
    the filter and section links in one row (pages with many settings)
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

  saveModel: "draft" (edit, then save; the save bar says so once there is
  a change), "instant" (the page's controls apply at once, said once;
  Your account > Preferences only) or "none" (only actions and facts).
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
		/** Use the whole width (the configuration file editor) */
		wide?: boolean;
		saveModel?: 'draft' | 'instant' | 'none';
		/** A filter for the rows; pages with more than about 15 settings */
		filter?: Filter;
		filterLabel?: string;
		sections?: readonly { id: string; label: string }[];
		scope?: Snippet;
		summary?: Snippet;
		summaryLabel?: string;
		/** Below 1200 px the summary follows the groups instead of leading them */
		summaryLast?: boolean;
		bar?: Snippet;
		children: Snippet;
	}

	let {
		id,
		title,
		description,
		need,
		allowed,
		wide = false,
		saveModel = 'draft',
		filter,
		filterLabel,
		sections = [],
		scope,
		summary,
		summaryLabel,
		summaryLast = false,
		bar,
		children
	}: Props = $props();
</script>

<SettingsPageFrame {id} {title} {description} {need} {allowed} {wide}>
	<SettingsBody
		{saveModel}
		{filter}
		{filterLabel}
		{sections}
		{scope}
		{summary}
		{summaryLabel}
		{summaryLast}
		{bar}
	>
		{@render children()}
	</SettingsBody>
</SettingsPageFrame>
