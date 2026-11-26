<!--
  Settings › Single sign-on (server.config): sign-in with an OpenID
  Connect provider, as one draft with one save bar (PATTERN.md rule 1)
  and a filter, since it has more than 15 settings. Test discovery is an
  action: it asks the issuer on the page, saved or not. While single
  sign-on is off, each group says once that its settings do nothing; the
  private networks are checked anyway, as the server does. The server
  reports no source for these settings: a row says "Set here" only for a
  stored value, and nothing claims a default. The endpoints and the
  private networks sit behind Advanced (structure plan, rows 56 and 66
  to 68): they matter only without auto-discovery or for a provider in a
  private network.

  Element ids are the old page's (the rows carry them), for links and the
  settings search.
-->
<script lang="ts">
	import { ApiError } from '#lib/api/client.ts';
	import { getOidc, saveOidc, testOidc } from '#lib/api/settings.ts';
	import { t, tf } from '#lib/i18n/i18n.svelte.ts';
	import { can } from '#lib/state/session.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import ChoiceList from '#lib/ui/ChoiceList.svelte';
	import Disclosure from '#lib/ui/Disclosure.svelte';
	import FormStatus from '#lib/ui/FormStatus.svelte';
	import Input from '#lib/ui/Input.svelte';
	import Notice from '#lib/ui/Notice.svelte';
	import States from '#lib/ui/States.svelte';
	import { confirm } from '#lib/ui/dialogs.svelte.ts';
	import ChangesBar from '#lib/ui/settings/ChangesBar.svelte';
	import ListEditor from '#lib/ui/settings/ListEditor.svelte';
	import type { PickOption } from '#lib/ui/settings/RecordPicker.svelte';
	import SettingRow from '#lib/ui/settings/SettingRow.svelte';
	import SettingsBody from '#lib/ui/settings/SettingsBody.svelte';
	import SettingsGroup from '#lib/ui/settings/SettingsGroup.svelte';
	import { SettingsDraft, saveAndConfirm } from '#lib/ui/settings/draft.svelte.ts';
	import { SettingsFilter } from '#lib/ui/settings/filter.svelte.ts';
	import RoleMapEditor from '../RoleMapEditor.svelte';
	import {
		formatRoleMap,
		oidcFromServer,
		oidcPayload,
		oidcProblems,
		parseRoleMap,
		privateNetworkProblem,
		roleMapProblems,
		splitNetworks,
		suggestedRedirect,
		type OidcValues,
		type RoleMapRow
	} from '../signin.ts';
	import { roleChoices, signInRoles } from '../signin-roles.ts';
	import { storedSource } from '../sources.ts';

	const canConfig = can('server.config');
	const origin = location.origin;
	const errText = (err: unknown, fallback = t('errors.server_error')) =>
		err instanceof Error && err.message ? err.message : fallback;

	type Form = Omit<OidcValues, 'group_role_map' | 'allowed_private_cidrs'> & {
		group_role_map: RoleMapRow[];
		allowed_private_cidrs: string[];
		[key: string]: unknown;
	};
	type Field = keyof Form & string;

	const toForm = (v: OidcValues): Form => ({
		...v,
		group_role_map: parseRoleMap(v.group_role_map),
		allowed_private_cidrs: splitNetworks(v.allowed_private_cidrs)
	});
	const toOidc = (f: Form): OidcValues => ({
		...f,
		group_role_map: formatRoleMap(f.group_role_map, '\n'),
		allowed_private_cidrs: f.allowed_private_cidrs.join(', ')
	});

	const onOff = (v: unknown) => (v ? t('settings.side.on') : t('settings.side.off'));
	const secret = (v: unknown) => (v ? '••••••' : tf('settings_ui.empty', 'empty'));
	const mapWords = (v: unknown) =>
		(v as RoleMapRow[])
			.filter((r) => r.group.trim())
			.map((r) => `${r.group} → ${r.role}`)
			.join(', ') || tf('settings_ui.none', 'none');

	const ROW_IDS: Record<Field, string> = {
		enabled: 'oidc-enabled',
		display_name: 'oidc-display-name',
		issuer_url: 'oidc-issuer-url',
		client_id: 'oidc-client-id',
		client_secret: 'oidc-client-secret',
		redirect_url: 'oidc-redirect-url',
		panel_url: 'oidc-panel-url',
		scopes: 'oidc-scopes',
		use_pkce: 'oidc-use-pkce',
		auto_discovery: 'oidc-auto-discovery',
		authorization_url: 'oidc-authorization-url',
		token_url: 'oidc-token-url',
		userinfo_url: 'oidc-userinfo-url',
		allowed_private_cidrs: 'oidc-allowed-private-cidrs',
		allow_signup: 'oidc-allow-signup',
		prefer_sso: 'oidc-prefer-sso',
		claim_username: 'oidc-claim-username',
		claim_email: 'oidc-claim-email',
		claim_name: 'oidc-claim-name',
		claim_groups: 'oidc-claim-groups',
		default_role: 'oidc-default-role',
		group_role_map: 'oidc-group-role-map'
	};
	const LABELS: Record<Field, [string, ((v: unknown) => string)?]> = {
		enabled: ['settings.oidc_enabled', onOff],
		display_name: ['settings.oidc_display_name'],
		issuer_url: ['settings.oidc_issuer_url'],
		client_id: ['settings.oidc_client_id'],
		client_secret: ['settings.oidc_client_secret', secret],
		redirect_url: ['settings.oidc_redirect_url'],
		panel_url: ['settings.oidc_panel_url'],
		scopes: ['settings.oidc_scopes'],
		use_pkce: ['settings.oidc_use_pkce', onOff],
		auto_discovery: ['settings.oidc_auto_discovery', onOff],
		authorization_url: ['settings.oidc_authorization_url'],
		token_url: ['settings.oidc_token_url'],
		userinfo_url: ['settings.oidc_userinfo_url'],
		allowed_private_cidrs: ['settings.oidc_allowed_private_cidrs'],
		allow_signup: ['settings.oidc_allow_signup', onOff],
		prefer_sso: ['settings.oidc_prefer_sso', onOff],
		claim_username: ['settings.oidc_claim_username'],
		claim_email: ['settings.oidc_claim_email'],
		claim_name: ['settings.oidc_claim_name'],
		claim_groups: ['settings.oidc_claim_groups'],
		default_role: ['settings.oidc_default_role'],
		group_role_map: ['settings.oidc_group_role_map', mapWords]
	};

	const draft = new SettingsDraft<Form>(
		toForm(oidcFromServer({}, origin)),
		Object.fromEntries(Object.entries(LABELS).map(([f, [key, format]]) => [f, { label: t(key), format }]))
	);
	$effect(() => draft.track());

	const filter = new SettingsFilter();

	let loaded = $state(false);
	let loadError = $state('');
	let roles = $state.raw<PickOption[]>([]);
	let refused = $state<Partial<Record<string, string>>>({});
	let test = $state<{ message: string; tone: 'neutral' | 'ok' | 'error' }>({ message: '', tone: 'neutral' });
	let testing = $state(false);
	let advancedOpen = $state(false);
	let claimsOpen = $state(false);

	const v = $derived(draft.values);
	const roleNames = $derived(roles.map((r) => r.value));
	const problems = $derived<Partial<Record<string, string>>>(
		loaded ? oidcProblems(toOidc(v), roleNames.length ? roleNames : [v.default_role]) : {}
	);
	const mapRowProblems = $derived(roleMapProblems(v.group_role_map, roleNames));
	const errorOf = (f: Field) =>
		f === 'allowed_private_cidrs' ? (refused[f] ?? '') : (problems[f] ?? refused[f] ?? '');

	async function load() {
		try {
			const [d, r] = await Promise.all([getOidc(), signInRoles()]);
			roles = r;
			draft.load(toForm(oidcFromServer(d, origin)));
			refused = {};
			loadError = '';
			loaded = true;
		} catch (err) {
			loadError = errText(err);
		}
	}

	function set<K extends Field>(field: K, value: Form[K]) {
		draft.set(field, value);
		if (refused[field]) refused = { ...refused, [field]: undefined };
	}

	// Turning it on fills the callback and console addresses when they are empty
	function setEnabled(on: boolean) {
		set('enabled', on);
		if (!on) return;
		if (!v.redirect_url.trim()) set('redirect_url', suggestedRedirect(origin));
		if (!v.panel_url.trim()) set('panel_url', origin);
	}

	const stored = (f: Field) => {
		const value = draft.loaded[f];
		return storedSource(
			(typeof value === 'string' && value.trim() !== '') || (Array.isArray(value) && value.length > 0)
		);
	};

	const ADVANCED: readonly Field[] = [
		'auto_discovery',
		'authorization_url',
		'token_url',
		'userinfo_url',
		'allowed_private_cidrs'
	];
	const CLAIMS: readonly Field[] = ['claim_username', 'claim_email', 'claim_name', 'claim_groups'];

	async function save() {
		if (draft.busy) return;
		const wrong = (Object.keys(ROW_IDS) as Field[]).filter((f) => problems[f]);
		if (wrong.length) {
			draft.error = tf(
				'settings_ui.fix_marked',
				'Some values are not right yet. They are marked on the page.'
			);
			if (wrong.some((f) => ADVANCED.includes(f))) advancedOpen = true;
			const first = wrong[0] as Field;
			document
				.getElementById(
					first === 'allowed_private_cidrs' ? `${ROW_IDS[first]}-control-0` : `${ROW_IDS[first]}-control`
				)
				?.focus();
			return;
		}
		if (v.enabled) {
			const turningOn = !draft.loaded.enabled;
			const ok = await confirm(
				turningOn
					? {
							title: t('settings.confirm.oidc_enable_title'),
							message: t('settings.confirm.oidc_enable'),
							confirmLabel: t('settings.oidc_enabled')
						}
					: {
							title: t('settings.confirm.oidc_save_title'),
							message: t('settings.confirm.oidc_save'),
							confirmLabel: t('settings_ui.save_changes')
						}
			);
			if (!ok) return;
		}
		refused = {};
		const body = oidcPayload(toOidc($state.snapshot(draft.values) as Form), origin);
		await saveAndConfirm(
			draft,
			async () => {
				try {
					await saveOidc(body);
				} catch (err) {
					const field = err instanceof ApiError ? (err.data as { field?: unknown } | null)?.field : undefined;
					if (typeof field === 'string' && field in ROW_IDS) {
						refused = { [field]: errText(err) };
						if (ADVANCED.includes(field as Field)) advancedOpen = true;
					}
					throw err;
				}
			},
			{ reload: load }
		);
	}

	async function runTest() {
		if (testing) return;
		test = { message: '', tone: 'neutral' };
		const issuer = v.issuer_url.trim();
		if (!issuer) {
			test = { message: t('settings.oidc_issuer_required'), tone: 'error' };
			document.getElementById('oidc-issuer-url-control')?.focus();
			return;
		}
		testing = true;
		try {
			const r = await testOidc(issuer, v.allowed_private_cidrs.join(', '));
			if (r.success !== false && !r.error) {
				const found = [r.authorization_endpoint, r.token_endpoint, r.userinfo_endpoint].filter(Boolean);
				test = {
					message: [t('settings.oidc_test_success'), found.join(', ')].filter(Boolean).join(' '),
					tone: 'ok'
				};
			} else {
				test = { message: r.error || t('settings.oidc_test_failed'), tone: 'error' };
			}
		} catch (err) {
			test = { message: errText(err, t('settings.oidc_test_failed')), tone: 'error' };
		} finally {
			testing = false;
		}
	}

	$effect(() => {
		if (canConfig) void load();
	});

	// A filter, a change or a refusal inside a folded part shows it
	$effect(() => {
		if (filter.active || ADVANCED.some((f) => draft.isChanged(f) || refused[f])) advancedOpen = true;
		if (filter.active || CLAIMS.some((f) => draft.isChanged(f))) claimsOpen = true;
	});

	const sections = $derived([
		{ id: 'oidc-form', label: tf('settings.sso.signin_title', 'Single sign-on') },
		{ id: 'oidc-provider', label: t('settings.oidc_provider') },
		{ id: 'oidc-accounts', label: tf('settings.sso.accounts_title', 'Accounts') },
		{ id: 'oidc-roles', label: t('settings.oidc_role_mapping') }
	]);

	const manualNote = $derived(
		v.auto_discovery
			? tf(
					'settings.sso.manual_unused',
					'Used only while Auto-discovery is off, or when the provider has no discovery document.'
				)
			: undefined
	);
