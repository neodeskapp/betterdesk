<!--
  What the shell mounts once for the chat (A242): it starts the tab's chat
  for accounts with Support chat or Team chat and stops it at sign-out,
  words the browser notifications, shows a notice when a colleague takes
  one of my conversations, and holds the chat panel.
-->
<script lang="ts">
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import { can, session } from '#lib/state/session.svelte.ts';
	import { toast } from '#lib/ui/toasts.svelte.ts';
	import ChatPanel from './ChatPanel.svelte';
	import { chat } from './client.svelte.ts';
	import { openChat } from './panel.svelte.ts';

	const allowed = $derived(!!session.user && (can('chat.support') || can('chat.team')));

	$effect(() => {
		if (!allowed) return;
		chat.start({
			notification: (msg) => {
				const body = msg.text
					? msg.text.slice(0, 120)
					: msg.attachments.length
						? t('chat.files_count', { count: msg.attachments.length })
						: '';
				return { title: t('chat.notification_title', { name: msg.from.name || '?' }), body };
			},
			open: (conv) => openChat(conv)
		});
		return () => chat.stop();
	});

	let shown = 0;
	$effect(() => {
		const list = chat.notices;
		for (const n of list.slice(shown)) {
			if (n.code === 'taken_over') toast.info(t('chat.taken_over', { name: n.by }));
		}
		shown = list.length;
	});
</script>

{#if allowed}<ChatPanel />{/if}
