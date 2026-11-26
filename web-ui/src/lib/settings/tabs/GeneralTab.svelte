<!--
  Settings › General. With server.config: the public addresses clients use
  and the connection strategy, as one draft with one save bar (PATTERN.md
  rule 1). Save sends the parts that changed to their own endpoints, one
  after the other (lib/settings/general.ts); a part that fails keeps its
  changes in the bar and the message says which part was saved and which
  not, and why. Each row says where its value comes from
  (lib/settings/sources.ts); an address the environment locks can't be
  changed here. The summary column shows the addresses clients get now and
  the server information. Without server.config only the server
  information.

  Element ids are the old page's (the rows carry them): links
  (/settings#public-server-id) and the settings search (search-index.ts)
  point at them.
-->
<script lang="ts">
	import {
		getClientConfig,
		getConnectionMode,
		getPublicEndpoints,
		getServerInfo,
		getSettingSources,
		saveConnectionMode,
		savePublicEndpoints,
		type ClientConfig,
		type ServerInfo,
		type SettingSources
	} from '#lib/api/settings.ts';
	import type { RestartItem } from '#lib/api/restart.ts';
	import { ApiError } from '#lib/api/client.ts';
	import GuideLink from '#lib/guide/GuideLink.svelte';
	import { t, tf } from '#lib/i18n/i18n.svelte.ts';
	import { formatDuration } from '#lib/i18n/format.ts';
	import { link } from '#lib/nav.ts';
	import { can, session } from '#lib/state/session.svelte.ts';
	import ChoiceList from '#lib/ui/ChoiceList.svelte';
	import ConnectionDiagram from '#lib/ui/ConnectionDiagram.svelte';
	import Disclosure from '#lib/ui/Disclosure.svelte';
	import Facts from '#lib/ui/Facts.svelte';
	import IdFacts from '#lib/settings-b/IdFacts.svelte';
	import Input from '#lib/ui/Input.svelte';
	import Notice from '#lib/ui/Notice.svelte';
	import ReadOnlyHint from '#lib/ui/ReadOnlyHint.svelte';
	import States from '#lib/ui/States.svelte';
	import Tag from '#lib/ui/Tag.svelte';
	import ValueCopy from '#lib/ui/ValueCopy.svelte';
	import Version from '#lib/ui/Version.svelte';
	import { confirm } from '#lib/ui/dialogs.svelte.ts';
	import ChangesBar from '#lib/ui/settings/ChangesBar.svelte';
	import SettingRow, { type ValueSource } from '#lib/ui/settings/SettingRow.svelte';
	import SettingsBody from '#lib/ui/settings/SettingsBody.svelte';
	import SettingsGroup from '#lib/ui/settings/SettingsGroup.svelte';
	import { SettingsDraft, saveAndConfirm } from '#lib/ui/settings/draft.svelte.ts';
	import {
		ENDPOINT_VARIABLES,
		GENERAL_DEFAULTS,
		changedParts,
		connectionFacts,
		connectionNotes,
		endpointSourceLabel,
		generalProblems,
		partFields,
		saveInParts,
		splitHostPort,
		type GeneralPart,
		type GeneralValues
	} from '../general.ts';
	import { onRestartCanceled, showRestartPrompt } from '../restart.svelte.ts';
	import { sourceInfo, sourceRow, type SourceInfo } from '../sources.ts';

	const canConfig = can('server.config');
	const errText = (err: unknown) =>
		err instanceof Error && err.message ? err.message : t('errors.server_error');

	/* ── Server information (versions for everyone, the rest with server.config) ── */

	let info = $state.raw<ServerInfo | null>(null);
	let infoError = $state('');

	async function loadInfo() {
		try {
			info = await getServerInfo();
			infoError = '';
		} catch (err) {
			infoError = errText(err);
		}
	}

	const goVersion = $derived(info?.goServer?.version ?? '');
	const isDevBuild = $derived(goVersion === 'dev' && !!info?.app?.version && info.app.version !== 'dev');
	const uptime = $derived(Number(info?.server?.uptime ?? 0));

	/* ── The draft: the addresses and the connection strategy, one save ── */

	const onOff = (v: unknown) => (v ? t('settings.side.on') : t('settings.side.off'));
	const modeWords = (v: unknown) =>
		v === 'relay_only' ? t('settings.general.conn_relay') : t('settings.general.conn_p2p');
	const orAuto = (v: unknown) =>
		String(v ?? '').trim() ? String(v) : tf('settings.general.auto', 'found automatically');

	const draft = new SettingsDraft<GeneralValues>(structuredClone(GENERAL_DEFAULTS), {
		public_server_id: { label: t('settings.public_endpoints_server_id'), format: orAuto },
		public_relay_server: { label: t('settings.public_endpoints_relay'), format: orAuto },
		public_api_url: { label: t('settings.public_endpoints_api_url'), format: orAuto },
		mode: { label: t('settings.connection_mode_label'), format: modeWords },
		p2p_fallback_ms: { label: t('settings.general.conn_fallback_label') },
		same_nat_relay: { label: t('settings.general.conn_same_nat'), format: onOff },
		allow_shared_nat_initiator: { label: t('settings.general.conn_shared_nat'), format: onOff },
		logged_in_only_initiator: { label: t('settings.general.conn_logged_in'), format: onOff },
		operator_only_outbound: { label: t('settings.general.conn_operator_only'), format: onOff }
	});
	$effect(() => draft.track());

	let loaded = $state(false);
	let loadError = $state('');
	/** What a save refused, by field (the server names the address it refused) */
	let refused = $state<Partial<Record<string, string>>>({});
	let advancedOpen = $state(false);

	const v = $derived(draft.values);
	const problems = $derived(generalProblems(v));
	const notes = $derived(connectionNotes(v));
	const errorOf = (field: string) => problems[field] ?? refused[field] ?? '';

	// Connection strategy: whether this console can store it, what the server runs with
	let connWritable = $state(false);
	let connProblem = $state('');
	let connRuntime = $state('');
	/** Addresses the server's environment sets at every start (the older answer, without sources) */
	let envLocked = $state.raw<readonly string[]>([]);
	let sources = $state.raw<SettingSources>({});

	const src = (key: string): SourceInfo => sourceInfo(sources[key]);

	/** A row's source: an empty address is the one found automatically */
	function endpointRow(field: 'public_server_id' | 'public_relay_server' | 'public_api_url') {
		const info = src(`public_endpoints.${field}`);
		const row = sourceRow(draft, field, info);
		const source: ValueSource | null =
			info.source?.kind === 'default'
				? {
						kind: 'default',
						text: tf(
							'settings.general.endpoint_auto',
							'Empty: clients get the address found automatically, shown under What clients get now.'
						)
					}
				: info.source;
		const locked = info.lockedBy !== '' || envLocked.includes(field);
		return { ...row, source, onreset: undefined, locked };
	}

	function connectionRow(field: keyof GeneralValues & string) {
		return sourceRow(draft, field, src(`connection_mode.${field}`));
	}

	async function readEndpoints() {
		const e = await getPublicEndpoints();
		envLocked = e.env_locked;
		return {
			public_server_id: e.public_server_id ?? '',
			public_relay_server: e.public_relay_server ?? '',
			public_api_url: e.public_api_url ?? ''
		};
	}

	async function readConnection() {
		try {
			const facts = connectionFacts(await getConnectionMode());
			connWritable = facts.writable;
			connProblem = facts.writable ? '' : t('settings.general.conn_readonly');
			connRuntime = facts.runtime
				? t('settings.general.conn_running', {
						mode: facts.runtime.relayOnly ? t('settings.general.conn_relay') : t('settings.general.conn_p2p'),
						ms: facts.runtime.ms
					})
				: '';
			return facts.settings;
		} catch (err) {
			connWritable = false;
			connProblem = errText(err);
			throw err;
		}
	}

	async function readSources() {
		try {
			sources = await getSettingSources();
		} catch {
			// Without them the rows show no source; the settings still work
			sources = {};
		}
	}

	async function loadForm() {
		const [endpoints, connection] = await Promise.allSettled([
			readEndpoints(),
			readConnection(),
			readSources()
		]);
		if (endpoints.status === 'rejected' || connection.status === 'rejected') {
			const failed = endpoints.status === 'rejected' ? endpoints : (connection as PromiseRejectedResult);
			loadError = errText(failed.reason);
			// Without the saved values the form would overwrite them with defaults
			if (endpoints.status === 'rejected') return;
		}
		loadError = '';
		draft.load({
			...GENERAL_DEFAULTS,
			...(endpoints.status === 'fulfilled' ? endpoints.value : {}),
			...(connection.status === 'fulfilled' ? connection.value : {})
		});
		refused = {};
		loaded = true;
	}

	const TITLES: Record<GeneralPart, string> = {
		endpoints: 'settings.public_endpoints_title',
		connection: 'settings.connection_mode_title',
		scope: 'settings.device_scope_title',
		availability: 'settings.availability_history_title'
	};

	let restart: RestartItem | null = null;

	async function savePart(part: GeneralPart, values: GeneralValues) {
		if (part === 'endpoints') {
			try {
				await savePublicEndpoints({
					public_server_id: values.public_server_id.trim(),
					public_relay_server: values.public_relay_server.trim(),
					public_api_url: values.public_api_url.trim()
				});
			} catch (err) {
				const field = err instanceof ApiError ? (err.data as { field?: unknown } | null)?.field : undefined;
				if (typeof field === 'string' && partFields.endpoints.includes(field))
					refused = { ...refused, [field]: errText(err) };
				throw err;
			}
			return;
		}
		if (!connWritable) throw new Error(t('settings.general.conn_readonly_short'));
		restart =
			(await saveConnectionMode({
				mode: values.mode,
				p2p_fallback_ms: Number(values.p2p_fallback_ms),
				same_nat_relay: values.same_nat_relay,
				allow_shared_nat_initiator: values.allow_shared_nat_initiator,
				logged_in_only_initiator: values.logged_in_only_initiator,
				operator_only_outbound: values.operator_only_outbound
			})) ?? restart;
	}

	async function save() {
		if (draft.busy) return;
		const parts = changedParts(draft.changes);
		const wrong = parts.flatMap((p) => partFields[p]).filter((f) => problems[f]);
		if (wrong.length) {
			draft.error = tf(
				'settings_ui.fix_marked',
				'Some values are not right yet. They are marked on the page.'
			);
			if (wrong.includes('p2p_fallback_ms')) advancedOpen = true;
			document.getElementById(`${idOf(wrong[0] ?? '')}-control`)?.focus();
			return;
		}
		if (
			parts.includes('endpoints') &&
			!(await confirm({
				title: t('settings.public_endpoints_save_title'),
				message: t('settings.public_endpoints_save_confirm'),
				confirmLabel: t('settings.public_endpoints_save')
			}))
		)
			return;
		restart = null;
		refused = {};
		const values = $state.snapshot(draft.values) as GeneralValues;
		await saveAndConfirm(
			draft,
			() =>
				saveInParts(
					parts,
					partFields,
					(p) => t(TITLES[p]),
					(p) => savePart(p, values)
				),
			{ reload: () => Promise.all([loadForm(), loadClientConfig()]) }
		);
		if (restart) showRestartPrompt(restart);
	}

	const idOf = (field: string) =>
		({
			public_server_id: 'public-server-id',
			public_relay_server: 'public-relay-server',
			public_api_url: 'public-api-url',
			p2p_fallback_ms: 'conn-p2p-fallback-ms'
		})[field] ?? field;

	// Cancel in the restart prompt put the previous strategy back: show it, keep the other changes
	$effect(() =>
		onRestartCanceled(() => {
			void readConnection()
				.then((settings) => {
					for (const [field, value] of Object.entries(settings)) {
						draft.set(field, value as never);
						draft.commit([field]);
					}
				})
				.catch(() => undefined);
		})
	);

	/* ── What clients get now (summary column) ── */

	let clientConfig = $state.raw<ClientConfig | null>(null);
	let clientError = $state('');
	// The summary shows once the addresses, the form and the server information are in;
	// parts that grow one after the other would push the rest down (a layout shift)
	const sideSettled = $derived(
		!canConfig ||
			((clientConfig !== null || clientError !== '') &&
				(loaded || !!loadError) &&
				(info !== null || infoError !== ''))
	);

	async function loadClientConfig() {
		try {
			clientConfig = await getClientConfig();
			clientError = '';
		} catch (err) {
			clientError = errText(err);
		}
	}

	const sourceLabel = (source: string | undefined, variable: string) =>
		endpointSourceLabel(source, variable, t);

	// The diagram: the addresses typed above, or the ones clients get now
	const diagram = $derived.by(() => {
		const typedId = v.public_server_id.trim();
		const typedRelay = v.public_relay_server.trim();
		const id = splitHostPort(typedId || clientConfig?.server_id || '', 21116);
		const relay = splitHostPort(typedRelay || typedId || clientConfig?.relay_server || '', 21117);
		return {
			idServer: id.host || '?',
			relay: relay.host || id.host || '?',
			idPort: id.port,
			relayPort: relay.port,
			mode: v.mode === 'relay_only' ? ('relay-only' as const) : ('direct-first' as const)
		};
	});

	/* ── Start ── */

	$effect(() => {
		void loadInfo();
		if (!canConfig) return;
		void loadForm();
		void loadClientConfig();
	});

	// A rule behind Advanced that is changed, refused or reached by a link shows
	const advancedChanged = $derived(
		partFields.connection.slice(1).some((f) => draft.isChanged(f) || !!refused[f])
	);
	$effect(() => {
		if (advancedChanged) advancedOpen = true;
	});

	const sections = $derived([
		{ id: 'public-endpoints', label: t('settings.public_endpoints_title') },
		{ id: 'connection-mode-section', label: t('settings.connection_mode_title') }
	]);

	function numberFrom(e: Event): number | null {
		const raw = (e.currentTarget as HTMLInputElement).value;
		return raw === '' ? null : Number(raw);
	}

	const connLocked = $derived(!connWritable);
</script>

{#snippet consoleVersion()}<Version version={session.appVersion} />{/snippet}
{#snippet serverVersion()}
	{#if isDevBuild}{t('settings.general.dev_build')}{:else}<Version version={goVersion} />{/if}
{/snippet}
{#snippet dbPath()}<ValueCopy
		value={info?.paths?.database}
		label={t('settings.general.database_file')}
	/>{/snippet}

{#snippet serverInfo()}
	<section class="section" id="server-info" aria-labelledby="server-info-title">
		<h2 id="server-info-title">{t('settings.server_info')}</h2>
		{#if infoError}<p class="state is-error" role="alert">{infoError}</p>{/if}
		<IdFacts
			items={[
				{ id: 'console-version', label: t('settings.general.console_version'), value: consoleVersion },
				{ id: 'go-server-version', label: t('settings.general.server_version'), value: serverVersion },
				// The database file and the uptime come only with server.config
				...(info?.paths
					? [{ id: 'db-path', label: t('settings.general.database_file'), value: dbPath }]
					: []),
				...(info?.server
					? [
							{
								id: 'uptime',
								label: t('settings.general.running_for'),
								value: uptime > 0 ? formatDuration(uptime) : ''
							}
						]
					: [])
			]}
		/>
		{#if can('audit.view')}
			<p class="settings-audit-link">
				{t('settings.general.audit_moved')}
				<a href={link('/security-audit')}>{t('settings.general.audit_link')}</a>
			</p>
		{/if}
	</section>
{/snippet}

{#snippet address(value: string, source: string | undefined, variable: string, label: string)}
	<span class="side-value">
		<ValueCopy {value} {label} />
		<span><Tag label={sourceLabel(source, variable)} /></span>
	</span>
{/snippet}
{#snippet idValue()}
	{@render address(
		clientConfig?.server_id ?? '',
		clientConfig?.endpoint_sources?.host,
		ENDPOINT_VARIABLES.host,
		t('dashboard.config_server_id')
	)}
{/snippet}
{#snippet relayValue()}
	{@render address(
		clientConfig?.relay_server ?? '',
		clientConfig?.endpoint_sources?.relay,
		ENDPOINT_VARIABLES.relay,
		t('dashboard.config_relay_server')
	)}
{/snippet}
{#snippet apiValue()}
	{@render address(
		clientConfig?.api_url ?? '',
		clientConfig?.endpoint_sources?.api,
		ENDPOINT_VARIABLES.api,
		t('dashboard.config_api_server')
	)}
{/snippet}

{#snippet side()}
	<!-- All at once when its data is in: parts that grow one after the other would push the rest down -->
	{#if !sideSettled}
		<States kind="loading" />
	{:else}
		<div class="summary-stack">
			<section class="section" aria-labelledby="general-side-title">
				<h2 id="general-side-title">{t('settings.side.general_title')}</h2>
				<p class="section-note">{t('settings.side.general_note')}</p>
				{#if clientError || !clientConfig}
					<States kind="error" message={clientError} onretry={() => void loadClientConfig()} />
				{:else}
					<Facts
						items={[
							{ label: t('dashboard.config_server_id'), value: idValue },
							{ label: t('dashboard.config_relay_server'), value: relayValue },
							{ label: t('dashboard.config_api_server'), value: apiValue }
						]}
					/>
					{#if clientConfig.phone_unreachable_host}<Notice
							tone="warning"
							message={t('settings.side.local_warning')}
						/>{/if}
				{/if}
				{#if connRuntime}<p class="side-line" id="connection-mode-runtime">{connRuntime}</p>{/if}
			</section>
			{@render serverInfo()}
			<GuideLink />
		</div>
	{/if}
{/snippet}

{#snippet endpoint(
	field: 'public_server_id' | 'public_relay_server' | 'public_api_url',
	label: string,
	help: string
)}
	{@const r = endpointRow(field)}
	<SettingRow
		id={idOf(field)}
		{label}
		{help}
		changed={r.changed}
		source={r.source}
		error={errorOf(field)}
		keyName={`PUBLIC_${field === 'public_server_id' ? 'SERVER_ID' : field === 'public_relay_server' ? 'RELAY_SERVER' : 'API_URL'}`}
	>
		{#snippet control(f)}
			<Input
				id={f.id}
				describedby={f.describedby}
				invalid={f.invalid}
				class="mono"
				value={v[field]}
				disabled={r.locked}
				autocomplete="off"
				spellcheck={false}
				inputmode="url"
				oninput={(e) => {
					draft.set(field, (e.currentTarget as HTMLInputElement).value);
					if (refused[field]) refused = { ...refused, [field]: undefined };
				}}
			/>
		{/snippet}
	</SettingRow>
{/snippet}

{#snippet connSwitch(
	field:
		'same_nat_relay' | 'allow_shared_nat_initiator' | 'logged_in_only_initiator' | 'operator_only_outbound',
	id: string,
	label: string,
	help: string
)}
	{@const r = connectionRow(field)}
	<SettingRow
		{id}
		{label}
		{help}
		checked={v[field]}
		onchange={(on) => draft.set(field, on)}
		disabled={connLocked}
		{...r}
		noEffect={notes[field]}
	/>
{/snippet}

{#snippet bar()}<ChangesBar {draft} onsave={save} />{/snippet}

{#if !canConfig}
	<ReadOnlyHint writes={['server.config']} />
	{@render serverInfo()}
{:else}
	<SettingsBody {sections} summary={side} summaryLabel={t('settings.side.general_title')} {bar}>
		{#if loadError && !loaded}
			<States kind="error" message={loadError} onretry={() => void loadForm()} />
		{:else if !loaded}
			<States kind="loading" />
		{:else}
			<SettingsGroup
				id="public-endpoints"
				title={t('settings.public_endpoints_title')}
				description={t('settings.public_endpoints_desc')}
				consequence={t('settings.general.endpoints_effect')}
			>
				{#snippet note()}
					{#if envLocked.length && !Object.keys(sources).some( (k) => k.startsWith('public_endpoints.') )}<Notice
							id="public-endpoints-env-locked"
							message={t('settings.public_endpoints_env_locked')}
						/>{/if}
				{/snippet}
				{@render endpoint(
					'public_server_id',
					t('settings.public_endpoints_server_id'),
					tf(
						'settings.general.endpoint_id_help',
						'Host name or IP address of the ID server, as clients on the internet reach it. Fill it in when clients reach this server under another address than this console, such as behind a reverse proxy. For example remote.example.com.'
					)
				)}
				{@render endpoint(
					'public_relay_server',
					t('settings.public_endpoints_relay'),
					tf(
						'settings.general.endpoint_relay_help',
						'Host name or IP address of the relay server. Leave it empty to use the ID server address.'
					)
				)}
				{@render endpoint(
					'public_api_url',
					t('settings.public_endpoints_api_url'),
					tf(
						'settings.general.endpoint_api_help',
						'Full address clients use to sign in. The server uses port 21114 unless you changed it. For example https://api.example.com or http://203.0.113.10:21114.'
					)
				)}
			</SettingsGroup>

			<SettingsGroup
				id="connection-mode-section"
				title={t('settings.connection_mode_title')}
				description={t('settings.connection_mode_desc')}
				consequence={connWritable ? t('settings.general.conn_effect') : undefined}
			>
				{#snippet note()}
					{#if connProblem}<Notice id="connection-mode-source" message={connProblem} />{/if}
				{/snippet}
				{@const mode = connectionRow('mode')}
				<SettingRow
					id="connection-mode"
					label={t('settings.connection_mode_label')}
					help={tf(
						'settings.general.conn_mode_help',
						'Direct connections are faster. The relay works on every network.'
					)}
					group
					{...mode}
				>
					{#snippet control(f)}
						<div id={f.id}>
							<ChoiceList
								name="connection-mode"
								labelledby={f.labelledby}
								describedby={f.describedby}
								disabled={connLocked}
								bind:value={() => v.mode, (m) => draft.set('mode', m)}
								choices={[
									{
										id: 'conn-mode-p2p',
										value: 'p2p_first',
										title: t('settings.general.conn_p2p'),
										description: t('settings.general.conn_p2p_desc')
									},
									{
										id: 'conn-mode-relay',
										value: 'relay_only',
										title: t('settings.general.conn_relay'),
										description: t('settings.general.conn_relay_desc')
									}
								]}
							/>
						</div>
					{/snippet}
				</SettingRow>

				<div class="advanced" id="connection-mode-advanced">
					<Disclosure title={t('settings.connection_mode_advanced')} level={3} bind:open={advancedOpen}>
						{@const fallback = connectionRow('p2p_fallback_ms')}
						<SettingRow
							id="conn-p2p-fallback-ms"
							label={t('settings.general.conn_fallback_label')}
							help={t('settings.general.conn_fallback_hint')}
							error={errorOf('p2p_fallback_ms')}
							noEffect={notes.p2p_fallback_ms}
							{...fallback}
						>
							{#snippet control(f)}
								<Input
									id={f.id}
									describedby={f.describedby}
									invalid={f.invalid}
									type="number"
									min="0"
									max="30000"
									step="100"
									width="short"
									disabled={connLocked}
									value={v.p2p_fallback_ms}
									oninput={(e) => draft.set('p2p_fallback_ms', numberFrom(e))}
								/>
							{/snippet}
						</SettingRow>
						{@render connSwitch(
							'same_nat_relay',
							'conn-same-nat-relay',
							t('settings.general.conn_same_nat'),
							t('settings.general.conn_same_nat_hint')
						)}
						{@render connSwitch(
							'allow_shared_nat_initiator',
							'conn-allow-shared-nat-initiator',
							t('settings.general.conn_shared_nat'),
							t('settings.general.conn_shared_nat_hint')
						)}
						{@render connSwitch(
							'logged_in_only_initiator',
							'conn-logged-in-only-initiator',
							t('settings.general.conn_logged_in'),
							t('settings.general.conn_logged_in_hint')
						)}
						{@render connSwitch(
							'operator_only_outbound',
							'conn-operator-only-outbound',
							t('settings.general.conn_operator_only'),
							tf(
								'settings.general.conn_operator_only_help',
								'The RustDesk app must be signed in to an account whose role may connect to devices (Connect to devices). Other devices cannot start sessions. Sessions from the browser still work.'
							)
						)}
					</Disclosure>
				</div>

				<div class="diagram-block" data-search-skip>
					<h3 id="connection-diagram-title">{t('diagram.how_title')}</h3>
					<div class="settings-diagram" id="connection-diagram">
						<ConnectionDiagram {...diagram} />
					</div>
					<p class="form-hint" id="connection-mode-info">{t('settings.general.conn_ports_hint')}</p>
				</div>
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
	.advanced {
		padding: var(--space-2) var(--row-pad-x) 0;
	}
	.advanced :global(.setting-row:last-child) {
		border-bottom: 0;
	}
	.diagram-block {
		display: flex;
		flex-direction: column;
		gap: var(--space-3);
		padding: var(--space-4) var(--row-pad-x) 0;
	}
	.form-hint {
		margin: 0;
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	h3 {
		margin: 0;
		font-size: var(--text-md);
	}
	.state.is-error {
		color: var(--danger);
	}
	.settings-audit-link {
		margin: 0;
		color: var(--text-muted);
	}
	.side-value {
		display: flex;
		flex-direction: column;
		align-items: flex-start;
		gap: 2px;
		min-width: 0;
	}
	.side-line {
		margin: 0;
	}
</style>
