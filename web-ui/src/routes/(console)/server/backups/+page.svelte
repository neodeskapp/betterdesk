<!--
  Server › Backups (/server/backups, server.config): the backups of the
  whole server (make one, on a schedule, restore) and the backups each
  update made of the files it replaced, as one settings page. Settings ›
  Backup and the backups part of Updates before (/settings?tab=backup
  leads here). The page is lib/settings/tabs/BackupTab.svelte.
-->
<script lang="ts">
	import BackupTab from '#lib/settings/tabs/BackupTab.svelte';
</script>

<BackupTab />
