<!--
  The chat's notification settings (A242), per account and kept on the
  server: browser notifications (the browser asks for its permission when
  they are turned on), a short sound, and a mail when a person waits for
  an answer. Accounts with server.config also set when that mail goes out
  and whether it quotes the message.
-->
<script lang="ts">
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import { can } from '#lib/state/session.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import Checkbox from '#lib/ui/Checkbox.svelte';
	import Dialog from '#lib/ui/Dialog.svelte';
	import Input from '#lib/ui/Input.svelte';
	import MenuButton from '#lib/ui/MenuButton.svelte';
	import MenuDivider from '#lib/ui/MenuDivider.svelte';
	import MenuItem from '#lib/ui/MenuItem.svelte';
	import { toast } from '#lib/ui/toasts.svelte.ts';
	import * as api from './api.ts';
	import { chat } from './client.svelte.ts';
	import type { AccountSettings, ServerSettings } from './types.ts';

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

	let server = $state<ServerSettings | null>(null);
	let editing = $state(false);
	let minutes = $state('15');
	let busy = $state(false);

	async function openServer() {
		editing = true;
		try {
			server = await api.loadServerSettings();
			minutes = String(server.mail_after_minutes);
		} catch {
			server = { mail_after_minutes: 15, mail_preview: true };
		}
	}

	async function saveServer() {
		if (!server) return;
		const n = Number(minutes);
		if (!Number.isInteger(n) || n < 0 || n > 10080) {
			toast.error(t('chat.mail_after_invalid'));
			return;
		}
		busy = true;
		try {
			await api.saveServerSettings({ mail_after_minutes: n, mail_preview: server.mail_preview });
			editing = false;
		} catch (err) {
			toast.error(err instanceof Error ? err.message : t('chat.action_failed'));
		} finally {
			busy = false;
		}
	}
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
		<MenuItem icon="settings" onselect={() => void openServer()}>{t('chat.server_settings')}</MenuItem>
	{/if}
</MenuButton>

{#if editing}
	<Dialog open title={t('chat.server_settings')} onclose={() => (editing = false)}>
		{#if !server}
			<p>{t('common.loading')}</p>
		{:else}
			<label class="cs-label" for="chat-mail-after">{t('chat.mail_after')}</label>
			<Input id="chat-mail-after" type="number" min="0" max="10080" bind:value={minutes} />
			<p class="cs-hint">{t('chat.mail_after_hint')}</p>
			<Checkbox bind:checked={server.mail_preview} label={t('chat.mail_preview')} />
		{/if}
		{#snippet footer()}
			<Button onclick={() => (editing = false)}>{t('actions.cancel')}</Button>
			<Button variant="primary" {busy} onclick={() => void saveServer()}>{t('actions.save')}</Button>
		{/snippet}
	</Dialog>
{/if}

<style>
	.cs-label {
		display: block;
		margin-bottom: var(--space-1);
		font-weight: 600;
	}
	.cs-hint {
		margin: var(--space-1) 0 var(--space-3);
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
</style>
