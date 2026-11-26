<!--
  Server › Backups (/server/backups, server.config): the backups of the
  whole server (make one, on a schedule, restore) and, in the same column
  above the restore, the backups each update made of the files it replaced. Both retention counts
  are on this page: "Keep scheduled backups" and "Keep update backups".
  Settings › Backup and the backups part of Updates before
  (/settings?tab=backup leads here).
-->
<script lang="ts">
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import SettingsPageFrame from '#lib/settings/SettingsPageFrame.svelte';
	import BackupTab from '#lib/settings/tabs/BackupTab.svelte';
	import UpdateBackups from '#lib/settings-b/UpdateBackups.svelte';
</script>

<SettingsPageFrame
	id="server-backups"
	title={t('nav.server_backups')}
	description={t('settings.page.backup_desc')}
>
	<BackupTab>
		{#snippet before()}<div class="update-backups"><UpdateBackups /></div>{/snippet}
	</BackupTab>
</SettingsPageFrame>

<style>
	.update-backups {
		max-width: var(--measure);
	}
</style>
