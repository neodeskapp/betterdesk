<!--
  Settings › Joining (server.config): how a new device gets in and what
  people see of it. The join mode with the approval form and the tags
  (SignInTab, part "joining"; the mode itself is changed on Device tokens),
  then the default device visibility and the availability history
  (GeneralTab, view "joining"). Each part saves on its own.
-->
<script lang="ts">
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import SettingsLayout from '../SettingsLayout.svelte';
	import GeneralTab from './GeneralTab.svelte';
	import SignInTab from './SignInTab.svelte';

	const toc = $derived([
		{ id: 'joining-mode-section', label: t('tokens.enrollment_mode') },
		{ id: 'device-scope-settings-panel', label: t('settings.device_scope_title') },
		{ id: 'availability-history-settings-panel', label: t('settings.availability_history_title') }
	]);
</script>

<SettingsLayout {toc}>
	<SignInTab part="joining" bare />
	<GeneralTab view="joining" bare />
</SettingsLayout>
