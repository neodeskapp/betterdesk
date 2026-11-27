<!--
  Settings › Email (server.config): the mail server the console sends its
  email through, as one draft with one save bar (PATTERN.md rule 1). The
  user name and password show only with "The mail server needs a
  sign-in" (rule 6). Each row says where its value comes from
  (lib/settings/sources.ts): a value saved here wins over the SMTP_
  variables of the environment, so nothing is locked. Beside the form:
  whether mail works now with Test connection (an action that checks the
  saved server), and what sends email.
-->
<script lang="ts">
	import { ApiError } from '#lib/api/client.ts';
	import { errorText, getSmtp, saveSmtp, testSmtp, type SmtpConfig } from '#lib/api/settings-system.ts';
	import { getSettingSources, type SettingSources } from '#lib/api/settings.ts';
	import { t, tf } from '#lib/i18n/i18n.svelte.ts';
	import { link } from '#lib/nav.ts';
	import { onRefresh } from '#lib/shell/page.svelte.ts';
	import { can } from '#lib/state/session.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import Facts from '#lib/ui/Facts.svelte';
	import Input from '#lib/ui/Input.svelte';
	import ListRow from '#lib/ui/ListRow.svelte';
	import RowList from '#lib/ui/RowList.svelte';
	import ShowWhen from '#lib/ui/ShowWhen.svelte';
	import States from '#lib/ui/States.svelte';
	import Status from '#lib/ui/Status.svelte';
	import { confirm } from '#lib/ui/dialogs.svelte.ts';
	import ChangesBar from '#lib/ui/settings/ChangesBar.svelte';
	import ListEditor from '#lib/ui/settings/ListEditor.svelte';
	import SettingRow from '#lib/ui/settings/SettingRow.svelte';
	import SettingsBody from '#lib/ui/settings/SettingsBody.svelte';
	import SettingsGroup from '#lib/ui/settings/SettingsGroup.svelte';
	import { SettingsDraft, saveAndConfirm } from '#lib/ui/settings/draft.svelte.ts';
	import { emailProblem, smtpProblems, splitEmails, type SmtpValues } from '../email.ts';
	import { sourceInfo, sourceRow } from '../sources.ts';
	import '../side-rows.css';

	const allowed = can('server.config');

	/**
	 * The form for a saved configuration. The password field holds the
	 * server's mask while a password is saved: sent back untouched it keeps
	 * that password, cleared it removes it.
	 */
	function toValues(c: SmtpConfig | null): SmtpValues {
		const configured = !!c?.configured;
		return {
			host: configured ? (c?.host ?? '') : '',
			port: configured ? Number(c?.port) || 587 : 587,
			secure: configured && !!c?.secure,
			// The switch is the form's own: on when a user name is saved
			auth: configured && !!c?.user,
			user: configured ? (c?.user ?? '') : '',
			pass: configured ? (c?.pass ?? '') : '',
			from: configured ? (c?.from ?? '') : '',
			alert_email: configured ? splitEmails(c?.alert_email ?? '') : []
		};
	}

	const onOff = (v: unknown) => (v ? t('settings.side.on') : t('settings.side.off'));
	const draft = new SettingsDraft<SmtpValues>(toValues(null), {
		host: { label: t('settings.email.host_label') },
		port: { label: t('settings.email.smtp_port') },
		secure: { label: t('settings.email.secure_label'), format: onOff },
		auth: { label: t('settings.email.auth_label'), format: onOff },
		user: { label: t('settings.email.smtp_user') },
		pass: { label: t('settings.email.smtp_pass'), format: (v) => (v ? '••••••' : t('settings_ui.empty')) },
		from: { label: t('settings.email.smtp_from') },
		alert_email: { label: t('settings.email.alert_email') }
	});
	$effect(() => draft.track());

	let config = $state.raw<SmtpConfig | null>(null);
	let loaded = $state(false);
	let loadError = $state('');
	let sources = $state.raw<SettingSources>({});
	let testing = $state(false);
	let testResult = $state<{ ok: boolean; message: string } | null>(null);
	/** What the server refused, by field */
	let refused = $state<Partial<Record<string, string>>>({});

	const v = $derived(draft.values);
	const problems = $derived<Partial<Record<string, string>>>(smtpProblems(v));
	/** A save was tried: from then on every problem shows, not only those of changed fields */
	let tried = $state(false);
	// A server that is not set up has no host: that is said once a change or a save asks for one
	const errorOf = (f: string) => ((tried || draft.isChanged(f)) && problems[f]) || refused[f] || '';

	/** The fields a refusal of the server can name */
	const FIELDS: readonly string[] = ['host', 'port', 'pass', 'from', 'alert_email'];

	async function load() {
		try {
			const [c] = await Promise.all([
				getSmtp(),
				getSettingSources()
					.then((s) => (sources = s))
					.catch(() => (sources = {}))
			]);
			config = c;
			draft.load(toValues(c));
			refused = {};
			tried = false;
			loadError = '';
			loaded = true;
		} catch (err) {
			config = null;
			loadError = errorText(err, t('errors.server_error'));
		}
	}

	function set<K extends keyof SmtpValues & string>(field: K, value: SmtpValues[K]) {
		draft.set(field, value);
		if (refused[field]) refused = { ...refused, [field]: undefined };
	}

	/** TLS goes with port 465 and STARTTLS with 587: switching one moves the other along */
	function setSecure(on: boolean) {
		set('secure', on);
		if (on && Number(v.port) === 587) set('port', 465);
		else if (!on && Number(v.port) === 465) set('port', 587);
	}

	async function save() {
		if (draft.busy) return;
		const wrong = Object.keys(problems);
		tried = true;
		if (wrong.length) {
			draft.error = tf(
				'settings_ui.fix_marked',
				'Some values are not right yet. They are marked on the page.'
			);
			document.getElementById(`${ROW_IDS[wrong[0] ?? 'host']}-control`)?.focus();
			return;
		}
		const yes = await confirm({
			title: t('settings.confirm.smtp_save_title'),
			message: t('settings.confirm.smtp_save'),
			confirmLabel: t('settings.email.smtp_save')
		});
		if (!yes) return;
		refused = {};
		const values = $state.snapshot(draft.values) as SmtpValues;
		// The certificate check has no switch here: the saved one goes back as it was
		const tlsVerify = config?.tlsVerify;
		await saveAndConfirm(
			draft,
			async () => {
				try {
					// Without a sign-in an empty user name goes out, which the server reads as "no sign-in"
					await saveSmtp({
						host: values.host.trim(),
						port: Number(values.port),
						secure: values.secure,
						user: values.auth ? values.user.trim() : '',
						pass: values.auth ? values.pass : '',
						from: values.from.trim(),
						alert_email: values.alert_email.join(', '),
						...(tlsVerify === undefined ? {} : { tlsVerify })
					});
				} catch (err) {
					// The server names the field a refusal is about
					const field = err instanceof ApiError ? (err.data as { field?: unknown } | null)?.field : undefined;
					if (typeof field === 'string' && FIELDS.includes(field)) refused = { [field]: errorText(err, '') };
					throw err;
				}
				testResult = null;
			},
			{ reload: load }
		);
	}

	async function test() {
		testing = true;
		testResult = null;
		try {
			const r = await testSmtp();
			testResult = r.success
				? { ok: true, message: t('settings.email.smtp_test_success') }
				: { ok: false, message: r.error || t('settings.email.smtp_test_failed') };
		} catch (err) {
			testResult = { ok: false, message: errorText(err, t('settings.email.smtp_test_failed')) };
		} finally {
			testing = false;
		}
	}

	$effect(() => {
		if (!allowed) return;
		void load();
		return onRefresh(() => void load());
	});

	const ROW_IDS: Record<string, string> = {
		host: 'email-smtp-host',
		port: 'email-smtp-port',
		secure: 'email-smtp-secure',
		auth: 'email-smtp-auth',
		user: 'email-smtp-user',
		pass: 'email-smtp-pass',
		from: 'email-smtp-from',
		alert_email: 'email-smtp-alert'
	};

	const src = (field: string) => sourceInfo(sources[`smtp.${field}`]);
	/**
	 * What an empty field means: the server's default ("Empty uses
	 * neodesk@localhost"), said beside the field while it is empty and
	 * the default applies. Empty when there is nothing to say.
	 */
	function emptyUses(field: 'host' | 'user' | 'pass' | 'from'): string {
		if (String(v[field] ?? '').trim()) return '';
		const info = src(field);
		const value = info.hasDefault ? String(info.default ?? '').trim() : '';
		if (!value || info.lockedBy) return '';
		return tf('settings.email.empty_uses', 'Empty uses {value}.', { value });
	}
	const configured = $derived(!!config?.configured);
	const facts = $derived([
		{
			label: t('settings.email.host_label'),
			value: config?.host ? `${config.host}:${config.port || 587}` : ''
		},
		{ label: t('settings.email.smtp_from'), value: config?.from ?? '' },
		{ label: t('settings.email.alert_email'), value: config?.alert_email ?? '' }
	]);
