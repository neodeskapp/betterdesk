<!--
  Saved sign-in of one device (BUGS L469): the account name and password
  the browser viewer types at the computer's lock or sign-in screen, what
  that screen asks for, and the switch that lets the viewer sign in by
  itself. The password is write-only: the sheet shows whether one is
  saved, by whom and when, and can replace or clear it, never show it.
-->
<script lang="ts">
	import type { SigninForm, SigninInput, SigninStatus } from '#lib/api/savedSignin.ts';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import Checkbox from '#lib/ui/Checkbox.svelte';
	import Field from '#lib/ui/Field.svelte';
	import FormSection from '#lib/ui/FormSection.svelte';
	import Input from '#lib/ui/Input.svelte';
	import Radio from '#lib/ui/Radio.svelte';
	import Sheet from '#lib/ui/Sheet.svelte';
	import Status from '#lib/ui/Status.svelte';
	import Time from '#lib/ui/Time.svelte';
	import User from '#lib/ui/User.svelte';
	import { createForm, FormError, type Form } from '#lib/ui/form.svelte.ts';
	import { ICONS } from '#lib/util/display.ts';
	import type { DeviceLike } from '../model.ts';

	let {
		device,
		load,
		save,
		change,
		clear,
		settle
	}: {
		device: DeviceLike;
		load: () => Promise<SigninStatus>;
		save: (input: SigninInput) => Promise<void>;
		change: (input: { auto: boolean; form: SigninForm }) => Promise<void>;
		clear: () => Promise<boolean>;
		settle: (done: true | null) => void;
	} = $props();

	const uid = $props.id();

	interface Values {
		username: string;
		password: string;
		form: SigninForm;
		auto: boolean;
	}

	let status = $state.raw<SigninStatus | null>(null);
	let loadError = $state('');
	let form = $state<Form<Values> | null>(null);
	let clearing = $state(false);

	/** The server's code of a refused save, in the sheet's words */
	function refusal(err: unknown): string {
		const code =
			err && typeof err === 'object' && 'message' in err ? String((err as { message: unknown }).message) : '';
		const known: Record<string, string> = {
			username_required: t('devices.signin_error_username'),
			password_required: t('devices.signin_error_password'),
			username_too_long: t('devices.signin_error_too_long'),
			password_too_long: t('devices.signin_error_too_long'),
			username_control_character: t('devices.signin_error_control'),
			password_control_character: t('devices.signin_error_control'),
			username_character_not_typeable: t('devices.signin_error_not_typeable'),
			password_character_not_typeable: t('devices.signin_error_not_typeable'),
			vault_unavailable: t('devices.signin_unavailable')
		};
		return known[code] ?? (code || t('devices.signin_save_failed'));
	}

	$effect(() => {
		let gone = false;
		load().then(
			(st) => {
				if (gone) return;
				status = st;
				form = createForm<Values>({
					initial: { username: st.username, password: '', form: st.form, auto: st.auto },
					submit: async (v) => {
						const name = v.username.trim();
						const nameChanged = name !== (status?.username ?? '');
						if (!status?.set || v.password || nameChanged) {
							if (!name) throw new FormError(t('devices.signin_error_username'), 'username');
							if (!v.password)
								throw new FormError(
									status?.set ? t('devices.signin_error_new_name') : t('devices.signin_error_password'),
									'password'
								);
							try {
								await save({ username: name, password: v.password, form: v.form });
							} catch (err) {
								throw new FormError(refusal(err));
							}
							if (v.auto !== (status?.set ? status.auto : false))
								await change({ auto: v.auto, form: v.form });
						} else {
							await change({ auto: v.auto, form: v.form });
						}
						settle(true);
						return v;
					}
				});
			},
			(err: unknown) => {
				if (!gone) loadError = err instanceof Error ? err.message : String(err);
			}
		);
		return () => {
			gone = true;
		};
	});

	async function clearDetails() {
		clearing = true;
		try {
			if (await clear()) settle(true);
		} finally {
			clearing = false;
		}
	}
</script>

