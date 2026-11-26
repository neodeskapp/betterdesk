<!--
  Server › Updates (/server/updates, server.config): check for a newer
  version, install it, the update channel. Settings › Updates before
  (/settings?tab=updates leads here); the backups made before updates are
  on Server › Backups. The one settings template (SettingsPage): the
  channel is saved with the save bar, Check and Install are actions.
-->
<script lang="ts">
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import UpdatesTab from '#lib/settings/tabs/UpdatesTab.svelte';
	import SettingsPage from '#lib/ui/settings/SettingsPage.svelte';
</script>

<SettingsPage
	id="server-updates"
	title={t('updates.tab_title')}
	description={t('settings.page.updates_desc')}
>
	<UpdatesTab />
</SettingsPage>