</script>

{#snippet textRow(field: 'host' | 'user' | 'pass' | 'from', label: string, help: string, type = 'text')}
	<SettingRow
		id={ROW_IDS[field] ?? field}
		{label}
		{help}
		error={errorOf(field)}
		keyName={sources[`smtp.${field}`]?.env_var}
		{...sourceRow(draft, field, src(field))}
	>
		{#snippet control(f)}
			{@const empty = emptyUses(field)}
			<Input
				id={f.id}
				describedby={[f.describedby, empty && `${f.id}-empty`].filter(Boolean).join(' ') || undefined}
				invalid={f.invalid}
				class={field === 'host' ? 'mono' : undefined}
				{type}
				autocomplete={type === 'password' ? 'new-password' : 'off'}
				spellcheck={false}
				value={v[field]}
				oninput={(e) => set(field, (e.currentTarget as HTMLInputElement).value)}
			/>
			{#if empty}<p class="empty-uses" id="{f.id}-empty">{empty}</p>{/if}
		{/snippet}
	</SettingRow>
{/snippet}

{#snippet side()}
	<div class="summary-stack">
		<section class="section" aria-labelledby="email-side-title">
			<h2 id="email-side-title">{t('settings.side.state_title')}</h2>
			{#if loadError}
				<States kind="error" message={loadError} onretry={() => void load()} />
			{:else if !config}
				<States kind="loading" />
			{:else if !configured}
				<p><Status tone="neutral" label={t('settings.side.email_off')} /></p>
			{:else}
				<p><Status tone="good" label={t('settings.side.email_on')} /></p>
				<Facts items={facts} />
				<div class="settings-inline-controls">
					<Button id="email-smtp-test-btn" busy={testing} onclick={test}
						>{t('settings.email.smtp_test')}</Button
					>
					<span role="status">
						{#if testResult}
							<Status tone={testResult.ok ? 'good' : 'danger'} label={testResult.message} />
						{/if}
					</span>
				</div>
				<p class="hint">{t('settings.email.test_hint')}</p>
			{/if}
		</section>
		<section class="section" aria-labelledby="email-uses-title">
			<h2 id="email-uses-title">{t('settings.email.uses_title')}</h2>
			<RowList>
				<ListRow
					title={t('nav.automation')}
					sub={t('settings.email.uses_alerts')}
					href={link('/automation')}
				/>
				{#if can('billing.view')}
					<ListRow
						title={t('nav.help_requests')}
						sub={t('settings.email.uses_help')}
						href={link('/billing?settings=1#billing-settings-panel')}
					/>
				{/if}
			</RowList>
		</section>
	</div>
{/snippet}

{#snippet bar()}<ChangesBar {draft} onsave={save} />{/snippet}

{#if !allowed}
	<States message={t('settings.page.admin_only')} />
{:else}
	<SettingsBody summary={side} summaryLabel={t('settings.side.state_title')} {bar} class="side-rows">
		{#if loadError && !loaded}
			<States kind="error" message={loadError} onretry={() => void load()} />
		{:else if !loaded}
			<States kind="loading" />
		{:else}
			<SettingsGroup
				id="email-smtp-form"
				title={t('settings.email.server_title')}
				description={t('settings.email.server_note')}
				consequence={t('settings.email.save_effect')}
			>
				{@render textRow(
					'host',
					t('settings.email.host_label'),
					tf(
						'settings.email.host_help',
						'Host name or IP address of the mail server, without a port. For example smtp.example.com.'
					)
				)}
				<SettingRow
					id="email-smtp-port"
					label={t('settings.email.smtp_port')}
					help={t('settings.email.port_hint')}
					error={errorOf('port')}
					keyName={sources['smtp.port']?.env_var}
					{...sourceRow(draft, 'port', src('port'))}
				>
					{#snippet control(f)}
						<Input
							id={f.id}
							describedby={f.describedby}
							invalid={f.invalid}
							type="number"
							min="1"
							max="65535"
							width="short"
							value={v.port}
							oninput={(e) => {
								const raw = (e.currentTarget as HTMLInputElement).value;
								set('port', raw === '' ? null : Number(raw));
							}}
						/>
					{/snippet}
				</SettingRow>
				<SettingRow
					id="email-smtp-secure"
					label={t('settings.email.secure_label')}
					help={t('settings.email.secure_hint')}
					checked={v.secure}
					onchange={setSecure}
					keyName={sources['smtp.secure']?.env_var}
					{...sourceRow(draft, 'secure', src('secure'))}
				/>
				<SettingRow
					id="email-smtp-auth"
					label={t('settings.email.auth_label')}
					help={t('settings.email.auth_hint')}
					checked={v.auth}
					onchange={(on) => set('auth', on)}
					controls="email-smtp-auth-fields"
					changed={draft.isChanged('auth')}
				/>
				<ShowWhen id="email-smtp-auth-fields" when={v.auth}>
					{@render textRow(
						'user',
						t('settings.email.smtp_user'),
						tf('settings.email.user_help', 'The user name for the mail server, often the full email address.')
					)}
					{@render textRow(
						'pass',
						t('settings.email.smtp_pass'),
						t('settings.email.pass_hint_masked'),
						'password'
					)}
				</ShowWhen>
				{@render textRow(
					'from',
					t('settings.email.smtp_from'),
					tf(
						'settings.email.from_help',
						'The sender people see. Many mail servers only accept their own addresses here. For example neodesk@example.com or NeoDesk <neodesk@example.com>.'
					),
					'email'
				)}
				<SettingRow
					id="email-smtp-alert"
					label={t('settings.email.alert_email')}
					help={tf(
						'settings.email.alert_help',
						'Who gets system warnings, and alerts when no operator email is known. One address per row, for example ops@example.com.'
					)}
					group
					error={refused.alert_email ?? ''}
					keyName={sources['smtp.alert_email']?.env_var}
					{...sourceRow(draft, 'alert_email', src('alert_email'), splitEmails)}
				>
					{#snippet control(f)}
						<ListEditor
							id={f.id}
							describedby={f.describedby}
							items={v.alert_email}
							inputmode="email"
							onchange={(list) => set('alert_email', list)}
							itemLabel={(n) => tf('settings.email.alert_n', 'Alert email {n}', { n })}
							check={emailProblem}
							addLabel={tf('settings.email.alert_add', 'Add an address')}
						/>
					{/snippet}
				</SettingRow>
			</SettingsGroup>
		{/if}
	</SettingsBody>
{/if}

<style>
	/* The summary column is narrow: sections apart, labels take what they need and values the rest */
	.summary-stack {
		display: flex;
		flex-direction: column;
		gap: var(--space-6);
		min-width: 0;
	}
	.summary-stack :global(.facts) {
		grid-template-columns: minmax(0, max-content) minmax(0, 1fr);
	}
	.summary-stack :global(.facts > dt) {
		max-width: 160px;
	}
	.hint,
	.empty-uses {
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	.empty-uses {
		margin: var(--space-1) 0 0;
	}
	.settings-inline-controls {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2) var(--space-3);
		align-items: center;
	}
</style>
