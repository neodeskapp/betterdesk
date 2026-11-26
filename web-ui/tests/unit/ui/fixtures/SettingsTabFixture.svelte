<script lang="ts">
	import SettingsBody from '../../../../src/lib/ui/settings/SettingsBody.svelte';
	import SettingsGroup from '../../../../src/lib/ui/settings/SettingsGroup.svelte';
	import SettingRow from '../../../../src/lib/ui/settings/SettingRow.svelte';
	import { SettingsFilter } from '../../../../src/lib/ui/settings/filter.svelte.ts';

	let { error = '' }: { error?: string } = $props();

	const filter = new SettingsFilter();
	let mode = $state('light');
</script>

<!-- A tab of Settings: the Settings page is the frame, the tab brings the rest of the template -->
<SettingsBody {filter} sections={[{ id: 'g-look', label: 'Look' }]} summaryLabel="Preview">
	{#snippet summary()}<p>The preview</p>{/snippet}
	<SettingsGroup id="g-look" title="Look">
		<SettingRow id="r-mode" label="Default theme" help="What everyone sees." group>
			{#snippet control(f)}
				<div role="radiogroup" id={f.id} aria-labelledby={f.labelledby} aria-describedby={f.describedby}>
					<label><input type="radio" name="mode" value="light" bind:group={mode} /> Light</label>
					<label><input type="radio" name="mode" value="dark" bind:group={mode} /> Dark</label>
				</div>
			{/snippet}
		</SettingRow>
		<SettingRow id="r-color" label="Accent" help="Buttons and links." {error}>
			{#snippet control(f)}<input
					id={f.id}
					aria-describedby={f.describedby}
					aria-invalid={f.invalid || undefined}
				/>{/snippet}
		</SettingRow>
	</SettingsGroup>
</SettingsBody>
