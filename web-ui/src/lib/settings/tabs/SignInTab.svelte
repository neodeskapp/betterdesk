<!--
  Settings › Sign-in (server.config): how long RustDesk apps stay signed
  in, whether every console account needs two-factor sign-in, and one
  session per account, as one draft with one save bar (PATTERN.md rule 1).
  Save sends the parts that changed to their own endpoints, one after the
  other (lib/settings/general.ts saveInParts); the toast offers Undo,
  since these are sign-in rules for everyone. Each row says where its
  value comes from (lib/settings/sources.ts); SESSION_MAX_PER_USER in the
  server's environment locks "One session per account".

  Your own password and two-factor sign-in are on Your account
  (lib/account/PasswordAndTwoFactor.svelte); directory (LDAP), single
  sign-on and joining are tabs of their own (LdapTab, SsoTab, JoiningTab).

  Element ids are the old page's (the rows and groups carry them), for
  links and the settings search.
-->
<script lang="ts">
	import { getConsoleSessions, saveConsoleSessions } from '#lib/api/sessions.ts';
	import {
		getClientSessions,
		getSettingSources,
		getTwoFactorPolicy,
		saveClientSessions,
		saveTwoFactorPolicy,
		type SettingSources
	} from '#lib/api/settings.ts';
	import { t, tf } from '#lib/i18n/i18n.svelte.ts';
	import { can } from '#lib/state/session.svelte.ts';
	import Input from '#lib/ui/Input.svelte';
	import States from '#lib/ui/States.svelte';
	import ChangesBar from '#lib/ui/settings/ChangesBar.svelte';
	import SettingRow from '#lib/ui/settings/SettingRow.svelte';
	import SettingsBody from '#lib/ui/settings/SettingsBody.svelte';
	import SettingsGroup from '#lib/ui/settings/SettingsGroup.svelte';
	import { SettingsDraft, saveAndConfirm, type Change } from '#lib/ui/settings/draft.svelte.ts';
	import { partsWithChanges, saveInParts } from '../general.ts';
	import { sessionProblems } from '../signin.ts';
	import { sourceInfo, sourceRow, type SourceInfo } from '../sources.ts';

	const canConfig = can('server.config');
	/** session.DefaultMaxPerUser: sessions per account without the switch or SESSION_MAX_PER_USER */
	const DEFAULT_MAX_PER_USER = 10;
	const errText = (err: unknown) =>
		err instanceof Error && err.message ? err.message : t('errors.server_error');

	interface Values {
		expiry_days: number | null;
		sliding: boolean;
		max_days: number | null;
		two_factor_required: boolean;
		single_session: boolean;
		[key: string]: unknown;
	}

	type Part = 'sessions' | 'two_factor' | 'single_session';
	const ORDER: readonly Part[] = ['sessions', 'two_factor', 'single_session'];
	const PARTS: Record<Part, readonly string[]> = {
		sessions: ['expiry_days', 'sliding', 'max_days'],
		two_factor: ['two_factor_required'],
		single_session: ['single_session']
	};
	const TITLES: Record<Part, string> = {
		sessions: 'settings.client_sessions_title',
		two_factor: 'settings.signin.require_2fa',
		single_session: 'settings.signin.single_session'
	};
	const title = (p: Part) => t(TITLES[p]);

	const onOff = (v: unknown) => (v ? t('settings.side.on') : t('settings.side.off'));
	const draft = new SettingsDraft<Values>(
		{ expiry_days: 7, sliding: true, max_days: 30, two_factor_required: false, single_session: false },
		{
			expiry_days: { label: t('settings.client_sessions_expiry_days') },
			sliding: { label: t('settings.client_sessions_sliding'), format: onOff },
			max_days: { label: t('settings.client_sessions_max_days') },
			two_factor_required: { label: t('settings.signin.require_2fa'), format: onOff },
			single_session: { label: t('settings.signin.single_session'), format: onOff }
		}
	);
	$effect(() => draft.track());

	let loaded = $state(false);
	let loadError = $state('');
	let sources = $state.raw<SettingSources>({});
	/**
	 * How many sessions an account may have with the switch off, for the
	 * help. The server says it only while the switch is off (source
	 * "default"); otherwise it is session.DefaultMaxPerUser.
	 */
	let offLimit = $state(DEFAULT_MAX_PER_USER);
	/** SESSION_MAX_PER_USER as the older answer says it, for servers without sources */
	let sessionEnv = $state<{ value: string; limit: number } | null>(null);

	const v = $derived(draft.values);
	const problems = $derived.by(() => {
		const p = sessionProblems(v);
		return {
			expiry_days: p.expiry_days ? t(p.expiry_days) : '',
			max_days: p.max_days ? t(p.max_days) : ''
		};
	});

	async function loadForm() {
		try {
			const [sessions, twoFactor, consoleSessions] = await Promise.all([
				getClientSessions(),
				getTwoFactorPolicy(),
				getConsoleSessions(),
				getSettingSources()
					.then((s) => (sources = s))
					.catch(() => (sources = {}))
			]);
			offLimit = consoleSessions.source === 'default' ? consoleSessions.limit : DEFAULT_MAX_PER_USER;
			sessionEnv =
				consoleSessions.source === 'env'
					? { value: String(consoleSessions.env_value ?? ''), limit: consoleSessions.limit }
					: null;
			draft.load({
				expiry_days: Number(sessions.expiry_days ?? 7),
				sliding: sessions.sliding !== false,
				max_days: Number(sessions.max_days ?? 30),
				two_factor_required: twoFactor,
				single_session: consoleSessions.single_session
			});
			loadError = '';
			loaded = true;
		} catch (err) {
			// Without the saved values the form would overwrite them with defaults
			loadError = errText(err);
		}
	}

	async function savePart(part: Part, values: Values) {
		if (part === 'sessions')
			await saveClientSessions({
				expiry_days: Number(values.expiry_days),
				sliding: values.sliding,
				max_days: Number(values.max_days)
			});
		else if (part === 'two_factor') await saveTwoFactorPolicy(values.two_factor_required);
		else await saveConsoleSessions(values.single_session);
	}

	async function save() {
		if (draft.busy) return;
		const parts = partsWithChanges(draft.changes, PARTS, ORDER);
		if (parts.includes('sessions') && (problems.expiry_days || problems.max_days)) {
			draft.error = tf(
				'settings_ui.fix_marked',
				'Some values are not right yet. They are marked on the page.'
			);
			const first = problems.expiry_days ? 'client-sessions-expiry-days' : 'client-sessions-max-days';
			document.getElementById(`${first}-control`)?.focus();
			return;
		}
		const values = $state.snapshot(draft.values) as Values;
		await saveAndConfirm(draft, () => saveInParts(parts, PARTS, title, (p) => savePart(p, values)), {
			undo,
			reload: loadForm
		});
	}

	/** Undo in the toast: every saved change back to the value it had */
	async function undo(changes: Change[]) {
		const back = {
			...($state.snapshot(draft.loaded) as Values),
			...Object.fromEntries(changes.map((c) => [c.field, c.fromValue]))
		} as Values;
		const parts = partsWithChanges(changes, PARTS, ORDER);
		await saveInParts(parts, PARTS, title, (p) => savePart(p, back));
	}

	$effect(() => {
		if (canConfig) void loadForm();
	});

	const src = (key: string): SourceInfo => sourceInfo(sources[key]);
	const singleInfo = $derived.by(() => {
		const info = src('console_sessions.single_session');
		if (info.lockedBy || !sessionEnv) return info;
		return { ...info, lockedBy: 'SESSION_MAX_PER_USER' };
	});

	/**
	 * With SESSION_MAX_PER_USER set, the row shows what the variable gives:
	 * on for one session, off for more, and says the number once, with the
	 * variable named in the source line only
	 */
	const envRow = $derived.by(() => {
		if (!sessionEnv) return null;
		const { value, limit } = sessionEnv;
		const help =
			limit === 1
				? tf('settings.signin.single_session_on', "A new sign-in signs out the account's other browser.")
				: limit === 0
					? tf(
							'settings.signin.sessions_any',
							'An account can be signed in on any number of browsers and devices at once.'
						)
					: tf(
							'settings.signin.sessions_up_to',
							'An account can be signed in on up to {max} browsers and devices at once.',
							{ max: limit }
						);
		const text = tf(
			'settings.signin.single_session_env_source',
			'Set in the environment file ({name}={value}). Remove it there to use this switch.',
			{ name: SESSION_ENV, value }
		);
		return { checked: limit === 1, help, source: { kind: 'environment' as const, text } };
	});
	const SESSION_ENV = 'SESSION_MAX_PER_USER';

	function days(e: Event): number | null {
		const raw = (e.currentTarget as HTMLInputElement).value;
		return raw === '' ? null : Number(raw);
	}
