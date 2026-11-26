<!--
  Settings › Directory (LDAP) (server.config): sign-in with a directory
  such as Active Directory, as one draft with one save bar (PATTERN.md
  rule 1) and a filter, since it has more than 15 settings. Test
  connection is an action: it tries the values as they are on the page,
  saved or not. While directory sign-in is off, each group says once that
  its settings do nothing (rule 6); the fields stay editable, so a
  directory can be set up before it is turned on. The server reports no
  source for these settings: a row says "Set here" only for a stored
  value (lib/settings/sources.ts storedSource), and nothing claims a
  default. The TLS check and the timeout sit behind Advanced (structure
  plan, rows 35 and 36).

  Element ids are the old page's (the rows carry them), for links and the
  settings search.
-->
<script lang="ts">
	import { ApiError } from '#lib/api/client.ts';
	import { getLdap, saveLdap, testLdap } from '#lib/api/settings.ts';
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
	import type { PickOption } from '#lib/ui/settings/RecordPicker.svelte';
	import SettingRow from '#lib/ui/settings/SettingRow.svelte';
	import SettingsBody from '#lib/ui/settings/SettingsBody.svelte';
	import SettingsGroup from '#lib/ui/settings/SettingsGroup.svelte';
	import { SettingsDraft, saveAndConfirm } from '#lib/ui/settings/draft.svelte.ts';
	import { SettingsFilter } from '#lib/ui/settings/filter.svelte.ts';
	import RoleMapEditor from '../RoleMapEditor.svelte';
	import {
		formatRoleMap,
		ldapFromServer,
		ldapPayload,
		ldapProblems,
		parseRoleMap,
		roleMapProblems,
		type LdapValues,
		type RoleMapRow
	} from '../signin.ts';
	import { roleChoices, signInRoles } from '../signin-roles.ts';
	import { storedSource } from '../sources.ts';

	const canConfig = can('server.config');
	const errText = (err: unknown, fallback = t('errors.server_error')) =>
		err instanceof Error && err.message ? err.message : fallback;

	type Form = Omit<LdapValues, 'group_role_map'> & { group_role_map: RoleMapRow[]; [key: string]: unknown };
	type Field = keyof Form & string;

	const toForm = (v: LdapValues): Form => ({ ...v, group_role_map: parseRoleMap(v.group_role_map) });
	const toLdap = (f: Form): LdapValues => ({ ...f, group_role_map: formatRoleMap(f.group_role_map, '|') });

	const onOff = (v: unknown) => (v ? t('settings.side.on') : t('settings.side.off'));
	const secret = (v: unknown) => (v ? '••••••' : tf('settings_ui.empty', 'empty'));
	const mapWords = (v: unknown) =>
		(v as RoleMapRow[])
			.filter((r) => r.group.trim())
			.map((r) => `${r.group} → ${r.role}`)
			.join(', ') || tf('settings_ui.none', 'none');

	/** The settings in the order of the page, with their names */
	const LABELS: [Field, string, ((v: unknown) => string)?][] = [
		['enabled', 'settings.ldap_enabled', onOff],
		['host', 'settings.ldap_host'],
		['port', 'settings.ldap_port'],
		['use_tls', 'settings.ldap_use_tls', onOff],
		['start_tls', 'settings.ldap_start_tls', onOff],
		['skip_tls_verify', 'settings.ldap_skip_verify', onOff],
		['conn_timeout_sec', 'settings.ldap_timeout'],
		['direct_bind', 'settings.ldap_direct_bind', onOff],
		['bind_dn', 'settings.ldap_bind_dn'],
		['bind_password', 'settings.ldap_bind_password', secret],
		['base_dn', 'settings.ldap_base_dn'],
		['user_filter', 'settings.ldap_user_filter'],
		['direct_bind_dn', 'settings.ldap_direct_bind_dn'],
		['user_attr_id', 'settings.ldap_attr_id'],
		['user_attr_email', 'settings.ldap_attr_email'],
		['user_attr_name', 'settings.ldap_attr_name'],
		['group_base_dn', 'settings.ldap_group_base_dn'],
		['group_filter', 'settings.ldap_group_filter'],
		['group_attr_name', 'settings.ldap_group_attr_name'],
		['allow_signup', 'settings.ldap_allow_signup', onOff],
		['default_role', 'settings.ldap_default_role'],
		['group_role_map', 'settings.ldap_group_role_map', mapWords]
	];

	const draft = new SettingsDraft<Form>(
		toForm(ldapFromServer({})),
		Object.fromEntries(LABELS.map(([f, key, format]) => [f, { label: t(key), format }]))
	);
	$effect(() => draft.track());

	const filter = new SettingsFilter();

	let loaded = $state(false);
	let loadError = $state('');
	let roles = $state.raw<PickOption[]>([]);
	/** What the server refused, by field */
	let refused = $state<Partial<Record<string, string>>>({});
	let test = $state<{ message: string; tone: 'neutral' | 'ok' | 'error' }>({ message: '', tone: 'neutral' });
	let testing = $state(false);
	let advancedOpen = $state(false);
	let attrsOpen = $state(false);

	const v = $derived(draft.values);
	const roleNames = $derived(roles.map((r) => r.value));
	const problems = $derived<Partial<Record<string, string>>>(
		loaded ? ldapProblems(toLdap(v), roleNames.length ? roleNames : [v.default_role]) : {}
	);
	const mapRowProblems = $derived(roleMapProblems(v.group_role_map, roleNames));
	const errorOf = (f: Field) => problems[f] ?? refused[f] ?? '';

	async function load() {
		try {
			const [d, r] = await Promise.all([getLdap(), signInRoles()]);
			roles = r;
			draft.load(toForm(ldapFromServer(d)));
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

	// TLS on: the usual LDAPS port; off: the plain one
	function setTls(on: boolean) {
		set('use_tls', on);
		const port = Number(v.port);
		if (on && (port === 389 || !port)) set('port', 636);
		else if (!on && port === 636) set('port', 389);
	}

	/** "Set here" for a stored text; the server reports no source for the rest */
	const stored = (f: Field) => {
		const value = draft.loaded[f];
		return storedSource(typeof value === 'string' && value.trim() !== '');
	};

	async function save() {
		if (draft.busy) return;
		const wrong = LABELS.map(([f]) => f).filter((f) => problems[f]);
		if (wrong.length) {
			draft.error = tf(
				'settings_ui.fix_marked',
				'Some values are not right yet. They are marked on the page.'
			);
			if (wrong.some((f) => f === 'skip_tls_verify' || f === 'conn_timeout_sec')) advancedOpen = true;
			document.getElementById(`${ROW_IDS[wrong[0] as Field]}-control`)?.focus();
			return;
		}
		const turningOn = v.enabled && !draft.loaded.enabled;
		if (v.enabled) {
			const ok = await confirm(
				turningOn
					? {
							title: t('settings.confirm.ldap_enable_title'),
							message: t('settings.confirm.ldap_enable'),
							confirmLabel: t('settings.ldap_enabled')
						}
					: {
							title: t('settings.confirm.ldap_save_title'),
							message: t('settings.confirm.ldap_save'),
							confirmLabel: t('settings_ui.save_changes')
						}
			);
			if (!ok) return;
		}
		refused = {};
		const body = ldapPayload(toLdap($state.snapshot(draft.values) as Form));
		await saveAndConfirm(
			draft,
			async () => {
				try {
					await saveLdap(body);
				} catch (err) {
					const field = err instanceof ApiError ? (err.data as { field?: unknown } | null)?.field : undefined;
					if (typeof field === 'string' && field in ROW_IDS) refused = { [field]: errText(err) };
					throw err;
				}
			},
			{ reload: load }
		);
	}

	async function runTest() {
		if (testing) return;
		testing = true;
		test = { message: '', tone: 'neutral' };
		try {
			const r = await testLdap(ldapPayload(toLdap($state.snapshot(draft.values) as Form)));
			test =
				r.success !== false && !r.error
					? { message: t('settings.ldap_test_success'), tone: 'ok' }
					: { message: r.error || t('settings.ldap_test_failed'), tone: 'error' };
		} catch (err) {
			test = { message: errText(err, t('settings.ldap_test_failed')), tone: 'error' };
		} finally {
			testing = false;
		}
	}

	$effect(() => {
		if (canConfig) void load();
	});

	// A filter, a change or a refusal inside a folded part shows it
	$effect(() => {
		if (
			filter.active ||
			['skip_tls_verify', 'conn_timeout_sec'].some((f) => draft.isChanged(f) || refused[f])
		)
			advancedOpen = true;
		if (
			filter.active ||
			['user_attr_id', 'user_attr_email', 'user_attr_name'].some((f) => draft.isChanged(f))
		)
			attrsOpen = true;
	});

	const ROW_IDS: Record<Field, string> = {
		enabled: 'ldap-enabled',
		host: 'ldap-host',
		port: 'ldap-port',
		use_tls: 'ldap-use-tls',
		start_tls: 'ldap-start-tls',
		skip_tls_verify: 'ldap-skip-tls-verify',
		conn_timeout_sec: 'ldap-conn-timeout',
		direct_bind: 'ldap-direct-bind',
		bind_dn: 'ldap-bind-dn',
		bind_password: 'ldap-bind-password',
		base_dn: 'ldap-base-dn',
		user_filter: 'ldap-user-filter',
		direct_bind_dn: 'ldap-direct-bind-dn',
		user_attr_id: 'ldap-user-attr-id',
		user_attr_email: 'ldap-user-attr-email',
		user_attr_name: 'ldap-user-attr-name',
		group_base_dn: 'ldap-group-base-dn',
		group_filter: 'ldap-group-filter',
		group_attr_name: 'ldap-group-attr-name',
		allow_signup: 'ldap-allow-signup',
		default_role: 'ldap-default-role',
		group_role_map: 'ldap-group-role-map'
	};

	const sections = $derived([
		{ id: 'ldap-form', label: tf('settings.ldap.signin_title', 'Directory sign-in') },
		{ id: 'ldap-connection', label: t('settings.ldap_connection') },
		{ id: 'ldap-bind', label: t('settings.ldap_bind_mode') },
		{ id: 'ldap-groups', label: t('settings.ldap_group_mapping') }
	]);

	const notUsed = (direct: boolean) =>
		v.direct_bind === direct
			? undefined
			: direct
				? tf('settings.ldap.only_direct', 'Used only while Direct bind is on.')
				: tf('settings.ldap.not_direct', 'Not used while Direct bind is on.');

	function numberFrom(e: Event): number | null {
		const raw = (e.currentTarget as HTMLInputElement).value;
		return raw === '' ? null : Number(raw);
	}
</script>

{#snippet text(field: Field, label: string, help: string, opts: { mono?: boolean; password?: boolean } = {})}
	<SettingRow
		id={ROW_IDS[field] ?? field}
		{label}
		{help}
		changed={draft.isChanged(field)}
		source={opts.password ? null : stored(field)}
		error={errorOf(field)}
		noEffect={field === 'direct_bind_dn'
			? notUsed(true)
			: ['bind_dn', 'bind_password', 'base_dn', 'user_filter'].includes(field)
				? notUsed(false)
				: undefined}
	>
		{#snippet control(f)}
			<Input
				id={f.id}
				describedby={f.describedby}
				invalid={f.invalid}
				class={opts.mono ? 'mono' : undefined}
				type={opts.password ? 'password' : 'text'}
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
				'settings.ldap.off_note',
				'These settings do nothing while LDAP sign-in is off. You can fill them in and test the connection first.'
			)}
		/>{/if}
{/snippet}

{#snippet bar()}<ChangesBar {draft} onsave={save} />{/snippet}

{#if canConfig}
	<SettingsBody
		{filter}
		filterLabel={tf('settings.ldap.filter', 'Filter directory settings')}
		{sections}
		{bar}
	>
		{#if loadError && !loaded}
			<States kind="error" message={loadError} onretry={() => void load()} />
		{:else if !loaded}
			<States kind="loading" />
		{:else}
			<SettingsGroup
				id="ldap-form"
				title={tf('settings.ldap.signin_title', 'Directory sign-in')}
				description={tf(
					'settings.ldap.signin_note',
					'People sign in to the console with their account from your directory, such as Active Directory.'
				)}
				consequence={t('settings.signin.ldap_effect')}
			>
				{@render toggle('enabled', t('settings.ldap_enabled'), t('settings.signin.ldap_enabled_hint'))}
			</SettingsGroup>

			<SettingsGroup
				id="ldap-connection"
				title={t('settings.ldap_connection')}
				description={tf('settings.ldap.connection_note', 'Where the console reaches your directory server.')}
				note={offNote}
			>
				{#snippet actions()}
					<Button id="ldap-test-btn" size="sm" busy={testing} onclick={() => void runTest()}
						>{testing ? t('settings.ldap_testing') : t('settings.ldap_test_connection')}</Button
					>
				{/snippet}
				<div class="test-line"><FormStatus message={test.message} tone={test.tone} /></div>
				{@render text(
					'host',
					t('settings.ldap_host'),
					tf(
						'settings.ldap.host_help',
						'Host name or IP address of the directory server, without ldap:// and without a port. For example ldap.example.com.'
					),
					{ mono: true }
				)}
				<SettingRow
					id="ldap-port"
					label={t('settings.ldap_port')}
					help={tf('settings.ldap.port_help', 'Usually 389, or 636 with LDAPS. Empty: the usual port.')}
					changed={draft.isChanged('port')}
					error={errorOf('port')}
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
							oninput={(e) => set('port', numberFrom(e))}
						/>
					{/snippet}
				</SettingRow>
				{@render toggle(
					'use_tls',
					t('settings.ldap_use_tls'),
					tf(
						'settings.ldap.tls_help',
						'Connect with TLS from the start, usually on port 636. Turning it on or off moves the port along.'
					),
					setTls
				)}
				{@render toggle(
					'start_tls',
					t('settings.ldap_start_tls'),
					tf(
						'settings.ldap.start_tls_help',
						'Start without TLS on the usual port and switch to TLS before signing in.'
					)
				)}
				<div class="advanced" id="ldap-advanced">
					<Disclosure title={t('settings.connection_mode_advanced')} level={3} bind:open={advancedOpen}>
						{@render toggle(
							'skip_tls_verify',
							t('settings.ldap_skip_verify'),
							tf(
								'settings.ldap.skip_verify_help',
								'Accepts any certificate from the directory. Use it only to test with a self-signed certificate.'
							)
						)}
						<SettingRow
							id="ldap-conn-timeout"
							label={t('settings.ldap_timeout')}
							help={tf(
								'settings.ldap.timeout_help',
								'Seconds to wait for the directory, when connecting and for each answer. Empty: 10 seconds.'
							)}
							changed={draft.isChanged('conn_timeout_sec')}
							error={errorOf('conn_timeout_sec')}
						>
							{#snippet control(f)}
								<Input
									id={f.id}
									describedby={f.describedby}
									invalid={f.invalid}
									type="number"
									min="1"
									max="120"
									width="short"
									value={v.conn_timeout_sec}
									oninput={(e) => set('conn_timeout_sec', numberFrom(e))}
								/>
							{/snippet}
						</SettingRow>
					</Disclosure>
				</div>
			</SettingsGroup>

			<SettingsGroup
				id="ldap-bind"
				title={t('settings.ldap_bind_mode')}
				description={tf(
					'settings.ldap.bind_note',
					'Either a service account searches for the person, or the console signs in with a name built from what they typed.'
				)}
				note={offNote}
			>
				{@render toggle(
					'direct_bind',
					t('settings.ldap_direct_bind'),
					tf(
						'settings.ldap.direct_bind_help',
						'Sign in with a DN built from the name the person types, without a service account.'
					)
				)}
				{@render text(
					'bind_dn',
					t('settings.ldap_bind_dn'),
					tf(
						'settings.ldap.bind_dn_help',
						'A read-only account that searches for people. For example cn=readonly,dc=example,dc=com.'
					),
					{ mono: true }
				)}
				{@render text(
					'bind_password',
					t('settings.ldap_bind_password'),
					tf('settings.ldap.bind_password_help', 'The password of the service account.'),
					{ password: true }
				)}
				{@render text(
					'base_dn',
					t('settings.ldap_base_dn'),
					tf('settings.ldap.base_dn_help', 'Where people are searched. For example dc=example,dc=com.'),
					{ mono: true }
				)}
				{@render text(
					'user_filter',
					t('settings.ldap_user_filter'),
					tf(
						'settings.ldap.user_filter_help',
						'How a person is found by the name they type, with {{username}} where the name goes. Empty: (sAMAccountName={{username}}). For example (uid={{username}}).'
					),
					{ mono: true }
				)}
				{@render text(
					'direct_bind_dn',
					t('settings.ldap_direct_bind_dn'),
					tf(
						'settings.ldap.direct_bind_dn_help',
						'The DN to sign in with, with {{username}} where the name goes. For example uid={{username}},ou=users,dc=example,dc=com.'
					),
					{ mono: true }
				)}
				<div class="advanced" id="ldap-attrs">
					<Disclosure title={t('settings.ldap_user_attrs')} level={3} bind:open={attrsOpen}>
						<p class="fold-note">{t('settings.signin.attrs_note')}</p>
						{@render text(
							'user_attr_id',
							t('settings.ldap_attr_id'),
							tf(
								'settings.ldap.attr_id_help',
								'The attribute with the sign-in name. For example sAMAccountName or uid.'
							)
						)}
						{@render text(
							'user_attr_email',
							t('settings.ldap_attr_email'),
							tf('settings.ldap.attr_email_help', 'For example mail.')
						)}
						{@render text(
							'user_attr_name',
							t('settings.ldap_attr_name'),
							tf('settings.ldap.attr_name_help', 'For example displayName or cn.')
						)}
					</Disclosure>
				</div>
			</SettingsGroup>

			<SettingsGroup
				id="ldap-groups"
				title={t('settings.ldap_group_mapping')}
				description={tf('settings.ldap.groups_note', 'Which role people get, from the groups they are in.')}
				consequence={tf(
					'settings.ldap.groups_effect',
					'A person gets the role at each sign-in through the directory.'
				)}
				note={offNote}
			>
				{@render text(
					'group_base_dn',
					t('settings.ldap_group_base_dn'),
					tf(
						'settings.ldap.group_base_dn_help',
						'Where groups are searched. Empty: the Base DN. For example ou=groups,dc=example,dc=com.'
					),
					{ mono: true }
				)}
				{@render text(
					'group_filter',
					t('settings.ldap_group_filter'),
					tf(
						'settings.ldap.group_filter_help',
						"How a person's groups are found, with {{dn}} for the person's DN. For example (member={{dn}})."
					),
					{ mono: true }
				)}
				{@render text(
					'group_attr_name',
					t('settings.ldap_group_attr_name'),
					tf(
						'settings.ldap.group_attr_help',
						'Role map entries can name a group by this attribute, besides its DN or CN. For example cn.'
					)
				)}
				{@render toggle(
					'allow_signup',
					t('settings.ldap_allow_signup'),
					t('settings.ldap_allow_signup_hint')
				)}
				<SettingRow
					id="ldap-default-role"
					label={t('settings.ldap_default_role')}
					help={tf(
						'settings.ldap.default_role_help',
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
								name="ldap-default-role"
								labelledby={f.labelledby}
								describedby={f.describedby}
								bind:value={() => v.default_role, (r) => set('default_role', r)}
								choices={roleChoices('ldap-default-role', roles, v.default_role)}
							/>
						</div>
					{/snippet}
				</SettingRow>
				<SettingRow
					id="ldap-group-role-map"
					label={t('settings.ldap_group_role_map')}
					help={tf(
						'settings.ldap.role_map_help',
						'One entry per group: the group by its DN or name, and the role its people get. For example CN=NeoDesk Admins,OU=Groups,DC=example,DC=com with Administrator.'
					)}
					group
					changed={draft.isChanged('group_role_map')}
					source={draft.loaded.group_role_map.length ? storedSource(true) : null}
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
