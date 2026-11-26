<script lang="ts">
	import SettingsPage from '../../../../src/lib/ui/settings/SettingsPage.svelte';
	import SettingsGroup from '../../../../src/lib/ui/settings/SettingsGroup.svelte';
	import SettingRow from '../../../../src/lib/ui/settings/SettingRow.svelte';
	import ChangesBar from '../../../../src/lib/ui/settings/ChangesBar.svelte';
	import { SettingsDraft } from '../../../../src/lib/ui/settings/draft.svelte.ts';
	import { SettingsFilter } from '../../../../src/lib/ui/settings/filter.svelte.ts';

	let { allowed = true, onsave = () => {} }: { allowed?: boolean; onsave?: (d: SettingsDraft) => void } =
		$props();

	const filter = new SettingsFilter();
	export const draft = new SettingsDraft(
		{ 'device.view': true, 'device.delete': false },
		{ 'device.view': { label: 'View devices' }, 'device.delete': { label: 'Delete devices' } }
	);
	$effect(() => draft.track());
</script>

<SettingsPage
	id="fx"
	title="Roles"
	description="What each role may do."
	{allowed}
	{filter}
	filterLabel="Filter permissions"
	sections={[
		{ id: 'g-dev', label: 'Devices' },
		{ id: 'g-srv', label: 'Server' }
	]}
>
	{#snippet scope()}<label for="fx-role">Role</label><select id="fx-role"><option>Operator</option></select
		>{/snippet}
	<SettingsGroup id="g-dev" title="Devices" consequence="Applies on the next page these people open.">
		<SettingRow
			id="r-view"
			label="View devices"
			help="See the device list."
			keyName="device.view"
			checked={draft.values['device.view'] as boolean}
			onchange={(v) => draft.set('device.view', v)}
			changed={draft.isChanged('device.view')}
			source={{ kind: 'default', text: 'Built-in default for this role' }}
		/>
		<SettingRow
			id="r-del"
			label="Delete devices"
			help="Remove devices for good."
			keyName="device.delete"
			checked={draft.values['device.delete'] as boolean}
			onchange={(v) => draft.set('device.delete', v)}
			changed={draft.isChanged('device.delete')}
			needs={draft.values['device.view']
				? []
				: [
						{
							text: 'Needs View devices.',
							fixLabel: 'Turn it on',
							onfix: () => draft.set('device.view', true)
						}
					]}
		/>
	</SettingsGroup>
	<SettingsGroup id="g-srv" title="Server">
		<SettingRow
			id="r-cfg"
			label="Server configuration"
			help="Change server settings."
			keyName="server.config"
			checked={false}
		/>
	</SettingsGroup>
	{#snippet bar()}<ChangesBar {draft} onsave={() => onsave(draft)} />{/snippet}
</SettingsPage>
