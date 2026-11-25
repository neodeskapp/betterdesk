<!--
  Server › Configuration files (/server/config-files, server.config): the
  server's configuration files with an editor and the restart they may
  need. Settings › Advanced before (/settings?tab=advanced leads here).
-->
<script lang="ts">
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import SettingsPageFrame from '#lib/settings/SettingsPageFrame.svelte';
	import AdvancedTab from '#lib/settings/tabs/AdvancedTab.svelte';
</script>

<SettingsPageFrame
	id="server-config-files"
	wide
	title={t('nav.server_config_files')}
	description={t('settings.page.advanced_desc')}
>
	<AdvancedTab />
</SettingsPageFrame>
