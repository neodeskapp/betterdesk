<!--
  Chat settings for this server (server.config): the mail to a person who
  waits for an answer, whether it quotes the message, and whether
  computers may open the chat at all ("Chat with computers", A259).
  The settings pattern in a dialog with its own Save (PATTERN.md rules 4,
  6, 7 and 11): the waiting mail is a switch plus its minutes (the server
  stores 0 for no mail); quoting says it has no effect while no mail goes
  out; one line says whom the settings reach; a wrong number is named at
  its field; the toast names what was saved.
-->
<script lang="ts">
	import { untrack } from 'svelte';
	import { t, tf } from '#lib/i18n/i18n.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import Dialog from '#lib/ui/Dialog.svelte';
	import Input from '#lib/ui/Input.svelte';
	import States from '#lib/ui/States.svelte';
	import SettingRow from '#lib/ui/settings/SettingRow.svelte';
	import { SettingsDraft, saveAndConfirm } from '#lib/ui/settings/draft.svelte.ts';
	import * as api from './api.ts';
	import { chat } from './client.svelte.ts';

	let { onclose }: { onclose: () => void } = $props();

	type Values = { mail: boolean; minutes: number; preview: boolean; computers: boolean };

	const draft = new SettingsDraft<Values>(
		{ mail: true, minutes: 15, preview: true, computers: true },
		{
			mail: { label: tf('chat.mail_waiting', 'Mail when a person waits for an answer') },
			minutes: { label: tf('chat.mail_minutes', 'Minutes to wait') },
			preview: { label: t('chat.mail_preview') },
			computers: { label: t('chat.computers') }
		}
	);

	let loaded = $state(false);
	let minutesText = $state('15');
	let minutesError = $state('');

	async function load() {
		try {
			const s = await api.loadServerSettings();
			const n = s.mail_after_minutes;
			draft.load({ mail: n > 0, minutes: n > 0 ? n : 15, preview: s.mail_preview, computers: s.computers });
		} catch {
			// The defaults the server uses until something is saved
		}
		minutesText = String(draft.values.minutes);
		loaded = true;
	}

	$effect(() => {
		untrack(() => void load());
	});

	function setMinutes(text: string) {
		minutesText = text;
		minutesError = '';
		const n = Number(text);
		if (text.trim() !== '' && Number.isInteger(n)) draft.set('minutes', n);
	}

	async function save() {
		const n = Number(minutesText);
		if (draft.values.mail && (minutesText.trim() === '' || !Number.isInteger(n) || n < 1 || n > 10080)) {
			minutesError = tf('chat.mail_minutes_invalid', 'Enter a number of minutes from 1 to 10080.');
			return;
		}
		const ok = await saveAndConfirm(draft, () =>
			api.saveServerSettings({
				mail_after_minutes: draft.values.mail ? n : 0,
				mail_preview: draft.values.preview,
				computers: draft.values.computers
			})
		);
		if (ok) {
			chat.listVersion++;
			onclose();
		}
	}
</script>

<Dialog open title={t('chat.server_settings')} beforeclose={() => !draft.busy} {onclose}>
	{#if !loaded}
		<States kind="loading" />
	{:else}
		<p class="cs-effect">
			{tf('chat.server_settings_effect', 'These apply to everyone on this server, from the next message.')}
		</p>
		<div class="cs-rows">
			<SettingRow
				id="chat-mail-after"
				label={tf('chat.mail_waiting', 'Mail when a person waits for an answer')}
				help={tf(
					'chat.mail_waiting_hint',
					'The mail goes to whoever has the conversation, else to everyone who may assign it, sees the computer and has chat mails on.'
				)}
				checked={draft.values.mail}
				changed={draft.isChanged('mail')}
				onchange={(on) => draft.set('mail', on)}
			/>
			{#if draft.values.mail}
				<SettingRow
					id="chat-mail-minutes"
					label={tf('chat.mail_minutes', 'Minutes to wait')}
					help={tf(
						'chat.mail_minutes_hint',
						'How long a message waits for an answer before the mail goes out.'
					)}
					changed={draft.isChanged('minutes')}
				>
					{#snippet control(f)}
						<Input
							id={f.id}
							type="number"
							width="short"
							min="1"
							max="10080"
							step="1"
							value={minutesText}
							invalid={!!minutesError}
							describedby={[f.describedby, minutesError && 'chat-mail-minutes-error']
								.filter(Boolean)
								.join(' ') || undefined}
							oninput={(e) => setMinutes((e.currentTarget as HTMLInputElement).value)}
						/>
						{#if minutesError}<p class="cs-error" id="chat-mail-minutes-error">{minutesError}</p>{/if}
					{/snippet}
				</SettingRow>
			{/if}
			<SettingRow
				id="chat-mail-preview"
				label={t('chat.mail_preview')}
				checked={draft.values.preview}
				changed={draft.isChanged('preview')}
				onchange={(on) => draft.set('preview', on)}
				noEffect={draft.values.mail
					? undefined
					: tf('chat.mail_preview_no_effect', 'Has no effect while no mail is sent.')}
			/>
			<SettingRow
				id="chat-computers"
				label={t('chat.computers')}
				help={t('chat.computers_hint')}
				checked={draft.values.computers}
				changed={draft.isChanged('computers')}
				onchange={(on) => draft.set('computers', on)}
			/>
		</div>
		{#if draft.error}<p class="cs-error" role="alert">{draft.error}</p>{/if}
	{/if}
	{#snippet footer()}
		<Button onclick={onclose}>{t('actions.cancel')}</Button>
		<Button variant="primary" busy={draft.busy} disabled={!loaded} onclick={() => void save()}
			>{t('actions.save')}</Button
		>
	{/snippet}
</Dialog>

<style>
	.cs-effect {
		margin: 0 0 var(--space-3);
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	.cs-rows {
		margin: 0 calc(-1 * var(--row-pad-x));
		border-top: 1px solid var(--rule);
	}
	.cs-error {
		margin: var(--space-1) 0 0;
		color: var(--danger);
		font-size: var(--text-sm);
	}
</style>
