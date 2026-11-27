<!--
  Settings › Email, below the mail server: who the mails say they are from
  and who runs this server (their footer), a preview of every mail the
  console sends with the branding of now, in light and dark and in the
  language picked, with "Send me this mail", and the addresses that
  turned a notification off with the link in the mail.
-->
<script lang="ts">
	import { ApiError } from '#lib/api/client.ts';
	import {
		errorText,
		getMailIdentity,
		getMailKinds,
		getMailPreview,
		getUnsubscribed,
		resubscribe,
		saveMailIdentity,
		sendMailPreview,
		type MailIdentity,
		type MailPreview
	} from '#lib/api/settings-system.ts';
	import { t, tf } from '#lib/i18n/i18n.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import Field from '#lib/ui/Field.svelte';
	import Input from '#lib/ui/Input.svelte';
	import SegmentedFilter from '#lib/ui/SegmentedFilter.svelte';
	import Select from '#lib/ui/Select.svelte';
	import States from '#lib/ui/States.svelte';
	import Status from '#lib/ui/Status.svelte';
	import SettingsGroup from '#lib/ui/settings/SettingsGroup.svelte';
	import { emailProblem } from '../email.ts';

	// ── Sender and footer ──

	const EMPTY: MailIdentity = { from_name: '', organisation: '', support_email: '', support_url: '' };
	let identity = $state<MailIdentity>({ ...EMPTY });
	let defaultFrom = $state('NeoDesk');
	let identityError = $state('');
	let refusedField = $state<{ field: string; message: string } | null>(null);
	let saving = $state(false);
	let saved = $state(false);

	const urlProblem = (raw: string) =>
		!raw.trim() || /^https?:\/\/[^\s"'<>]+$/i.test(raw.trim())
			? ''
			: tf('settings.email.err_identity_url', 'Enter a web address that starts with https:// or http://.');
	const fieldError = (field: keyof MailIdentity) =>
		(refusedField?.field === field ? refusedField.message : '') ||
		(field === 'support_email' ? emailProblem(identity.support_email ?? '') : '') ||
		(field === 'support_url' ? urlProblem(identity.support_url ?? '') : '');

	async function loadIdentity() {
		try {
			const a = await getMailIdentity();
			identity = { ...EMPTY, ...a.identity };
			defaultFrom = a.default_from_name || 'NeoDesk';
			identityError = '';
		} catch (err) {
			identityError = errorText(err, t('errors.server_error'));
		}
	}

	async function saveIdentity() {
		if (saving || fieldError('support_email') || fieldError('support_url')) return;
		saving = true;
		saved = false;
		refusedField = null;
		try {
			const a = await saveMailIdentity($state.snapshot(identity));
			identity = { ...EMPTY, ...a.identity };
			defaultFrom = a.default_from_name || defaultFrom;
			saved = true;
			void loadPreview();
		} catch (err) {
			const field = err instanceof ApiError ? (err.data as { field?: unknown } | null)?.field : undefined;
			refusedField = {
				field: typeof field === 'string' ? field : '',
				message: errorText(err, t('errors.server_error'))
			};
		} finally {
			saving = false;
		}
	}

	// ── Previews ──

	let kinds = $state.raw<{ value: string; label: string }[]>([]);
	let languages = $state.raw<{ value: string; label: string }[]>([]);
	let kind = $state('invite');
	let lang = $state('en');
	let scheme = $state<'light' | 'dark'>('light');
	let preview = $state.raw<MailPreview | null>(null);
	let previewError = $state('');
	let sending = $state(false);
	let sendResult = $state<{ ok: boolean; message: string } | null>(null);
	let previewAbort: AbortController | null = null;

	async function loadKinds() {
		try {
			const k = await getMailKinds();
			kinds = k.kinds.map((x) => ({ value: x.kind, label: x.title || x.kind }));
			languages = (k.languages ?? []).map((l) => ({ value: l.code, label: l.name || l.code }));
			if (k.default_language && languages.some((l) => l.value === k.default_language))
				lang = k.default_language;
			await loadPreview();
		} catch (err) {
			previewError = errorText(err, t('errors.server_error'));
		}
	}

	async function loadPreview() {
		previewAbort?.abort();
		const ctl = new AbortController();
		previewAbort = ctl;
		try {
			preview = await getMailPreview(kind, lang, scheme, ctl.signal);
			previewError = '';
		} catch (err) {
			if (ctl.signal.aborted) return;
			previewError = errorText(err, t('errors.server_error'));
		}
	}

	async function sendMe() {
		sending = true;
		sendResult = null;
		try {
			const r = await sendMailPreview(kind, lang);
			sendResult = {
				ok: true,
				message: tf('settings.email.preview_sent', 'Sent to {email}.', { email: r.to ?? '' })
			};
		} catch (err) {
			sendResult = { ok: false, message: errorText(err, t('settings.email.preview_failed')) };
		} finally {
			sending = false;
		}
	}

	// ── Turned off by the reader ──

	let unsubscribed = $state.raw<Record<string, string[]>>({});
	const UNSUB_KINDS = ['chat', 'alert', 'help'] as const;
	const unsubLabel = (k: string) => t(`settings.email.unsubscribed_${k}`);
	const anyUnsubscribed = $derived(UNSUB_KINDS.some((k) => (unsubscribed[k] ?? []).length > 0));

	async function loadUnsubscribed() {
		try {
			unsubscribed = (await getUnsubscribed()).unsubscribed;
		} catch {
			unsubscribed = {};
		}
	}

	async function turnOn(k: string, address: string) {
		try {
			unsubscribed = (await resubscribe(k, address)).unsubscribed;
		} catch {
			/* the list stays as it was */
		}
	}

	$effect(() => {
		void loadIdentity();
		void loadKinds();
		void loadUnsubscribed();
	});

	const title = $derived(kinds.find((k) => k.value === kind)?.label ?? kind);
</script>

<SettingsGroup
	id="email-identity"
	title={t('settings.email.identity_title')}
	description={t('settings.email.identity_note')}
>
	{#if identityError}
		<States kind="error" message={identityError} onretry={() => void loadIdentity()} />
	{:else}
		<div class="mails-form">
			<Field
				id="email-from-name"
				label={t('settings.email.from_name')}
				hint={tf('settings.email.from_name_hint', 'Shown as the sender in the inbox. Empty: {default}.', {
					default: defaultFrom
				})}
				error={fieldError('from_name')}
			>
				{#snippet children(f)}
					<Input {...f} placeholder={defaultFrom} bind:value={identity.from_name} />
				{/snippet}
			</Field>
			<Field
				id="email-organisation"
				label={t('settings.email.organisation')}
				hint={t('settings.email.organisation_hint')}
				error={fieldError('organisation')}
			>
				{#snippet children(f)}
					<Input {...f} bind:value={identity.organisation} />
				{/snippet}
			</Field>
			<Field
				id="email-support-email"
				label={t('settings.email.support_email')}
				hint={t('settings.email.support_email_hint')}
				error={fieldError('support_email')}
			>
				{#snippet children(f)}
					<Input
						{...f}
						type="email"
						inputmode="email"
						spellcheck={false}
						bind:value={identity.support_email}
					/>
				{/snippet}
			</Field>
			<Field
				id="email-support-url"
				label={t('settings.email.support_url')}
				hint={t('settings.email.support_url_hint')}
				error={fieldError('support_url')}
			>
				{#snippet children(f)}
					<Input {...f} type="url" inputmode="url" spellcheck={false} bind:value={identity.support_url} />
				{/snippet}
			</Field>
			<div class="mails-actions">
				<Button id="email-identity-save" variant="primary" busy={saving} onclick={saveIdentity}
					>{t('settings.email.identity_save')}</Button
				>
				<span role="status">
					{#if saved}<Status tone="good" label={t('settings.email.identity_saved')} />{/if}
					{#if refusedField && !refusedField.field}<Status tone="danger" label={refusedField.message} />{/if}
				</span>
			</div>
		</div>
	{/if}
</SettingsGroup>

<SettingsGroup
	id="email-previews"
	title={t('settings.email.previews_title')}
	description={t('settings.email.previews_note')}
>
	<!-- Which mail to show: no setting, so no row in the settings list -->
	<div class="mails-pickers" data-search-skip>
		<Field id="email-preview-kind" label={t('settings.email.preview_mail')}>
			{#snippet children(f)}
				<Select {...f} options={kinds} bind:value={kind} onchange={() => void loadPreview()} />
			{/snippet}
		</Field>
		<Field id="email-preview-lang" label={t('settings.email.preview_language')}>
			{#snippet children(f)}
				<Select {...f} options={languages} bind:value={lang} onchange={() => void loadPreview()} />
			{/snippet}
		</Field>
		<SegmentedFilter
			label={t('settings.email.previews_title')}
			options={[
				{ value: 'light', label: t('settings.email.preview_light') },
				{ value: 'dark', label: t('settings.email.preview_dark') }
			]}
			bind:value={scheme}
			onchange={() => void loadPreview()}
		/>
	</div>
	{#if previewError}
		<States kind="error" message={previewError} onretry={() => void loadPreview()} />
	{:else if !preview}
		<States kind="loading" />
	{:else}
		<p class="mails-subject">
			<strong>{preview.from}</strong>
			<span
				>{tf('settings.email.preview_subject', 'Subject: {subject}', {
					subject: preview.subject ?? ''
				})}</span
			>
			<span class="mails-pre">{preview.preheader}</span>
		</p>
		<!-- The mail's own page, without scripts and on its own origin -->
		<iframe
			class="mails-frame"
			title={tf('settings.email.preview_frame', 'Preview of the mail {mail}', { mail: title })}
			sandbox=""
			srcdoc={preview.html}
		></iframe>
		<div class="mails-actions">
			<Button id="email-preview-send" busy={sending} onclick={sendMe}
				>{t('settings.email.preview_send')}</Button
			>
			<span role="status">
				{#if sendResult}<Status tone={sendResult.ok ? 'good' : 'danger'} label={sendResult.message} />{/if}
			</span>
		</div>
	{/if}
</SettingsGroup>

<SettingsGroup
	id="email-unsubscribed"
	title={t('settings.email.unsubscribed_title')}
	description={t('settings.email.unsubscribed_note')}
>
	{#if !anyUnsubscribed}
		<p class="hint">{t('settings.email.unsubscribed_none')}</p>
	{:else}
		<ul class="mails-unsub">
			{#each UNSUB_KINDS as k (k)}
				{#each unsubscribed[k] ?? [] as address (address)}
					<li>
						<span>{unsubLabel(k)}</span>
						<span class="mono">{address}</span>
						<Button
							size="sm"
							aria-label={tf('settings.email.resubscribe_label', 'Turn {kind} mails to {address} on again', {
								kind: unsubLabel(k),
								address
							})}
							onclick={() => void turnOn(k, address)}>{t('settings.email.resubscribe')}</Button
						>
					</li>
				{/each}
			{/each}
		</ul>
	{/if}
</SettingsGroup>

<style>
	.mails-form {
		display: grid;
		gap: var(--space-4);
		max-width: 560px;
	}
	.mails-pickers {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-3) var(--space-4);
		align-items: end;
		margin-bottom: var(--space-4);
	}
	.mails-actions {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2) var(--space-3);
		align-items: center;
		margin-top: var(--space-3);
	}
	.mails-subject {
		display: flex;
		flex-direction: column;
		gap: var(--space-1);
		margin: 0 0 var(--space-3);
		overflow-wrap: anywhere;
	}
	.mails-pre,
	.hint {
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	.mails-frame {
		display: block;
		width: 100%;
		max-width: 680px;
		height: 720px;
		border: 1px solid var(--border);
		border-radius: 4px;
	}
	.mails-unsub {
		list-style: none;
		margin: 0;
		padding: 0;
	}
	.mails-unsub li {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2) var(--space-3);
		align-items: center;
		padding: var(--space-2) 0;
		border-top: 1px solid var(--border);
	}
</style>
