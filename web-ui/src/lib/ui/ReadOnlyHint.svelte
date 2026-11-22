<!--
  The one sentence a page shows when the account may open it but every
  control that changes something is hidden for it. Put it right under the
  page head:

    <ReadOnlyHint writes={['device.edit', 'device.delete']} />

  writes are the permissions of the page's write controls, checked with
  canWrite as the page hides them. writable is true when the page still
  offers a write that is no single permission (a role rule, the section's
  level); pass the page's own flags there. With any write left, or for a
  super admin, nothing shows.
-->
<script lang="ts">
	import { tf } from '#lib/i18n/i18n.svelte.ts';
	import { canWrite, session } from '#lib/state/session.svelte.ts';
	import Notice from './Notice.svelte';

	interface Props {
		writes?: readonly string[];
		writable?: boolean;
	}

	let { writes = [], writable = false }: Props = $props();

	const shown = $derived(!session.isSuperAdmin && !writable && !writes.some((p) => canWrite(p)));
</script>

{#if shown}<Notice
		class="read-only-hint"
		message={tf('common.read_only_page', 'You can view this page but not change it.')}
	/>{/if}
