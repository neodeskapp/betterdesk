<!--
  Your account: your own password and two-factor sign-in (Settings ›
  Sign-in before; the old #account-password and #auth-panel-totp links
  lead here, lib/redirects.ts). A directory or single sign-on account has
  no password of the console's: it gets a note instead of the form, and
  turns two-factor sign-in off with a current code.

  Element ids: #password and #two-factor are the parts; the fields keep
  their old ids (current-password, new-password, confirm-password) for
  links and the settings search.
-->
<script lang="ts">
	import { untrack } from 'svelte';
	import {
		changePassword,
		disableTotp,
		enableTotp,
		getTotpStatus,
		renewRecoveryCodes,
		setupTotp
	} from '#lib/api/settings.ts';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import { passwordRules } from '#lib/settings/signin.ts';
	import { trackDirty } from '#lib/settings/dirty.ts';
	import Button from '#lib/ui/Button.svelte';
	import CodeBlock from '#lib/ui/CodeBlock.svelte';
	import Field from '#lib/ui/Field.svelte';
	import FormStatus from '#lib/ui/FormStatus.svelte';
	import Input from '#lib/ui/Input.svelte';
	import Notice from '#lib/ui/Notice.svelte';
	import SaveBar from '#lib/ui/SaveBar.svelte';
	import States from '#lib/ui/States.svelte';
	import Status from '#lib/ui/Status.svelte';
	import Steps from '#lib/ui/Steps.svelte';
	import ValueField from '#lib/ui/ValueField.svelte';
	import { createForm } from '#lib/ui/form.svelte.ts';

	const errText = (err: unknown, fallback = t('errors.server_error')) =>
		err instanceof Error && err.message ? err.message : fallback;
	type Line = { message: string; tone: 'neutral' | 'ok' | 'error' };
	const NO_LINE: Line = { message: '', tone: 'neutral' };

	/* ── Your password ── */

	const password = createForm({
		initial: { current: '', next: '', confirm: '' },
		validate: (v) => {
			const fill = t('settings.fill_all_fields');
			if (!v.current || !v.next || !v.confirm) {
				return {
					current: v.current ? undefined : fill,
					next: v.next ? undefined : fill,
					confirm: v.confirm ? undefined : fill
				};
			}
			if (!passwordRules(v.next).every((r) => r.met))
				return { next: t('settings.password_requirements_not_met') };
			if (v.next !== v.confirm) return { confirm: t('settings.passwords_not_match') };
			return null;
		},
		submit: async (v) => {
			await changePassword(v.current, v.next, v.confirm);
			return { current: '', next: '', confirm: '' };
		},
		savedMessage: t('settings.password_changed')
	});
	const rules = $derived(passwordRules(password.values.next));
	const RULE_LABEL: Record<string, string> = {
		length: 'settings.req_length'
	};

	/* ── Two-factor sign-in ── */

	type TotpView = 'loading' | 'error' | 'off' | 'on' | 'setup' | 'codes' | 'disable' | 'renew';
	let totpView = $state<TotpView>('loading');
	let totpOn = $state<boolean | null>(null);
	/**
	 * Turning it off asks the password, or a current code where the account
	 * has none here: a directory or single sign-on account, which has no
	 * password to change here either
	 */
	let disableWithCode = $state(false);
	let totpError = $state('');
	let totpNote = $state<Line>(NO_LINE);
	let setup = $state.raw<{ qrCode: string; secret: string } | null>(null);
	let code = $state('');
	let codeStatus = $state<Line>(NO_LINE);
	let codes = $state.raw<string[]>([]);
	/** The codes on show are new ones for an account that had two-factor sign-in already */
	let codesRenewed = $state(false);
	let disablePassword = $state('');
	let disableStatus = $state<Line>(NO_LINE);
	let totpBusy = $state(false);
	let codeEl = $state<HTMLInputElement | null>(null);
	let disableEl = $state<HTMLInputElement | null>(null);
	let setupBtn = $state<HTMLElement | null>(null);

	async function loadTotp() {
		totpView = 'loading';
		try {
			const status = await getTotpStatus();
			totpOn = status.enabled;
			disableWithCode = status.disableWith === 'code';
			totpView = totpOn ? 'on' : 'off';
		} catch (err) {
			totpError = errText(err);
			totpView = 'error';
		}
	}

	// The QR code comes as a data URL from the server; anything else is not shown
	const qrSrc = $derived(
		setup && /^data:image\/(png|svg\+xml|jpeg|gif);base64,/i.test(setup.qrCode) ? setup.qrCode : ''
	);

	async function startSetup() {
		totpNote = NO_LINE;
		totpBusy = true;
		try {
			const s = await setupTotp();
			setup = { qrCode: s.qrCode ?? '', secret: s.secret ?? '' };
			code = '';
			codeStatus = NO_LINE;
			totpView = 'setup';
			await Promise.resolve();
			queueMicrotask(() => codeEl?.focus());
		} catch (err) {
			totpNote = { message: errText(err), tone: 'error' };
		} finally {
			totpBusy = false;
		}
	}

	async function verifyCode(e: SubmitEvent) {
		e.preventDefault();
		if (totpBusy) return;
		const c = code.trim();
		if (c.length !== 6) {
			codeStatus = { message: t('auth.totp_enter_code'), tone: 'error' };
			codeEl?.focus();
			return;
		}
		totpBusy = true;
		try {
			codes = await enableTotp(c);
			totpOn = true;
			codesRenewed = false;
			totpView = 'codes';
		} catch (err) {
			codeStatus = { message: errText(err, t('auth.totp_invalid_code')), tone: 'error' };
		} finally {
			totpBusy = false;
		}
	}

	function startRenew() {
		code = '';
		codeStatus = NO_LINE;
		totpNote = NO_LINE;
		totpView = 'renew';
		queueMicrotask(() => codeEl?.focus());
	}

	async function confirmRenew(e: SubmitEvent) {
		e.preventDefault();
		if (totpBusy) return;
		const c = code.trim();
		if (c.length !== 6) {
			codeStatus = { message: t('auth.totp_enter_code'), tone: 'error' };
			codeEl?.focus();
			return;
		}
		totpBusy = true;
		try {
			codes = await renewRecoveryCodes(c);
			codesRenewed = true;
			totpView = 'codes';
		} catch (err) {
			codeStatus = { message: errText(err, t('auth.totp_invalid_code')), tone: 'error' };
		} finally {
			totpBusy = false;
		}
	}

	function startDisable() {
		disablePassword = '';
		disableStatus = NO_LINE;
		totpNote = NO_LINE;
		totpView = 'disable';
		queueMicrotask(() => disableEl?.focus());
	}

	async function confirmDisable(e: SubmitEvent) {
		e.preventDefault();
		if (totpBusy) return;
		if (!disablePassword) {
			disableStatus = { message: t('auth.fill_all_fields'), tone: 'error' };
			disableEl?.focus();
			return;
		}
		totpBusy = true;
		try {
			await disableTotp(disableWithCode ? { code: disablePassword.trim() } : { password: disablePassword });
			disablePassword = '';
			await loadTotp();
			totpNote = { message: t('settings.totp_disabled_success'), tone: 'ok' };
		} catch (err) {
			disableStatus = { message: errText(err), tone: 'error' };
		} finally {
			totpBusy = false;
		}
	}

	function cancelTotp() {
		setup = null;
		void loadTotp().then(() => queueMicrotask(() => setupBtn?.focus()));
	}

	$effect(() =>
		trackDirty(
			() => password.dirty,
			() => password.busy
		)
	);

	$effect(() => {
		untrack(() => void loadTotp());
	});