</script>

{#snippet text(
	field: Field,
	label: string,
	help: string,
	opts: { mono?: boolean; password?: boolean; url?: boolean; noEffect?: string } = {}
)}
	<SettingRow
		id={ROW_IDS[field] ?? field}
		{label}
		{help}
		changed={draft.isChanged(field)}
		source={opts.password ? null : stored(field)}
		error={errorOf(field)}
		noEffect={opts.noEffect}
	>
		{#snippet control(f)}
			<Input
				id={f.id}
				describedby={f.describedby}
				invalid={f.invalid}
				class={opts.mono ? 'mono' : undefined}
				type={opts.password ? 'password' : opts.url ? 'url' : 'text'}
				inputmode={opts.url ? 'url' : undefined}
				autocomplete={opts.password ? 'new-password' : 'off'}
				spellcheck={false}
				value={String(v[field] ?? '')}
				oninput={(e) => set(field, (e.currentTarget as HTMLInputElement).value as never)}
			/>
		{/snippet}
	</SettingRow>
{/snippet}

{#snippet toggle(field: Field, label: string, help: string, onchange?: (on: boolean) => void)}
	<SettingRow
		id={ROW_IDS[field] ?? field}
		{label}
		{help}
		checked={!!v[field]}
		onchange={onchange ?? ((on) => set(field, on as never))}
		changed={draft.isChanged(field)}
	/>
{/snippet}

{#snippet offNote()}
	{#if !v.enabled}<Notice
			message={tf(
				'settings.sso.off_note',
				'These settings do nothing while single sign-on is off. You can fill them in and test discovery first.'
			)}
		/>{/if}
{/snippet}

{#snippet bar()}<ChangesBar {draft} onsave={save} />{/snippet}

{#if canConfig}
	<SettingsBody
		{filter}
		filterLabel={tf('settings.sso.filter', 'Filter single sign-on settings')}
		{sections}
		{bar}
	>
		{#if loadError && !loaded}
			<States kind="error" message={loadError} onretry={() => void load()} />
		{:else if !loaded}
			<States kind="loading" />
		{:else}
			<SettingsGroup
				id="oidc-form"
				title={tf('settings.sso.signin_title', 'Single sign-on')}
				description={tf(
					'settings.sso.signin_note',
					'People sign in to the console with an account from an identity provider such as Microsoft Entra ID, Okta, Google or Keycloak (OpenID Connect).'
				)}
				consequence={t('settings.signin.oidc_effect')}
			>
				{@render toggle(
					'enabled',
					t('settings.oidc_enabled'),
					tf(
						'settings.sso.enabled_help',
						'The sign-in page gets a button for the provider. Turning it on fills in the redirect URL and the console address when they are empty.'
					),
					setEnabled
				)}
			</SettingsGroup>

			<SettingsGroup
				id="oidc-provider"
				title={t('settings.oidc_provider')}
				description={tf(
					'settings.sso.provider_note',
					'What the provider gave you when you registered the console with it.'
				)}
				note={offNote}
			>
				{#snippet actions()}
					<Button id="oidc-test-btn" size="sm" busy={testing} onclick={() => void runTest()}
						>{testing ? t('settings.oidc_testing') : t('settings.oidc_test_discovery')}</Button
					>
				{/snippet}
				<div class="test-line"><FormStatus message={test.message} tone={test.tone} /></div>
				{@render text(
					'display_name',
					t('settings.oidc_display_name'),
					tf(
						'settings.sso.display_name_help',
						'The text on the sign-in button. For example Microsoft or Okta.'
					)
				)}
				{@render text(
					'issuer_url',
					t('settings.oidc_issuer_url'),
					tf(
						'settings.sso.issuer_help',
						'The provider address that hands out its settings. For example https://login.microsoftonline.com/your-tenant/v2.0.'
					),
					{ mono: true, url: true }
				)}
				{@render text(
					'client_id',
					t('settings.oidc_client_id'),
					tf('settings.sso.client_id_help', 'The ID the provider gave the console.'),
					{ mono: true }
				)}
				{@render text(
					'client_secret',
					t('settings.oidc_client_secret'),
					tf('settings.sso.client_secret_help', 'The secret the provider gave the console.'),
					{ password: true }
				)}
				{@render text(
					'redirect_url',
					t('settings.oidc_redirect_url'),
					tf(
						'settings.sso.redirect_help',
						'Where the provider sends people back. Enter the same address at the provider. It ends in /api/auth/oidc/callback, for example {url}.',
						{ url: suggestedRedirect(origin) }
					),
					{ mono: true, url: true }
				)}
				{@render text(
					'panel_url',
					t('settings.oidc_panel_url'),
					tf(
						'settings.sso.panel_help',
						'The address people open the console at, after a reverse proxy if there is one. For example {url}.',
						{ url: origin }
					),
					{ mono: true, url: true }
				)}
				{@render text(
					'scopes',
					t('settings.oidc_scopes'),
					tf(
						'settings.sso.scopes_help',
						'What the console asks the provider for, separated by spaces. Usually openid profile email.'
					),
					{ mono: true }
				)}
				{@render toggle(
					'use_pkce',
					t('settings.oidc_use_pkce'),
					tf(
						'settings.sso.pkce_help',
						'Protects the sign-in against a stolen code. Turn it off only if the provider refuses it.'
					)
				)}
				<div class="advanced" id="oidc-advanced">
					<Disclosure title={t('settings.connection_mode_advanced')} level={3} bind:open={advancedOpen}>
						<p class="fold-note">{t('settings.signin.oidc_advanced_note')}</p>
						{@render toggle(
							'auto_discovery',
							t('settings.oidc_auto_discovery'),
							tf(
								'settings.sso.discovery_help',
								'The console reads the endpoints from the issuer. Off: enter them below.'
							)
						)}
						<div id="oidc-manual-endpoints">
							{@render text(
								'authorization_url',
								t('settings.oidc_authorization_url'),
								tf('settings.sso.auth_url_help', 'For example https://idp.example.com/authorize.'),
								{ mono: true, url: true, noEffect: manualNote }
							)}
							{@render text(
								'token_url',
								t('settings.oidc_token_url'),
								tf('settings.sso.token_url_help', 'For example https://idp.example.com/token.'),
								{ mono: true, url: true, noEffect: manualNote }
							)}
							{@render text(
								'userinfo_url',
								t('settings.oidc_userinfo_url'),
								tf('settings.sso.userinfo_url_help', 'For example https://idp.example.com/userinfo.'),
								{ mono: true, url: true, noEffect: manualNote }
							)}
						</div>
						<SettingRow
							id="oidc-allowed-private-cidrs"
							label={t('settings.oidc_allowed_private_cidrs')}
							help={tf(
								'settings.sso.cidrs_help',
								'For a provider on your own network: one address or range per row. For example 10.20.0.0/16 or 192.168.50.10. Empty: the console reaches no private address.'
							)}
							group
							changed={draft.isChanged('allowed_private_cidrs')}
							source={stored('allowed_private_cidrs')}
							error={errorOf('allowed_private_cidrs')}
						>
							{#snippet control(f)}
								<ListEditor
									id={f.id}
									describedby={f.describedby}
									items={v.allowed_private_cidrs}
									onchange={(list) => set('allowed_private_cidrs', list)}
									itemLabel={(n) => tf('settings.sso.network_n', 'Private network {n}', { n })}
									check={privateNetworkProblem}
									addLabel={tf('settings.sso.network_add', 'Add a network')}
								/>
							{/snippet}
						</SettingRow>
					</Disclosure>
				</div>
			</SettingsGroup>

			<SettingsGroup
				id="oidc-accounts"
				title={tf('settings.sso.accounts_title', 'Accounts')}
				description={tf('settings.sso.accounts_note', 'Who gets an account and how the sign-in page starts.')}
				note={offNote}
			>
				{@render toggle(
					'allow_signup',
					t('settings.oidc_allow_signup'),
					tf(
						'settings.sso.signup_help',
						'A person the provider accepts gets an account at their first sign-in. Off: only people who already have an account can sign in this way.'
					)
				)}
				{@render toggle(
					'prefer_sso',
					t('settings.oidc_prefer_sso'),
					tf(
						'settings.sso.prefer_help',
						'The sign-in page goes to the provider at once. To sign in with a password instead, open /login?autoLaunch=0.'
					)
				)}
				<div class="advanced" id="oidc-claims">
					<Disclosure title={t('settings.oidc_claim_mapping')} level={3} bind:open={claimsOpen}>
						<p class="fold-note">{t('settings.signin.attrs_note')}</p>
						{@render text(
							'claim_username',
							t('settings.oidc_claim_username'),
							tf('settings.sso.claim_user_help', 'For example preferred_username.')
						)}
						{@render text(
							'claim_email',
							t('settings.oidc_claim_email'),
							tf('settings.sso.claim_email_help', 'For example email.')
						)}
						{@render text(
							'claim_name',
							t('settings.oidc_claim_name'),
							tf('settings.sso.claim_name_help', 'For example name.')
						)}
						{@render text(
							'claim_groups',
							t('settings.oidc_claim_groups'),
							tf(
								'settings.sso.claim_groups_help',
								'The claim with the groups for the role map below. For example groups.'
							)
						)}
					</Disclosure>
				</div>
			</SettingsGroup>

			<SettingsGroup
				id="oidc-roles"
				title={t('settings.oidc_role_mapping')}
				description={tf(
					'settings.sso.roles_note',
					'Which role people get, from their groups at the provider.'
				)}
				consequence={tf(
					'settings.sso.roles_effect',
					'A person gets the role at each sign-in through the provider.'
				)}
				note={offNote}
			>
				<SettingRow
					id="oidc-default-role"
					label={t('settings.oidc_default_role')}
					help={tf(
						'settings.sso.default_role_help',
						'The role of people whose groups match no entry of the map below.'
					)}
					group
					changed={draft.isChanged('default_role')}
					source={stored('default_role')}
					error={errorOf('default_role')}
				>
					{#snippet control(f)}
						<div id={f.id}>
							<ChoiceList
								name="oidc-default-role"
								labelledby={f.labelledby}
								describedby={f.describedby}
								bind:value={() => v.default_role, (r) => set('default_role', r)}
								choices={roleChoices('oidc-default-role', roles, v.default_role)}
							/>
						</div>
					{/snippet}
				</SettingRow>
				<SettingRow
					id="oidc-group-role-map"
					label={t('settings.oidc_group_role_map')}
					help={tf(
						'settings.sso.role_map_help',
						'One entry per group: the group as the provider names it, and the role its people get. For example neodesk-admins with Administrator.'
					)}
					group
					changed={draft.isChanged('group_role_map')}
					source={stored('group_role_map')}
					error={refused.group_role_map ?? ''}
				>
					{#snippet control(f)}
						<RoleMapEditor
							id={f.id}
							describedby={f.describedby}
							rows={v.group_role_map}
							{roles}
							problems={mapRowProblems}
							onchange={(rows) => set('group_role_map', rows)}
							groupLabel={(n) => tf('settings.signin.map_group_n', 'Group {n}', { n })}
							roleLabel={(n) => tf('settings.signin.map_role_n', 'Role for group {n}', { n })}
							addLabel={tf('settings.signin.map_add', 'Add a group')}
						/>
					{/snippet}
				</SettingRow>
			</SettingsGroup>
		{/if}
	</SettingsBody>
{/if}

<style>
	.advanced {
		padding: var(--space-2) var(--row-pad-x) 0;
	}
	.advanced :global(.setting-row:last-child) {
		border-bottom: 0;
	}
	.fold-note {
		margin: var(--space-2) 0;
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	.test-line {
		padding: 0 var(--row-pad-x);
	}
	.test-line :global(.form-status:empty) {
		display: none;
	}
</style>
