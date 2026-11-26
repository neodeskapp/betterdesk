<!--
  Four tabs of Settings in one component, by `part`:
  - signin (Settings › Sign-in, server.config): the console's client
    sessions and whether everyone needs two-factor sign-in. Your own
    password and two-factor sign-in are on Your account
    (lib/account/PasswordAndTwoFactor.svelte).
  - ldap (Settings › Directory (LDAP), server.config): sign-in with a
    directory, with a test button.
  - sso (Settings › Single sign-on, server.config): sign-in with an OpenID
    Connect provider, with a test of its discovery.
  - joining (part of Settings › Joining, server.config): how new devices
    are approved. The join mode shows here; it is changed on Device
    tokens, which the section links to for accounts with
    enrollment.manage.
  Each is its own form with its own save (different endpoints; LDAP and
  SSO ask before a save that can lock people out). What an install sets
  once or never (LDAP's TLS check and timeout, the provider's endpoints and
  private networks) sits behind Advanced. `bare` leaves out the tab layout,
  for a tab made of several parts (JoiningTab).

  Element ids are the old page's, for links and the settings search.
-->
<script lang="ts">
	import {
		getClientSessions,
		getEnrollment,
		getLdap,
		getOidc,
		getTwoFactorPolicy,
		saveClientSessions,
		saveEnrollment,
		saveLdap,
		saveOidc,
		saveTwoFactorPolicy,
		testLdap,
		testOidc
	} from '#lib/api/settings.ts';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import { link } from '#lib/nav.ts';
	import { can } from '#lib/state/session.svelte.ts';
	import BooleanStatus from '#lib/ui/BooleanStatus.svelte';
	import Button from '#lib/ui/Button.svelte';
	import Checkbox from '#lib/ui/Checkbox.svelte';
	import Disclosure from '#lib/ui/Disclosure.svelte';
	import Empty from '#lib/ui/Empty.svelte';
	import Facts from '#lib/ui/Facts.svelte';
	import Field from '#lib/ui/Field.svelte';
	import FormStatus from '#lib/ui/FormStatus.svelte';
	import Input from '#lib/ui/Input.svelte';
	import Notice from '#lib/ui/Notice.svelte';
	import SaveBar from '#lib/ui/SaveBar.svelte';
	import Select from '#lib/ui/Select.svelte';
	import ShowWhen, { showWhenControl } from '#lib/ui/ShowWhen.svelte';
	import Textarea from '#lib/ui/Textarea.svelte';
	import { confirm } from '#lib/ui/dialogs.svelte.ts';
	import { createForm } from '#lib/ui/form.svelte.ts';
	import { trackDirty } from '../dirty.ts';
	import {
		ldapFromServer,
		ldapPayload,
		oidcFromServer,
		oidcPayload,
		ldapNumberProblems,
		sessionProblems,
		validOidcRedirect,
		suggestedRedirect,
		type LdapValues,
		type OidcValues
	} from '../signin.ts';
	import ConsoleSessionsForm from '../ConsoleSessionsForm.svelte';
	import ImpersonationMailForm from '../ImpersonationMailForm.svelte';
	import SettingsGrid from '../SettingsGrid.svelte';
	import SettingsLayout from '../SettingsLayout.svelte';

	type Part = 'signin' | 'ldap' | 'sso' | 'joining';
	let { part = 'signin', bare = false }: { part?: Part; bare?: boolean } = $props();

	const canConfig = can('server.config');

	/** Examples in empty fields: addresses, directory names and claims, the same in every language */
	const EX = {
		ldapHost: 'ldap.example.com',
		ldapPort: '389',
		ldapTimeout: '10',
		bindDn: 'cn=readonly,dc=example,dc=com',
		baseDn: 'dc=example,dc=com',
		userFilter: '(sAMAccountName={{username}})',
		directBindDn: 'uid={{username}},ou=users,dc=example,dc=com',
		attrId: 'sAMAccountName',
		attrEmail: 'mail',
		attrName: 'displayName',
		groupBaseDn: 'ou=groups,dc=example,dc=com',
		groupFilter: '(member={{dn}})',
		groupAttr: 'cn',
		ldapRoleMap:
			'CN=NeoDesk Admins,OU=Groups,DC=example,DC=com=admin|CN=Operators,OU=Groups,DC=example,DC=com=operator',
		oidcName: 'Azure AD / Okta / Google',
		issuer: 'https://login.microsoftonline.com/{tenant}/v2.0',
		cidrs: '10.20.0.0/16, 192.168.50.10',
		redirect: 'https://your-server:5443/api/auth/oidc/callback',
		panel: 'http://your-server:3375',
		scopes: 'openid profile email',
		authUrl: 'https://idp.example.com/authorize',
		tokenUrl: 'https://idp.example.com/token',
		userinfoUrl: 'https://idp.example.com/userinfo',
		claimUser: 'preferred_username',
		claimEmail: 'email',
		claimName: 'name',
		claimGroups: 'groups',
		oidcRoleMap: 'neodesk-admins=admin\nneodesk-operators=operator'
	} as const;
	const errText = (err: unknown, fallback = t('errors.server_error')) =>
		err instanceof Error && err.message ? err.message : fallback;
	type Line = { message: string; tone: 'neutral' | 'ok' | 'error' };
	const NO_LINE: Line = { message: '', tone: 'neutral' };

	/* ── Device enrollment ── */

	const enrollment = createForm({
		initial: { rich_approve: true, tag_picker: true },
		submit: async (v) => {
			const saved = await saveEnrollment(v);
			return { rich_approve: saved.rich_approve !== false, tag_picker: saved.tag_picker !== false };
		}
	});
	let enrolledMode = $state('');
	let pendingCount = $state(0);
	/** The mode is changed on Device tokens, under the permission that page asks */
	const canChangeMode = can('enrollment.manage');

	async function loadEnrollment() {
		try {
			const d = await getEnrollment();
			enrolledMode = d.mode || 'open';
			pendingCount = Number(d.pending_count) || 0;
			enrollment.reset({ rich_approve: d.rich_approve !== false, tag_picker: d.tag_picker !== false });
		} catch (err) {
			enrollment.status = { state: 'error', message: errText(err) };
		}
	}

	/* ── Client sessions ── */

	const sessions = createForm({
		initial: { expiry_days: 7 as number | null, sliding: true, max_days: 30 as number | null },
		// Days out of range are refused on the field, not replaced
		validate: (v) => {
			const p = sessionProblems(v);
			return {
				expiry_days: p.expiry_days && t(p.expiry_days),
				max_days: p.max_days && t(p.max_days)
			};
		},
		submit: async (v) => {
			const out = { expiry_days: Number(v.expiry_days), sliding: v.sliding, max_days: Number(v.max_days) };
			await saveClientSessions({
				expiry_days: out.expiry_days,
				sliding: out.sliding,
				max_days: out.max_days
			});
			const d = await getClientSessions();
			return {
				expiry_days: Number(d.expiry_days ?? out.expiry_days),
				sliding: d.sliding !== false,
				max_days: Number(d.max_days ?? out.max_days)
			};
		}
	});

	/* ── Mandatory two-factor sign-in ── */

	const twoFactor = createForm({
		id: 'two-factor-policy-form',
		initial: { required: false },
		submit: async (v) => {
			await saveTwoFactorPolicy(v.required);
			return { required: await getTwoFactorPolicy() };
		}
	});

	async function loadTwoFactor() {
		try {
			twoFactor.reset({ required: await getTwoFactorPolicy() });
		} catch (err) {
			twoFactor.status = { state: 'error', message: errText(err) };
		}
	}

	async function loadSessions() {
		try {
			const d = await getClientSessions();
			sessions.reset({
				expiry_days: Number(d.expiry_days ?? 7),
				sliding: d.sliding !== false,
				max_days: Number(d.max_days ?? 30)
			});
		} catch (err) {
			sessions.status = { state: 'error', message: errText(err) };
		}
	}

	/* ── LDAP ── */

	let ldapWasEnabled = $state(false);
	let ldapOn = $state<boolean | null>(null);
	let ldapTest = $state<Line>(NO_LINE);
	let ldapTesting = $state(false);

	const ldap = createForm<LdapValues>({
		initial: ldapFromServer({}),
		// Numbers out of range are refused here; the server checks the rest
		// and its refusal lands on the field it names
		validate: (v) => {
			const p = ldapNumberProblems(v);
			return { port: p.port && t(p.port), conn_timeout_sec: p.conn_timeout_sec && t(p.conn_timeout_sec) };
		},
		submit: async (v) => {
			const body = ldapPayload(v);
			await saveLdap(body);
			ldapWasEnabled = body.enabled;
			ldapOn = body.enabled;
			return v;
		},
		savedMessage: t('settings.ldap_saved')
	});

	async function loadLdap() {
		try {
			const d = await getLdap();
			const v = ldapFromServer(d);
			ldap.reset(v);
			ldapWasEnabled = v.enabled;
			ldapOn = v.enabled;
		} catch (err) {
			ldap.status = { state: 'error', message: errText(err) };
		}
	}

	async function onLdapEnabled() {
		if (!ldap.values.enabled || ldapWasEnabled) return;
		const ok = await confirm({
			title: t('settings.confirm.ldap_enable_title'),
			message: t('settings.confirm.ldap_enable'),
			confirmLabel: t('settings.ldap_enabled')
		});
		if (!ok) ldap.values.enabled = false;
	}

	// TLS on: the usual LDAPS port; off: the plain one
	function onLdapTls() {
		const port = Number(ldap.values.port);
		if (ldap.values.use_tls && (port === 389 || !port)) ldap.values.port = 636;
		else if (!ldap.values.use_tls && port === 636) ldap.values.port = 389;
	}

	async function submitLdap(e: SubmitEvent) {
		e.preventDefault();
		if (ldap.busy) return;
		const problems = ldapNumberProblems(ldap.values);
		if (problems.port || problems.conn_timeout_sec) {
			// The form's check refuses them and says why at the field
			await ldap.submit();
			return;
		}
		if (ldap.values.enabled) {
			const ok = await confirm({
				title: t('settings.confirm.ldap_save_title'),
				message: t('settings.confirm.ldap_save'),
				confirmLabel: t('settings.ldap_save')
			});
			if (!ok) return;
		}
		await ldap.submit();
	}

	async function runLdapTest() {
		if (ldapTesting) return;
		ldapTesting = true;
		ldapTest = NO_LINE;
		try {
			const r = await testLdap(ldapPayload($state.snapshot(ldap.values) as LdapValues));
			ldapTest =
				r.success !== false && !r.error
					? { message: t('settings.ldap_test_success'), tone: 'ok' }
					: { message: r.error || t('settings.ldap_test_failed'), tone: 'error' };
		} catch (err) {
			ldapTest = { message: errText(err, t('settings.ldap_test_failed')), tone: 'error' };
		} finally {
			ldapTesting = false;
		}
	}

	/* ── Single sign-on (OIDC) ── */

	let oidcWasEnabled = $state(false);
	let oidcOn = $state<boolean | null>(null);
	let oidcTest = $state<Line>(NO_LINE);
	let oidcTesting = $state(false);
	let issuerEl = $state<HTMLInputElement | null>(null);

	const oidc = createForm<OidcValues>({
		initial: oidcFromServer({}, location.origin),
		validate: (v) =>
			v.enabled && !validOidcRedirect(v.redirect_url)
				? { redirect_url: t('settings.oidc_redirect_url_invalid') }
				: null,
		submit: async (v) => {
			const body = oidcPayload(v, location.origin);
			await saveOidc(body);
			oidcWasEnabled = body.enabled;
			oidcOn = body.enabled;
			return v;
		},
		savedMessage: t('settings.oidc_saved')
	});

	// The redirect address must end in the callback path; said under the field as soon as it is wrong
	const redirectWarning = $derived(
		oidc.values.enabled && oidc.values.redirect_url.trim() && !validOidcRedirect(oidc.values.redirect_url)
			? `${t('settings.oidc_redirect_url_invalid')} ${t('settings.oidc_redirect_url_example', { url: suggestedRedirect(location.origin) })}`
			: ''
	);

	async function loadOidc() {
		try {
			const v = oidcFromServer(await getOidc(), location.origin);
			oidc.reset(v);
			oidcWasEnabled = v.enabled;
			oidcOn = v.enabled;
		} catch (err) {
			oidc.status = { state: 'error', message: errText(err) };
		}
	}

	async function onOidcEnabled() {
		if (oidc.values.enabled && !oidcWasEnabled) {
			const ok = await confirm({
				title: t('settings.confirm.oidc_enable_title'),
				message: t('settings.confirm.oidc_enable'),
				confirmLabel: t('settings.oidc_enabled')
			});
			if (!ok) {
				oidc.values.enabled = false;
				return;
			}
		}
		// Turning it on fills the callback and console addresses when they are empty
		if (oidc.values.enabled) {
			if (!oidc.values.redirect_url.trim()) oidc.values.redirect_url = suggestedRedirect(location.origin);
			if (!oidc.values.panel_url.trim()) oidc.values.panel_url = location.origin;
		}
	}

	async function submitOidc(e: SubmitEvent) {
		e.preventDefault();
		if (oidc.busy) return;
		if (oidc.values.enabled) {
			if (!validOidcRedirect(oidc.values.redirect_url)) {
				// The form's check refuses it and says why at the field
				await oidc.submit();
				document.getElementById('oidc-redirect-url')?.focus();
				return;
			}
			const ok = await confirm({
				title: t('settings.confirm.oidc_save_title'),
				message: t('settings.confirm.oidc_save'),
				confirmLabel: t('settings.oidc_save')
			});
			if (!ok) return;
		}
		await oidc.submit();
	}

	async function runOidcTest() {
		if (oidcTesting) return;
		oidcTest = NO_LINE;
		const issuer = oidc.values.issuer_url.trim();
		if (!issuer) {
			oidcTest = { message: t('settings.oidc_issuer_required'), tone: 'error' };
			issuerEl?.focus();
			return;
		}
		oidcTesting = true;
		try {
			const r = await testOidc(issuer, oidc.values.allowed_private_cidrs.trim());
			if (r.success !== false && !r.error) {
				const found = [r.authorization_endpoint, r.token_endpoint, r.userinfo_endpoint].filter(Boolean);
				oidcTest = {
					message: [t('settings.oidc_test_success'), found.join(', ')].filter(Boolean).join(' '),
					tone: 'ok'
				};
			} else {
				oidcTest = { message: r.error || t('settings.oidc_test_failed'), tone: 'error' };
			}
		} catch (err) {
			oidcTest = { message: errText(err, t('settings.oidc_test_failed')), tone: 'error' };
		} finally {
			oidcTesting = false;
		}
	}

	/* ── Start ── */

	$effect(() =>
		trackDirty(
			() => enrollment.dirty || sessions.dirty || twoFactor.dirty || ldap.dirty || oidc.dirty,
			() => enrollment.busy || sessions.busy || twoFactor.busy || ldap.busy || oidc.busy
		)
	);

	// Each part loads what it shows
	$effect(() => {
		if (!canConfig) return;
		if (part === 'signin') {
			void loadSessions();
			void loadTwoFactor();
		}
		if (part === 'joining') void loadEnrollment();
		if (part === 'ldap') void loadLdap();
		if (part === 'sso') void loadOidc();
	});

	const toc = $derived(
		canConfig && part === 'signin'
			? [
					{ id: 'client-sessions-settings-section', label: t('settings.client_sessions_title') },
					{ id: 'require-two-factor', label: t('settings.signin.require_2fa') }
				]
			: undefined
	);

	const ROLES = $derived([
		{ value: 'viewer', label: t('settings.ldap_role_viewer') },
		{ value: 'operator', label: t('settings.ldap_role_operator') },
		{ value: 'admin', label: t('settings.ldap_role_admin') }
	]);
