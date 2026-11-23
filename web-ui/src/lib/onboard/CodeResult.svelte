<!--
  A new invite or quick support link, shown once (the server keeps only a
  fingerprint): the link with Copy, the short code people type into a
  NeoDesk that is already installed, the QR code of the link, and Send
  by email when the console can send mail. A warning when the console is
  not on HTTPS: the person's computer then trusts whatever answers on the
  network.
-->
<script lang="ts">
	import { ApiError } from '#lib/api/client.ts';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import Field from '#lib/ui/Field.svelte';
	import FormStatus from '#lib/ui/FormStatus.svelte';
	import Input from '#lib/ui/Input.svelte';
	import Notice from '#lib/ui/Notice.svelte';
	import QR from '#lib/ui/QR.svelte';
	import ValueField from '#lib/ui/ValueField.svelte';
	import { ICONS } from '#lib/util/display.ts';

	interface Props {
		link: string;
		/** "Invite link" or "Session link" */
		linkLabel?: string;
		displayCode: string;
		/** The console is on HTTPS */
		https: boolean;
		/** The console can send mail: Send by email is offered */
		mail: boolean;
		/** The address the form was filled with */
		email?: string;
		/** Already mailed when it was made */
		mailed?: boolean;
		/** Why the mail asked for when it was made did not go out (an error code) */
		mailError?: string;
		/** The short code is for typing into an installed NeoDesk (invites) */
		showCode?: boolean;
		onmail: (to: string) => Promise<void>;
	}

	let {
		link,
		linkLabel = t('onboard.result.link'),
		displayCode,
		https,
		mail,
		email = '',
		mailed = false,
		mailError = '',
		showCode = true,
		onmail
	}: Props = $props();

	const uid = $props.id();
	let to = $state('');
	let toTouched = false;
	let sending = $state(false);
	let status = $state<{ message: string; tone: 'neutral' | 'ok' | 'error' }>({
		message: '',
		tone: 'neutral'
	});

	$effect(() => {
		if (!toTouched) to = email;
	});
	$effect(() => {
		if (mailed) status = { message: t('onboard.result.mailed', { to: email }), tone: 'ok' };
		else if (mailError) status = { message: t(`onboard.errors.${mailError}`), tone: 'error' };
	});

	async function send(e: SubmitEvent) {
		e.preventDefault();
		const address = to.trim();
		if (!address || sending) return;
		sending = true;
		status = { message: t('onboard.result.sending'), tone: 'neutral' };
		try {
			await onmail(address);
			status = { message: t('onboard.result.mailed', { to: address }), tone: 'ok' };
		} catch (err) {
			const code = err instanceof ApiError ? err.code : null;
			status = {
				message: code ? t(`onboard.errors.${code}`) : t('onboard.result.mail_failed'),
				tone: 'error'
			};
		} finally {
			sending = false;
		}
	}
</script>

<div class="code-result" aria-live="polite">
	{#if !https}
		<Notice tone="warning" class="insecure" message={t('onboard.result.insecure')} />
	{/if}
	<div class="block">
		<p class="label">{linkLabel}</p>
		<ValueField value={link} label={linkLabel} />
	</div>
	<div class="link-row">
		{#if showCode}
			<div class="block code-block">
				<p class="label">{t('onboard.result.code')}</p>
				<p class="code" dir="ltr">{displayCode}</p>
				<p class="hint">{t('onboard.result.code_hint')}</p>
			</div>
		{/if}
		<figure class="qr">
			<QR text={link} label={t('onboard.result.qr')} size={144} />
			<figcaption class="hint">{t('onboard.result.qr_caption')}</figcaption>
		</figure>
	</div>
	{#if mail}
		<form class="mail-form" onsubmit={send} novalidate>
			<Field label={t('onboard.result.mail_to')}>
				{#snippet children(f)}
					<Input
						{...f}
						id="{uid}-to"
						type="email"
						width="host"
						autocomplete="off"
						bind:value={to}
						oninput={() => (toTouched = true)}
					/>
				{/snippet}
			</Field>
			<Button type="submit" icon={ICONS.send} busy={sending} disabled={!to.trim()}
				>{t('onboard.result.send')}</Button
			>
		</form>
	{/if}
	<FormStatus message={status.message} tone={status.tone} />
</div>

<style>
	.code-result {
		display: flex;
		flex-direction: column;
		gap: var(--space-3);
	}
	.block {
		display: flex;
		flex-direction: column;
		gap: var(--space-1);
		min-width: 0;
	}
	.code-block {
		flex: 1 1 14rem;
	}
	.link-row {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-4);
		align-items: flex-start;
	}
	.label,
	.hint {
		margin: 0;
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	.code {
		margin: 0;
		font-family: var(--font-mono, monospace);
		font-size: 1.75rem;
		font-weight: 600;
		letter-spacing: 0.12em;
	}
	.qr {
		display: flex;
		flex: 0 1 12rem;
		flex-direction: column;
		gap: var(--space-1);
		margin: 0;
	}
	.mail-form {
		display: flex;
		flex-wrap: wrap;
		align-items: flex-end;
		gap: var(--space-2) var(--space-3);
	}
	.mail-form :global(.form-group) {
		margin-bottom: 0;
	}
</style>
