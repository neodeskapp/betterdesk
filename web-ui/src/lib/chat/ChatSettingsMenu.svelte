<!--
  The chat's notification settings (A242), per account and kept on the
  server: browser notifications (the browser asks for its permission when
  they are turned on), a short sound, and a mail when a person waits for
  an answer. Accounts with server.config also set when that mail goes out,
  whether it quotes the message, and whether computers may open the chat
  at all ("Chat with computers", A259) in ChatServerDialog.
-->
<script lang="ts">
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import { can } from '#lib/state/session.svelte.ts';
	import MenuButton from '#lib/ui/MenuButton.svelte';
	import MenuDivider from '#lib/ui/MenuDivider.svelte';
	import MenuItem from '#lib/ui/MenuItem.svelte';
	import { takeIntent } from '#lib/shell/intent.svelte.ts';
	import { toast } from '#lib/ui/toasts.svelte.ts';
	import * as api from './api.ts';
	import ChatServerDialog from './ChatServerDialog.svelte';
	import { chat } from './client.svelte.ts';
	import type { AccountSettings } from './types.ts';

	const permission = $derived(typeof Notification === 'undefined' ? 'unsupported' : Notification.permission);

	async function set(change: Partial<AccountSettings>) {
		const next = { ...chat.settings, ...change };
		if (change.browser && typeof Notification !== 'undefined' && Notification.permission === 'default') {
			try {
				await Notification.requestPermission();
			} catch {
				/* the browser said no */
			}
		}
		chat.settings = next;
		try {
			await api.saveMySettings(next);
		} catch {
			toast.error(t('chat.action_failed'));
		}
	}

	let editing = $state(false);

	// The top bar search's "Chat settings for this server" opens the dialog
	$effect(() => {
		if (takeIntent('chat-settings') && can('server.config')) editing = true;
	});
</script>

<MenuButton kebab label={t('chat.notifications')} align="end">
	<MenuItem
		type="checkbox"
		checked={chat.settings.browser}
		onselect={() => void set({ browser: !chat.settings.browser })}>{t('chat.notify_browser')}</MenuItem
	>
	<MenuItem
		type="checkbox"
		checked={chat.settings.sound}
		onselect={() => void set({ sound: !chat.settings.sound })}>{t('chat.notify_sound')}</MenuItem
	>
	<MenuItem
		type="checkbox"
		checked={chat.settings.mail}
		onselect={() => void set({ mail: !chat.settings.mail })}>{t('chat.notify_mail')}</MenuItem
	>
	{#if chat.settings.browser && permission === 'denied'}
		<MenuItem disabled>{t('chat.notify_blocked')}</MenuItem>
	{/if}
	{#if can('server.config')}
		<MenuDivider />
		<MenuItem icon="settings" onselect={() => (editing = true)}>{t('chat.server_settings')}</MenuItem>
	{/if}
</MenuButton>

{#if editing}
	<ChatServerDialog onclose={() => (editing = false)} />
{/if}