</script>

{#snippet bar()}<ChangesBar {draft} onsave={save} />{/snippet}

{#if canConfig}
	<SettingsBody {bar}>
		{#if loadError && !loaded}
			<States kind="error" message={loadError} onretry={() => void loadForm()} />
		{:else if !loaded}
			<States kind="loading" />
		{:else}
			<SettingsGroup
				id="client-sessions-form"
				title={t('settings.client_sessions_title')}
				description={t('settings.client_sessions_desc')}
				consequence={tf(
					'settings.signin.client_sessions_next',
					'Applies from the next sign-in of a RustDesk app. Apps signed in now keep their session.'
				)}
			>
				<SettingRow
					id="client-sessions-expiry-days"
					label={t('settings.client_sessions_expiry_days')}
					help={tf(
						'settings.signin.expiry_help',
						'When Extend session on activity is off, apps must sign in again after this many days.'
					)}
					error={problems.expiry_days}
					keyName="API_TOKEN_EXPIRY_DAYS"
					{...sourceRow(draft, 'expiry_days', src('client_sessions.expiry_days'))}
				>
					{#snippet control(f)}
						<Input
							id={f.id}
							describedby={f.describedby}
							invalid={f.invalid}
							type="number"
							min="1"
							max="365"
							width="short"
							value={v.expiry_days}
							oninput={(e) => draft.set('expiry_days', days(e))}
						/>
					{/snippet}
				</SettingRow>
				<SettingRow
					id="client-sessions-sliding"
					label={t('settings.client_sessions_sliding')}
					help={t('settings.client_sessions_sliding_hint')}
					checked={v.sliding}
					onchange={(on) => draft.set('sliding', on)}
					{...sourceRow(draft, 'sliding', src('client_sessions.sliding'))}
				/>
				<SettingRow
					id="client-sessions-max-days"
					label={t('settings.client_sessions_max_days')}
					help={tf(
						'settings.signin.max_days_help',
						'Upper limit for the renewal, counted from the first sign-in.'
					)}
					error={problems.max_days}
					noEffect={v.sliding
						? undefined
						: tf('settings.signin.max_days_off', 'Has no effect while Extend session on activity is off.')}
					{...sourceRow(draft, 'max_days', src('client_sessions.max_days'))}
				>
					{#snippet control(f)}
						<Input
							id={f.id}
							describedby={f.describedby}
							invalid={f.invalid}
							type="number"
							min="1"
							max="365"
							width="short"
							value={v.max_days}
							oninput={(e) => draft.set('max_days', days(e))}
						/>
					{/snippet}
				</SettingRow>
			</SettingsGroup>

			<SettingsGroup
				id="console-signin-section"
				title={tf('settings.signin.console_title', 'Signing in to the console')}
				description={tf(
					'settings.signin.console_note',
					'Rules for every account that signs in to this console.'
				)}
				consequence={tf(
					'settings.signin.console_effect',
					'Applies from the next sign-in. People signed in now stay signed in.'
				)}
			>
				<SettingRow
					id="require-two-factor"
					label={t('settings.signin.require_2fa')}
					help={t('settings.signin.require_2fa_hint')}
					checked={v.two_factor_required}
					onchange={(on) => draft.set('two_factor_required', on)}
					{...sourceRow(draft, 'two_factor_required', src('two_factor.required'))}
				/>
				{@const single = sourceRow(draft, 'single_session', singleInfo)}
				<SettingRow
					id="single-session"
					label={tf('settings.signin.single_session', 'One session per account')}
					help={envRow?.help ??
						tf(
							'settings.signin.single_session_hint',
							"A new sign-in signs out the account's other browser. Off: an account can be signed in on up to {max} browsers and devices at once.",
							{ max: offLimit }
						)}
					checked={envRow ? envRow.checked : v.single_session}
					onchange={(on) => draft.set('single_session', on)}
					disabled={singleInfo.lockedBy !== ''}
					keyName={envRow ? undefined : SESSION_ENV}
					terms={[SESSION_ENV]}
					{...single}
					source={envRow?.source ?? single.source}
					onreset={singleInfo.lockedBy ? undefined : single.onreset}
				/>
			</SettingsGroup>
		{/if}
	</SettingsBody>
{/if}