</script>

{#snippet onOff(value: boolean | null)}
	{#if value === null}<Empty />{:else}<BooleanStatus
			{value}
			on={t('settings.side.on')}
			off={t('settings.side.off')}
		/>{/if}
{/snippet}
{#snippet sideLdap()}{@render onOff(ldapOn)}{/snippet}
{#snippet sideOidc()}{@render onOff(oidcOn)}{/snippet}

{#snippet side()}
	<section class="section" aria-labelledby="auth-side-title" data-search-skip>
		<h2 id="auth-side-title">{t('settings.side.state_title')}</h2>
		<Facts
			items={part === 'ldap'
				? [{ label: t('settings.ldap_title'), value: sideLdap }]
				: [{ label: t('settings.oidc_title'), value: sideOidc }]}
		/>
	</section>
{/snippet}

{#snippet body()}
	{#if part === 'signin'}
		{#if canConfig}
			<section class="section" aria-labelledby="for-everyone-title">
				<h2 id="for-everyone-title">{t('settings.signin.for_everyone')}</h2>
				<p class="section-note">{t('settings.signin.for_everyone_note')}</p>
				<SettingsGrid>
					{#snippet start()}
						<form
							id="client-sessions-form"
							class="form-column settings-subgroup"
							onsubmit={sessions.onsubmit}
							novalidate
						>
							<h3 id="client-sessions-settings-section">{t('settings.client_sessions_title')}</h3>
							<p class="form-hint">{t('settings.client_sessions_desc')}</p>
							<Field
								id="client-sessions-expiry-days"
								label={t('settings.client_sessions_expiry_days')}
								hint={t('settings.client_sessions_expiry_hint')}
								width="short"
								error={sessions.errors.expiry_days}
							>
								{#snippet children(f)}
									<Input {...f} type="number" min="1" max="365" bind:value={sessions.values.expiry_days} />
								{/snippet}
							</Field>
							<Checkbox
								id="client-sessions-sliding"
								label={t('settings.client_sessions_sliding')}
								hint={t('settings.client_sessions_sliding_hint')}
								bind:checked={sessions.values.sliding}
								{...showWhenControl('client-sessions-max-group', sessions.values.sliding)}
							/>
							<ShowWhen id="client-sessions-max-group" when={sessions.values.sliding}>
								<Field
									id="client-sessions-max-days"
									label={t('settings.client_sessions_max_days')}
									hint={t('settings.client_sessions_max_days_hint')}
									width="short"
									error={sessions.errors.max_days}
								>
									{#snippet children(f)}
										<Input {...f} type="number" min="1" max="365" bind:value={sessions.values.max_days} />
									{/snippet}
								</Field>
							</ShowWhen>
							<p class="field-effect">{t('settings.signin.client_sessions_effect')}</p>
							<SaveBar
								dirty={sessions.dirty}
								busy={sessions.busy}
								status={sessions.status}
								label={t('settings.client_sessions_save')}
							/>
						</form>
					{/snippet}
					{#snippet end()}
						<form
							id={twoFactor.id}
							class="form-column settings-subgroup"
							onsubmit={twoFactor.onsubmit}
							novalidate
						>
							<Checkbox
								id="require-two-factor"
								label={t('settings.signin.require_2fa')}
								hint={t('settings.signin.require_2fa_hint')}
								bind:checked={twoFactor.values.required}
							/>
							<SaveBar form={twoFactor} label={t('actions.save')} />
						</form>

						<ConsoleSessionsForm />

						<ImpersonationMailForm />
					{/snippet}
				</SettingsGrid>
			</section>
		{/if}
	{:else if part === 'joining'}
		<section class="section" id="joining-mode-section" aria-labelledby="joining-mode-title">
			<h2 id="joining-mode-title">{t('tokens.enrollment_mode')}</h2>
			<form
				id="enrollment-settings-section"
				class="form-column settings-subgroup"
				onsubmit={enrollment.onsubmit}
				novalidate
			>
				<p class="form-hint">{t('settings.enrollment_desc')}</p>
				{#if enrolledMode}
					<p>
						{t('tokens.enrollment_mode')}: <strong>{t(`tokens.mode_${enrolledMode}`)}</strong>.
						{t(`tokens.mode_${enrolledMode}_desc`)}
						{#if canChangeMode}<a href={link('/devices/add?tab=tokens')}
								>{t('settings.enrollment_mode_link')}</a
							>{/if}
					</p>
				{/if}
				<Checkbox
					id="enrollment-rich-approve"
					label={t('settings.enrollment_rich_approve')}
					hint={t('settings.enrollment_rich_approve_hint')}
					bind:checked={enrollment.values.rich_approve}
				/>
				<Checkbox
					id="enrollment-tag-picker"
					label={t('settings.enrollment_tag_picker')}
					hint={t('settings.enrollment_tag_picker_hint')}
					bind:checked={enrollment.values.tag_picker}
				/>
				<p class="form-hint">
					{#if pendingCount > 0}<span id="enrollment-pending-hint"
							>{t('settings.enrollment_pending_count', { count: pendingCount })}</span
						>{/if}
					<a href={link('/registrations')}>{t('settings.enrollment_open_requests')}</a>
				</p>
				<p class="field-effect">{t('settings.signin.enrollment_effect')}</p>
				<SaveBar
					dirty={enrollment.dirty}
					busy={enrollment.busy}
					status={enrollment.status}
					label={t('settings.signin.enrollment_save')}
				/>
			</form>
		</section>
	{:else if part === 'ldap'}
		<section class="section" aria-labelledby="auth-panel-ldap">
			<form id="ldap-form" class="form-column settings-subgroup" onsubmit={submitLdap} novalidate>
				<h2 id="auth-panel-ldap">{t('settings.ldap_title')}</h2>
				<p class="form-hint">{t('settings.ldap_desc')}</p>
				<Checkbox
					id="ldap-enabled"
					label={t('settings.ldap_enabled')}
					hint={t('settings.signin.ldap_enabled_hint')}
					bind:checked={ldap.values.enabled}
					onchange={() => void onLdapEnabled()}
					{...showWhenControl('ldap-config-fields', ldap.values.enabled)}
				/>
				<ShowWhen id="ldap-config-fields" class="settings-subfields" when={ldap.values.enabled}>
					<h3>{t('settings.ldap_connection')}</h3>
					<Field id="ldap-host" label={t('settings.ldap_host')} error={ldap.errors.host}>
						{#snippet children(f)}<Input
								{...f}
								class="mono"
								placeholder={EX.ldapHost}
								bind:value={ldap.values.host}
							/>{/snippet}
					</Field>
					<Field id="ldap-port" label={t('settings.ldap_port')} width="short" error={ldap.errors.port}>
						{#snippet children(f)}<Input
								{...f}
								type="number"
								min="1"
								max="65535"
								placeholder={EX.ldapPort}
								bind:value={ldap.values.port}
							/>{/snippet}
					</Field>
					<Checkbox
						id="ldap-use-tls"
						label={t('settings.ldap_use_tls')}
						bind:checked={ldap.values.use_tls}
						onchange={onLdapTls}
					/>
					<Checkbox
						id="ldap-start-tls"
						label={t('settings.ldap_start_tls')}
						bind:checked={ldap.values.start_tls}
					/>
					<div id="ldap-advanced">
						<Disclosure title={t('settings.connection_mode_advanced')} level={3}>
							<div class="disclosure-body">
								<Checkbox
									id="ldap-skip-tls-verify"
									label={t('settings.ldap_skip_verify')}
									bind:checked={ldap.values.skip_tls_verify}
								/>
								<Field
									id="ldap-conn-timeout"
									label={t('settings.ldap_timeout')}
									hint={t('settings.ldap_timeout_hint')}
									width="short"
									error={ldap.errors.conn_timeout_sec}
								>
									{#snippet children(f)}<Input
											{...f}
											type="number"
											min="1"
											max="120"
											placeholder={EX.ldapTimeout}
											bind:value={ldap.values.conn_timeout_sec}
										/>{/snippet}
								</Field>
							</div>
						</Disclosure>
					</div>

					<h3>{t('settings.ldap_bind_mode')}</h3>
					<Checkbox
						id="ldap-direct-bind"
						label={t('settings.ldap_direct_bind')}
						hint={t('settings.ldap_direct_bind_hint')}
						bind:checked={ldap.values.direct_bind}
						aria-controls="ldap-bind-search-fields ldap-direct-bind-fields"
					/>
					<ShowWhen id="ldap-bind-search-fields" class="when-off" when={!ldap.values.direct_bind}>
						<Field
							id="ldap-bind-dn"
							label={t('settings.ldap_bind_dn')}
							hint={t('settings.ldap_bind_dn_hint')}
						>
							{#snippet children(f)}<Input
									{...f}
									class="mono"
									placeholder={EX.bindDn}
									bind:value={ldap.values.bind_dn}
								/>{/snippet}
						</Field>
						<Field id="ldap-bind-password" label={t('settings.ldap_bind_password')}>
							{#snippet children(f)}<Input
									{...f}
									type="password"
									autocomplete="new-password"
									bind:value={ldap.values.bind_password}
								/>{/snippet}
						</Field>
						<Field
							id="ldap-base-dn"
							label={t('settings.ldap_base_dn')}
							hint={t('settings.ldap_base_dn_hint')}
							error={ldap.errors.base_dn}
						>
							{#snippet children(f)}<Input
									{...f}
									class="mono"
									placeholder={EX.baseDn}
									bind:value={ldap.values.base_dn}
								/>{/snippet}
						</Field>
						<Field
							id="ldap-user-filter"
							label={t('settings.ldap_user_filter')}
							hint={t('settings.ldap_user_filter_hint')}
							error={ldap.errors.user_filter}
						>
							{#snippet children(f)}<Input
									{...f}
									class="mono"
									placeholder={EX.userFilter}
									bind:value={ldap.values.user_filter}
								/>{/snippet}
						</Field>
					</ShowWhen>
					<ShowWhen id="ldap-direct-bind-fields" when={ldap.values.direct_bind}>
						<Field
							id="ldap-direct-bind-dn"
							label={t('settings.ldap_direct_bind_dn')}
							hint={t('settings.ldap_direct_bind_dn_hint')}
							error={ldap.errors.direct_bind_dn}
						>
							{#snippet children(f)}<Input
									{...f}
									class="mono"
									placeholder={EX.directBindDn}
									bind:value={ldap.values.direct_bind_dn}
								/>{/snippet}
						</Field>
					</ShowWhen>

					<div id="ldap-attrs">
						<Disclosure title={t('settings.ldap_user_attrs')} level={3}>
							<div class="disclosure-body">
								<p class="form-hint">{t('settings.signin.attrs_note')}</p>
								<Field id="ldap-user-attr-id" label={t('settings.ldap_attr_id')} width="medium">
									{#snippet children(f)}<Input
											{...f}
											placeholder={EX.attrId}
											bind:value={ldap.values.user_attr_id}
										/>{/snippet}
								</Field>
								<Field id="ldap-user-attr-email" label={t('settings.ldap_attr_email')} width="medium">
									{#snippet children(f)}<Input
											{...f}
											placeholder={EX.attrEmail}
											bind:value={ldap.values.user_attr_email}
										/>{/snippet}
								</Field>
								<Field id="ldap-user-attr-name" label={t('settings.ldap_attr_name')} width="medium">
									{#snippet children(f)}<Input
											{...f}
											placeholder={EX.attrName}
											bind:value={ldap.values.user_attr_name}
										/>{/snippet}
								</Field>
							</div>
						</Disclosure>
					</div>

					<h3>{t('settings.ldap_group_mapping')}</h3>
					<Field
						id="ldap-group-base-dn"
						label={t('settings.ldap_group_base_dn')}
						hint={t('settings.ldap_group_base_dn_hint')}
						error={ldap.errors.group_base_dn}
					>
						{#snippet children(f)}<Input
								{...f}
								class="mono"
								placeholder={EX.groupBaseDn}
								bind:value={ldap.values.group_base_dn}
							/>{/snippet}
					</Field>
					<Field
						id="ldap-group-filter"
						label={t('settings.ldap_group_filter')}
						error={ldap.errors.group_filter}
					>
						{#snippet children(f)}<Input
								{...f}
								class="mono"
								placeholder={EX.groupFilter}
								bind:value={ldap.values.group_filter}
							/>{/snippet}
					</Field>
					<Field
						id="ldap-group-attr-name"
						label={t('settings.ldap_group_attr_name')}
						hint={t('settings.ldap_group_attr_name_hint')}
						width="medium"
					>
						{#snippet children(f)}<Input
								{...f}
								placeholder={EX.groupAttr}
								bind:value={ldap.values.group_attr_name}
							/>{/snippet}
					</Field>
					<Checkbox
						id="ldap-allow-signup"
						label={t('settings.ldap_allow_signup')}
						hint={t('settings.ldap_allow_signup_hint')}
						bind:checked={ldap.values.allow_signup}
					/>
					<Field
						id="ldap-default-role"
						label={t('settings.ldap_default_role')}
						hint={t('settings.ldap_default_role_hint')}
						width="medium"
						error={ldap.errors.default_role}
					>
						{#snippet children(f)}<Select
								{...f}
								options={ROLES}
								bind:value={ldap.values.default_role}
							/>{/snippet}
					</Field>
					<Field
						id="ldap-group-role-map"
						label={t('settings.ldap_group_role_map')}
						hint={t('settings.ldap_group_role_map_hint')}
						error={ldap.errors.group_role_map}
					>
						{#snippet children(f)}
							<Textarea
								{...f}
								class="mono"
								rows={4}
								placeholder={EX.ldapRoleMap}
								bind:value={ldap.values.group_role_map}
							/>
						{/snippet}
					</Field>
				</ShowWhen>
				<FormStatus message={ldapTest.message} tone={ldapTest.tone} class="test-result" />
				<p class="field-effect">{t('settings.signin.ldap_effect')}</p>
				<SaveBar dirty={ldap.dirty} busy={ldap.busy} status={ldap.status} label={t('settings.ldap_save')}>
					<Button id="ldap-test-btn" busy={ldapTesting} onclick={() => void runLdapTest()}
						>{ldapTesting ? t('settings.ldap_testing') : t('settings.ldap_test_connection')}</Button
					>
				</SaveBar>
			</form>
		</section>
	{:else if part === 'sso'}
		<section class="section" aria-labelledby="auth-panel-oidc">
			<form id="oidc-form" class="form-column settings-subgroup" onsubmit={submitOidc} novalidate>
				<h2 id="auth-panel-oidc">{t('settings.oidc_title')}</h2>
				<p class="form-hint">{t('settings.oidc_desc')}</p>
				<Checkbox
					id="oidc-enabled"
					label={t('settings.oidc_enabled')}
					hint={t('settings.oidc_enabled_hint')}
					bind:checked={oidc.values.enabled}
					onchange={() => void onOidcEnabled()}
					{...showWhenControl('oidc-config-fields', oidc.values.enabled)}
				/>
				<ShowWhen id="oidc-config-fields" class="settings-subfields" when={oidc.values.enabled}>
					<h3>{t('settings.oidc_provider')}</h3>
					<Field
						id="oidc-display-name"
						label={t('settings.oidc_display_name')}
						hint={t('settings.oidc_display_name_hint')}
						width="medium"
					>
						{#snippet children(f)}<Input
								{...f}
								placeholder={EX.oidcName}
								bind:value={oidc.values.display_name}
							/>{/snippet}
					</Field>
					<Field
						id="oidc-issuer-url"
						label={t('settings.oidc_issuer_url')}
						hint={t('settings.oidc_issuer_url_hint')}
						error={oidc.errors.issuer_url}
					>
						{#snippet children(f)}<Input
								{...f}
								type="url"
								class="mono"
								bind:element={issuerEl}
								placeholder={EX.issuer}
								bind:value={oidc.values.issuer_url}
							/>{/snippet}
					</Field>
					<Field id="oidc-client-id" label={t('settings.oidc_client_id')} error={oidc.errors.client_id}>
						{#snippet children(f)}<Input
								{...f}
								class="mono"
								autocomplete="off"
								bind:value={oidc.values.client_id}
							/>{/snippet}
					</Field>
					<Field id="oidc-client-secret" label={t('settings.oidc_client_secret')}>
						{#snippet children(f)}<Input
								{...f}
								type="password"
								autocomplete="new-password"
								bind:value={oidc.values.client_secret}
							/>{/snippet}
					</Field>
					<Field
						id="oidc-redirect-url"
						label={t('settings.oidc_redirect_url')}
						hint={t('settings.oidc_redirect_url_hint')}
						error={oidc.errors.redirect_url}
					>
						{#snippet children(f)}
							<Input
								{...f}
								type="url"
								class="mono"
								placeholder={EX.redirect}
								bind:value={oidc.values.redirect_url}
							/>
							{#if redirectWarning && !oidc.errors.redirect_url}<Notice
									tone="warning"
									role="status"
									id="oidc-redirect-url-warning"
									message={redirectWarning}
								/>{/if}
						{/snippet}
					</Field>
					<Field
						id="oidc-panel-url"
						label={t('settings.oidc_panel_url')}
						hint={t('settings.oidc_panel_url_hint')}
						error={oidc.errors.panel_url}
					>
						{#snippet children(f)}<Input
								{...f}
								type="url"
								class="mono"
								placeholder={EX.panel}
								bind:value={oidc.values.panel_url}
							/>{/snippet}
					</Field>
					<Field id="oidc-scopes" label={t('settings.oidc_scopes')}>
						{#snippet children(f)}<Input
								{...f}
								placeholder={EX.scopes}
								bind:value={oidc.values.scopes}
							/>{/snippet}
					</Field>
					<Checkbox
						id="oidc-use-pkce"
						label={t('settings.oidc_use_pkce')}
						bind:checked={oidc.values.use_pkce}
					/>
					<Checkbox
						id="oidc-allow-signup"
						label={t('settings.oidc_allow_signup')}
						bind:checked={oidc.values.allow_signup}
					/>
					<Checkbox
						id="oidc-prefer-sso"
						label={t('settings.oidc_prefer_sso')}
						hint={t('settings.oidc_prefer_sso_hint')}
						bind:checked={oidc.values.prefer_sso}
					/>

					<div id="oidc-advanced">
						<Disclosure title={t('settings.connection_mode_advanced')} level={3}>
							<div class="disclosure-body">
								<p class="form-hint">{t('settings.signin.oidc_advanced_note')}</p>
								<Checkbox
									id="oidc-auto-discovery"
									label={t('settings.oidc_auto_discovery')}
									bind:checked={oidc.values.auto_discovery}
									aria-controls="oidc-manual-endpoints"
								/>
								<ShowWhen id="oidc-manual-endpoints" class="when-off" when={!oidc.values.auto_discovery}>
									<h4>{t('settings.oidc_manual_endpoints')}</h4>
									<Field
										id="oidc-authorization-url"
										label={t('settings.oidc_authorization_url')}
										error={oidc.errors.authorization_url}
									>
										{#snippet children(f)}<Input
												{...f}
												type="url"
												class="mono"
												placeholder={EX.authUrl}
												bind:value={oidc.values.authorization_url}
											/>{/snippet}
									</Field>
									<Field
										id="oidc-token-url"
										label={t('settings.oidc_token_url')}
										error={oidc.errors.token_url}
									>
										{#snippet children(f)}<Input
												{...f}
												type="url"
												class="mono"
												placeholder={EX.tokenUrl}
												bind:value={oidc.values.token_url}
											/>{/snippet}
									</Field>
									<Field
										id="oidc-userinfo-url"
										label={t('settings.oidc_userinfo_url')}
										error={oidc.errors.userinfo_url}
									>
										{#snippet children(f)}<Input
												{...f}
												type="url"
												class="mono"
												placeholder={EX.userinfoUrl}
												bind:value={oidc.values.userinfo_url}
											/>{/snippet}
									</Field>
								</ShowWhen>
								<Field
									id="oidc-allowed-private-cidrs"
									label={t('settings.oidc_allowed_private_cidrs')}
									hint={t('settings.oidc_allowed_private_cidrs_hint')}
									error={oidc.errors.allowed_private_cidrs}
								>
									{#snippet children(f)}<Input
											{...f}
											class="mono"
											placeholder={EX.cidrs}
											bind:value={oidc.values.allowed_private_cidrs}
										/>{/snippet}
								</Field>
							</div>
						</Disclosure>
					</div>

					<div id="oidc-claims">
						<Disclosure title={t('settings.oidc_claim_mapping')} level={3}>
							<div class="disclosure-body">
								<p class="form-hint">{t('settings.signin.attrs_note')}</p>
								<Field id="oidc-claim-username" label={t('settings.oidc_claim_username')} width="medium">
									{#snippet children(f)}<Input
											{...f}
											placeholder={EX.claimUser}
											bind:value={oidc.values.claim_username}
										/>{/snippet}
								</Field>
								<Field id="oidc-claim-email" label={t('settings.oidc_claim_email')} width="medium">
									{#snippet children(f)}<Input
											{...f}
											placeholder={EX.claimEmail}
											bind:value={oidc.values.claim_email}
										/>{/snippet}
								</Field>
								<Field id="oidc-claim-name" label={t('settings.oidc_claim_name')} width="medium">
									{#snippet children(f)}<Input
											{...f}
											placeholder={EX.claimName}
											bind:value={oidc.values.claim_name}
										/>{/snippet}
								</Field>
								<Field id="oidc-claim-groups" label={t('settings.oidc_claim_groups')} width="medium">
									{#snippet children(f)}<Input
											{...f}
											placeholder={EX.claimGroups}
											bind:value={oidc.values.claim_groups}
										/>{/snippet}
								</Field>
							</div>
						</Disclosure>
					</div>

					<h3>{t('settings.oidc_role_mapping')}</h3>
					<Field
						id="oidc-default-role"
						label={t('settings.oidc_default_role')}
						width="medium"
						error={oidc.errors.default_role}
					>
						{#snippet children(f)}<Select
								{...f}
								options={ROLES}
								bind:value={oidc.values.default_role}
							/>{/snippet}
					</Field>
					<Field
						id="oidc-group-role-map"
						label={t('settings.oidc_group_role_map')}
						hint={t('settings.oidc_group_role_map_hint')}
						error={oidc.errors.group_role_map}
					>
						{#snippet children(f)}
							<Textarea
								{...f}
								class="mono"
								rows={3}
								placeholder={EX.oidcRoleMap}
								bind:value={oidc.values.group_role_map}
							/>
						{/snippet}
					</Field>
				</ShowWhen>
				<FormStatus message={oidcTest.message} tone={oidcTest.tone} class="test-result" />
				<p class="field-effect">{t('settings.signin.oidc_effect')}</p>
				<SaveBar dirty={oidc.dirty} busy={oidc.busy} status={oidc.status} label={t('settings.oidc_save')}>
					<Button id="oidc-test-btn" busy={oidcTesting} onclick={() => void runOidcTest()}
						>{oidcTesting ? t('settings.oidc_testing') : t('settings.oidc_test_discovery')}</Button
					>
				</SaveBar>
			</form>
		</section>
	{/if}
{/snippet}

{#if bare}
	{@render body()}
{:else}
	<SettingsLayout {toc} side={part === 'signin' ? undefined : side} sideLabelledby="auth-side-title" sticky>
		{@render body()}
	</SettingsLayout>
{/if}

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
	.settings-subgroup > :global(.form-group),
	.settings-subgroup :global(.settings-subfields > .form-group) {
		margin-bottom: 0;
	}
	.settings-subgroup :global(.settings-subfields) {
		gap: var(--space-3);
	}
	.settings-subgroup :global(.show-when .show-when) {
		margin-bottom: 0;
	}
	h3 {
		margin: 0;
		font-size: var(--text-md);
	}
	h4 {
		margin: var(--space-3) 0 0;
		font-size: var(--text-base, 1rem);
	}
	.form-hint,
	.field-effect {
		margin: 0;
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	.settings-subgroup :global(.test-result:empty) {
		display: none;
	}
	.disclosure-body {
		display: flex;
		flex-direction: column;
		gap: var(--space-3);
		padding: var(--space-3) 0 var(--space-2);
	}
	.disclosure-body > :global(.form-group) {
		margin-bottom: 0;
	}
</style>