<Sheet
	title={t('devices.signin_title', { id: device.id })}
	subtitle={t('devices.signin_action_desc')}
	status={loadError || form?.status.message || ''}
	statusTone={loadError || form?.status.state === 'error' ? 'error' : 'neutral'}
	onclose={() => settle(null)}
>
	{#if status && !status.available}
		<p class="note" role="alert">{t('devices.signin_unavailable')}</p>
	{:else if form && status}
		{@const fm = form}
		<form id={form.id} onsubmit={form.onsubmit} novalidate>
			<FormSection title={t('devices.signin_action')}>
				<p class="note">{t('devices.signin_intro')}</p>
				<p class="note">{t('devices.signin_windows_only')}</p>
				<p class="note">
					{status.sealed_signin ? t('devices.signin_path_sealed') : t('devices.signin_path_typed')}
				</p>
				<p class="state-line">
					{#if status.set}
						<Status tone="good" label={t('devices.signin_saved')} />
					{:else}
						<Status tone="neutral" label={t('devices.signin_not_saved')} />
					{/if}
				</p>
				{#if status.set && status.updated_by}
					<p class="updated">
						{t('devices.signin_updated_by')}: <User user={status.updated_by} />{#if status.updated_at},
							<Time value={status.updated_at} relative={false} />{/if}
					</p>
				{/if}
				<Field
					label={t('devices.signin_account')}
					hint={t('devices.signin_account_hint')}
					error={form.errors.username}
				>
					{#snippet children(f)}
						<Input
							{...f}
							width="medium"
							autocomplete="off"
							spellcheck="false"
							bind:value={fm.values.username}
						/>
					{/snippet}
				</Field>
				<Field
					label={status.set ? t('devices.signin_password_new') : t('devices.signin_password')}
					hint={status.set ? t('devices.signin_password_keep_hint') : undefined}
					error={form.errors.password}
				>
					{#snippet children(f)}
						<Input
							{...f}
							type="password"
							width="medium"
							autocomplete="new-password"
							bind:value={fm.values.password}
						/>
					{/snippet}
				</Field>
			</FormSection>
			<FormSection title={t('devices.signin_form')}>
				<fieldset class="choices" aria-describedby="{uid}-form-hint">
					<legend class="sr-only">{t('devices.signin_form')}</legend>
					<Radio
						name="{uid}-form"
						value="password"
						bind:group={fm.values.form}
						label={t('devices.signin_form_password')}
					/>
					<Radio
						name="{uid}-form"
						value="name_password"
						bind:group={fm.values.form}
						label={t('devices.signin_form_name_password')}
					/>
				</fieldset>
				<p class="note" id="{uid}-form-hint">{t('devices.signin_form_hint')}</p>
			</FormSection>
			<FormSection title={t('devices.signin_auto_title')}>
				<Checkbox
					label={t('devices.signin_auto')}
					hint={t('devices.signin_auto_hint')}
					bind:checked={fm.values.auto}
				/>
			</FormSection>
			{#if status.set}
				<FormSection title={t('devices.signin_clear')}>
					<p class="note">{t('devices.signin_clear_desc')}</p>
					<div>
						<Button icon={ICONS.delete} variant="danger" busy={clearing} onclick={() => void clearDetails()}
							>{t('devices.signin_clear')}</Button
						>
					</div>
				</FormSection>
			{/if}
		</form>
	{:else if !loadError}
		<p class="loading">{t('common.loading')}</p>
	{/if}
	{#snippet footer()}
		<Button class="sheet-footer-start" onclick={() => settle(null)}>{t('actions.cancel')}</Button>
		<Button
			variant="primary"
			type="submit"
			form={form?.id}
			busy={form?.busy}
			disabled={!form || !status?.available}>{t('actions.save')}</Button
		>
	{/snippet}
</Sheet>

<style>
	.state-line,
	.updated,
	.loading,
	.note {
		margin: 0;
	}
	.updated,
	.loading,
	.note {
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	.choices {
		display: flex;
		flex-direction: column;
		gap: var(--space-1);
		margin: 0;
		padding: 0;
		border: 0;
	}
</style>
