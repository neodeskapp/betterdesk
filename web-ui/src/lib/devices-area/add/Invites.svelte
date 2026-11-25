<!--
  Devices › Add computers › Invites and install links
  (/devices/add?tab=invites, enrollment.approve): the place for invites to
  one computer and install links for many. Today it opens "Add a
  computer" (lib/onboard/AddComputerDialog.svelte), which makes an invite
  and lists the open ones.

  nd-onboard fills this tab: the list of invites and install links with
  their counters, Create install link, and Join rules. Its parts go where
  the comment in the markup says.
-->
<script lang="ts">
	import { untrack } from 'svelte';
	import { listFolders, type Folder } from '#lib/api/devices.ts';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import AddComputerDialog from '#lib/onboard/AddComputerDialog.svelte';
	import Button from '#lib/ui/Button.svelte';
	import { ICONS } from '#lib/util/display.ts';

	let open = $state(false);
	let folders = $state.raw<Folder[]>([]);

	$effect(() => {
		untrack(() => {
			listFolders()
				.then((f) => (folders = f))
				.catch(() => {
					/* Add a computer works without folders: the field is left out */
				});
		});
	});
</script>

<div class="add-tab invites-page">
	<section class="section" aria-labelledby="invites-add-title">
		<div class="section-head"><h2 id="invites-add-title">{t('devices_add.invite_title')}</h2></div>
		<p class="section-note">{t('devices_add.invite_note')}</p>
		<p>
			<Button variant="primary" icon={ICONS.add} class="act-add-computer" onclick={() => (open = true)}
				>{t('onboard.invite.button')}</Button
			>
		</p>
	</section>
	<!-- nd-onboard: the invites and install links list, Create install link and Join rules go here -->
</div>

{#if open}<AddComputerDialog {folders} onclose={() => (open = false)} />{/if}