</script>

<section class="section" id="password" aria-labelledby="password-title">
	<h2 id="password-title">{t('account.password')}</h2>
	{#if disableWithCode}
		<!-- A directory or single sign-on account has no password of the console's -->
		<p class="form-hint">{t('settings.signin.password_external')}</p>
	{:else}
		<form id={password.id} class="form-column settings-subgroup" onsubmit={password.onsubmit} novalidate>
			<Field id="current-password" label={t('settings.current_password')} error={password.errors.current}>
				{#snippet children(f)}
					<Input
						{...f}
						type="password"
						autocomplete="current-password"
						bind:value={password.values.current}
					/>
				{/snippet}
			</Field>
			<Field id="new-password" label={t('settings.new_password')} error={password.errors.next}>
				{#snippet children(f)}
					<Input
						{...f}
						describedby={[f.describedby, 'password-requirements'].filter(Boolean).join(' ')}
						type="password"
						autocomplete="new-password"
						minlength={8}
						bind:value={password.values.next}
					/>
					<div class="password-requirements" id="password-requirements">
						<p class="form-hint">{t('settings.password_requirements')}:</p>
						<ul>
							{#each rules as rule (rule.id)}
								<li id="req-{rule.id}" class={{ valid: rule.met }}>{t(RULE_LABEL[rule.id] ?? '')}</li>
							{/each}
						</ul>
					</div>
				{/snippet}
			</Field>
			<Field id="confirm-password" label={t('settings.confirm_password')} error={password.errors.confirm}>
				{#snippet children(f)}
					<Input {...f} type="password" autocomplete="new-password" bind:value={password.values.confirm} />
				{/snippet}
			</Field>
			<p class="field-effect">{t('settings.signin.password_effect')}</p>
			<SaveBar form={password} label={t('settings.save_password')} />
		</form>
	{/if}
</section>

<section class="section" id="two-factor" aria-labelledby="two-factor-title">
	<h2 id="two-factor-title">{t('settings.totp_title')}</h2>
	<p class="form-hint">{t('settings.signin.totp_note')}</p>
	<div class="totp" aria-live="polite">
		{#if totpNote.message}<FormStatus message={totpNote.message} tone={totpNote.tone} />{/if}
		{#if totpView === 'loading'}
			<States kind="loading" />
		{:else if totpView === 'error'}
			<States kind="error" message={totpError} onretry={() => void loadTotp()} />
		{:else if totpView === 'on'}
			<p class="line"><Status tone="good" label={t('settings.signin.totp_on')} /></p>
			<p class="form-hint">{t('settings.totp_enabled_desc')}</p>
			<p class="line inline-controls">
				<Button onclick={startRenew}>{t('settings.signin.recovery_new')}</Button>
				<Button variant="danger-quiet" onclick={startDisable}>{t('settings.signin.totp_turn_off')}</Button>
			</p>
		{:else if totpView === 'off'}
			<p class="line"><Status tone="neutral" label={t('settings.signin.totp_off')} /></p>
			<p class="form-hint">{t('settings.totp_disabled_desc')}</p>
			<p class="line">
				<Button id="totp-setup-btn" bind:element={setupBtn} busy={totpBusy} onclick={() => void startSetup()}
					>{t('settings.signin.totp_set_up')}</Button
				>
			</p>
			<p class="field-effect">{t('settings.signin.totp_effect')}</p>
		{:else if totpView === 'setup' && setup}
			<Steps items={[t('settings.totp_step1'), t('settings.totp_step2'), t('settings.totp_step3')]} />
			{#if qrSrc}<img
					src={qrSrc}
					alt={t('settings.signin.totp_qr_alt')}
					class="totp-qr-image"
					width="200"
					height="200"
				/>{/if}
			<div class="form-group">
				<span class="form-label">{t('settings.totp_manual_key')}</span>
				<ValueField value={setup.secret} label={t('settings.totp_manual_key')} />
			</div>
			<form class="totp-form" onsubmit={verifyCode} novalidate>
				<Field id="totp-setup-code" label={t('settings.totp_enter_code')} width="short">
					{#snippet children(f)}
						<Input
							{...f}
							bind:element={codeEl}
							bind:value={code}
							inputmode="numeric"
							autocomplete="one-time-code"
							oninput={() => (code = code.replace(/[^0-9]/g, '').slice(0, 6))}
						/>
					{/snippet}
				</Field>
				<div class="inline-controls">
					<Button type="submit" variant="primary" busy={totpBusy}>{t('settings.signin.totp_verify')}</Button>
					<Button onclick={cancelTotp}>{t('actions.cancel')}</Button>
					<FormStatus message={codeStatus.message} tone={codeStatus.tone} />
				</div>
			</form>
		{:else if totpView === 'renew'}
			<form class="totp-form" onsubmit={confirmRenew} novalidate>
				<p class="form-hint">{t('settings.signin.recovery_new_hint')}</p>
				<Field id="totp-renew-code" label={t('auth.totp_code')} width="short">
					{#snippet children(f)}
						<Input
							{...f}
							bind:element={codeEl}
							bind:value={code}
							inputmode="numeric"
							autocomplete="one-time-code"
							oninput={() => (code = code.replace(/[^0-9]/g, '').slice(0, 6))}
						/>
					{/snippet}
				</Field>
				<div class="inline-controls">
					<Button type="submit" variant="primary" busy={totpBusy}>{t('settings.signin.recovery_new')}</Button>
					<Button onclick={() => void loadTotp()}>{t('actions.cancel')}</Button>
					<FormStatus message={codeStatus.message} tone={codeStatus.tone} />
				</div>
			</form>
		{:else if totpView === 'codes'}
			<p class="line">
				<Status
					tone="good"
					label={codesRenewed ? t('settings.signin.recovery_renewed') : t('settings.totp_enabled_success')}
				/>
			</p>
			<Notice tone="warning" message={t('settings.totp_recovery_warning')} />
			<div class="totp-recovery-codes">
				<CodeBlock code={codes.join('\n')} title={t('settings.signin.recovery_codes')} />
			</div>
			<p class="line">
				<Button variant="primary" onclick={() => void loadTotp()}
					>{t('settings.signin.recovery_saved')}</Button
				>
			</p>
		{:else if totpView === 'disable'}
			<form class="totp-form" onsubmit={confirmDisable} novalidate>
				<Notice tone="warning" message={t('settings.totp_disable_warning')} />
				{#if disableWithCode}
					<Field id="totp-disable-code" label={t('auth.totp_code')} width="short">
						{#snippet children(f)}
							<Input
								{...f}
								inputmode="numeric"
								autocomplete="one-time-code"
								bind:element={disableEl}
								bind:value={disablePassword}
								oninput={() => (disablePassword = disablePassword.replace(/[^0-9]/g, '').slice(0, 6))}
							/>
						{/snippet}
					</Field>
				{:else}
					<Field id="totp-disable-password" label={t('settings.current_password')}>
						{#snippet children(f)}
							<Input
								{...f}
								type="password"
								autocomplete="current-password"
								bind:element={disableEl}
								bind:value={disablePassword}
							/>
						{/snippet}
					</Field>
				{/if}
				<div class="inline-controls">
					<Button type="submit" variant="danger-quiet" busy={totpBusy}
						>{t('settings.signin.totp_turn_off')}</Button
					>
					<Button onclick={() => void loadTotp()}>{t('actions.cancel')}</Button>
					<FormStatus message={disableStatus.message} tone={disableStatus.tone} />
				</div>
			</form>
		{/if}
	</div>
</section>

<style>
	.form-column {
		max-width: var(--measure);
	}
	.settings-subgroup {
		display: flex;
		flex-direction: column;
		gap: var(--space-3);
		min-width: 0;
	}
	.settings-subgroup > :global(.form-group) {
		margin-bottom: 0;
	}
	.form-hint,
	.field-effect {
		max-width: var(--measure);
		margin: 0;
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	/* Password rules: ticked off as they are met */
	.password-requirements ul {
		margin: var(--space-1) 0 0;
		padding: 0;
		list-style: none;
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	.password-requirements li::before {
		content: '–';
		display: inline-block;
		width: 1.25em;
	}
	.password-requirements li.valid {
		color: var(--success);
	}
	.password-requirements li.valid::before {
		content: '✓';
	}
	.totp {
		display: flex;
		flex-direction: column;
		gap: var(--space-3);
		max-width: var(--measure);
	}
	.totp-form {
		display: flex;
		flex-direction: column;
		gap: var(--space-3);
	}
	.totp-form > :global(.form-group) {
		margin: 0;
	}
	.line {
		margin: 0;
	}
	.totp-qr-image {
		display: block;
		width: 200px;
		height: 200px;
		padding: var(--space-2);
		border: 1px solid var(--rule);
		border-radius: var(--radius);
		background: #ffffff;
	}
	.totp-recovery-codes {
		max-width: 32ch;
	}
	.form-group {
		display: flex;
		flex-direction: column;
		gap: var(--space-1);
	}
	.form-label {
		font-weight: 600;
	}
	.inline-controls {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-2);
	}
</style>
