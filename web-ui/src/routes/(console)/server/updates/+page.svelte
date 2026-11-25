<!--
  Server › Updates (/server/updates, server.config): check for a newer
  version, install it, the update channel. Settings › Updates before
  (/settings?tab=updates leads here); the backups made before updates are
  on Server › Backups.
-->
<script lang="ts">
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import SettingsPageFrame from '#lib/settings/SettingsPageFrame.svelte';
	import UpdatesTab from '#lib/settings/tabs/UpdatesTab.svelte';
</script>

<SettingsPageFrame
	id="server-updates"
	title={t('updates.tab_title')}
	description={t('settings.page.updates_desc')}
>
	<UpdatesTab />
</SettingsPageFrame>
