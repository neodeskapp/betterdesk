<script lang="ts">
	import SettingsPage from '../../../../src/lib/ui/settings/SettingsPage.svelte';
	import SettingsGroup from '../../../../src/lib/ui/settings/SettingsGroup.svelte';

	let { summaryLast = false, withScope = false }: { summaryLast?: boolean; withScope?: boolean } = $props();
</script>

{#snippet scopeSnippet()}<label for="fx-org">Organization</label><select id="fx-org"
		><option>A</option></select
	>{/snippet}

<SettingsPage
	id="fx-summary"
	allowed
	title="Backups"
	description="Copies of the server."
	{summaryLast}
	scope={withScope ? scopeSnippet : undefined}
>
	{#snippet summary()}<p>What a backup holds</p>{/snippet}
	<SettingsGroup id="g-sched" title="Scheduled backups"><p>rows</p></SettingsGroup>
</SettingsPage>
