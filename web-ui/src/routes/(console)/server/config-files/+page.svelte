<!--
  Server › Configuration files (/server/config-files, server.config): the
  server's configuration files with an editor and the restart they may
  need. Settings › Advanced before (/settings?tab=advanced leads here).
  The one settings template (SettingsPage) at full width; an editor, not a
  settings form, so no page-wide save line: the file group says once what
  Save file does.
-->
<script lang="ts">
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import AdvancedTab from '#lib/settings/tabs/AdvancedTab.svelte';
	import SettingsPage from '#lib/ui/settings/SettingsPage.svelte';
</script>

<SettingsPage
	id="server-config-files"
	wide
	saveModel="none"
	title={t('nav.server_config_files')}
	description={t('settings.page.advanced_desc')}
>
	<AdvancedTab />
</SettingsPage>
