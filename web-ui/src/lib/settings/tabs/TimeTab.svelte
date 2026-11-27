<!--
  Settings › Time (server.config): the billing time zone and the clock
  synchronisation, as one draft with one save bar (PATTERN.md rule 1).
  The time servers are a list with a message per wrong entry; the
  expert options sit behind Advanced. Each row says where its value comes
  from (lib/settings/sources.ts): the console writes the environment
  file these live in, so nothing is locked. A native install asks to
  restart the server after saving; Docker reloads them. Beside the form:
  the billing period in the chosen zone (also before saving) and the
  server clock with Check now (an action).
-->
<script lang="ts">
	import {
		checkTimeSync,
		detectTimezone,
		errorText,
		getClockSettings,
		getTimeSync,
		saveClockSettings,
		type ClockSettings,
		type TimeSync
	} from '#lib/api/settings-system.ts';
	import { ApiError } from '#lib/api/client.ts';
	import { getSettingSources, type SettingSources } from '#lib/api/settings.ts';
	import { formatNumber } from '#lib/i18n/format.ts';
	import { i18n, t, tf } from '#lib/i18n/i18n.svelte.ts';
	import { link } from '#lib/nav.ts';
	import { onRestartCanceled, showRestartPrompt } from '#lib/settings/restart.svelte.ts';
	import {
		TIME_ERRORS,
		chosenZone,
		clockPayload,
		ntpServerProblem,
		splitServers,
		timeProblems,
		zoneChoices,
		zoneLabel,
		zoneNow,
		type TimeValues
	} from '#lib/settings-b/time.ts';
	import { onRefresh } from '#lib/shell/page.svelte.ts';
	import { can } from '#lib/state/session.svelte.ts';
	import BooleanStatus from '#lib/ui/BooleanStatus.svelte';
	import Button from '#lib/ui/Button.svelte';
	import Disclosure from '#lib/ui/Disclosure.svelte';
	import Empty from '#lib/ui/Empty.svelte';
	import Facts, { type Fact } from '#lib/ui/Facts.svelte';
	import IdFacts, { type IdFact } from '#lib/settings-b/IdFacts.svelte';
	import FormStatus from '#lib/ui/FormStatus.svelte';
	import Input from '#lib/ui/Input.svelte';
	import Select from '#lib/ui/Select.svelte';
	import States from '#lib/ui/States.svelte';
	import Status from '#lib/ui/Status.svelte';
	import ValueCopy from '#lib/ui/ValueCopy.svelte';
	import { minuteNow } from '#lib/ui/clock.svelte.ts';
	import { confirm } from '#lib/ui/dialogs.svelte.ts';
	import ChangesBar from '#lib/ui/settings/ChangesBar.svelte';
	import ListEditor from '#lib/ui/settings/ListEditor.svelte';
	import SettingRow from '#lib/ui/settings/SettingRow.svelte';
	import SettingsBody from '#lib/ui/settings/SettingsBody.svelte';
	import SettingsGroup from '#lib/ui/settings/SettingsGroup.svelte';
	import { SettingsDraft, saveAndConfirm } from '#lib/ui/settings/draft.svelte.ts';
	import { sourceInfo, sourceRow } from '../sources.ts';
	import '../side-rows.css';

	const allowed = can('server.config');

	let common = $state.raw<string[]>([]);
	let zones = $state.raw<string[]>(['UTC']);
	let dockerMode = $state(false);
	let loaded = $state(false);
	let loadError = $state('');
	let sources = $state.raw<SettingSources>({});
	let customOpen = $state(false);
	let advancedOpen = $state(false);
	let detectLine = $state('');
	let detecting = $state(false);
	/** What the server refused, by field */
	let refused = $state<Partial<Record<string, string>>>({});

	let sync = $state.raw<TimeSync | null>(null);
	let checking = $state(false);
	let checkLine = $state<{ message: string; tone: 'neutral' | 'ok' | 'error' }>({
		message: '',
		tone: 'neutral'
	});

	const onOff = (v: unknown) => (v ? t('settings.side.on') : t('settings.side.off'));
	const draft = new SettingsDraft<TimeValues>(
		{
			zone: 'UTC',
			custom: '',
			ntp_servers: [],
			max_skew_ms: 2000,
			require_synced_clock: true,
			trust_os_ntp: true
		},
		{
			zone: { label: t('settings.time.billing_timezone') },
			custom: { label: t('settings.time.custom_zone_label') },
			ntp_servers: { label: t('settings.time.servers_label') },
			max_skew_ms: { label: t('settings.time.max_skew_label') },
			require_synced_clock: { label: t('commercialization.clock.require_sync'), format: onOff },
			trust_os_ntp: { label: t('settings.time.trust_os_label'), format: onOff }
		}
	);
	$effect(() => draft.track());

	const v = $derived(draft.values);
	const problems = $derived<Partial<Record<string, string>>>(timeProblems(v));

	function toValues(s: ClockSettings | null | undefined): TimeValues {
		const zone = s?.billing_timezone || 'UTC';
		const inList = zones.includes(zone);
		return {
			zone: inList ? zone : 'UTC',
			// A saved zone the list doesn't have goes in the custom field
			custom: inList ? '' : zone,
			ntp_servers: splitServers(s?.ntp_servers ?? ''),
			max_skew_ms: Number(s?.max_skew_ms) || 2000,
			require_synced_clock: s?.require_synced_clock !== false,
			trust_os_ntp: s?.trust_os_ntp !== false
		};
	}

	/** The saved values, with the saved zone added to the list when it is new */
	function withZones(s: ClockSettings): TimeValues {
		zones = zoneChoices(s.billing_timezone || 'UTC', common);
		const values = toValues(s);
		if (values.custom) customOpen = true;
		return values;
	}

	async function load() {
		try {
			const detected = await detectTimezone();
			common = detected.common;
		} catch {
			common = ['UTC', 'Europe/Warsaw'];
		}
		try {
			const [data] = await Promise.all([
				getClockSettings(),
				getSettingSources()
					.then((s) => (sources = s))
					.catch(() => (sources = {}))
			]);
			dockerMode = !!data.dockerMode;
			draft.load(withZones(data.settings ?? { ntp_servers: '', billing_timezone: 'UTC' }));
			refused = {};
			loadError = '';
			loaded = true;
		} catch (err) {
			// Without the saved values the form would overwrite them with defaults
			loadError = errorText(err, t('errors.server_error'));
		}
		void refreshSync();
	}

	async function refreshSync() {
		try {
			sync = await getTimeSync();
		} catch {
			/* the side keeps the last known state */
		}
	}

	/** An action that fills the field (unsaved); Save stores it */
	async function useServerZone() {
		detecting = true;
		try {
			const data = await detectTimezone();
			const tz = data.timezone || 'UTC';
			common = data.common.length ? data.common : common;
			zones = zoneChoices(tz, common);
			draft.set('zone', tz);
			draft.set('custom', '');
			detectLine = t('settings.time.detected', { tz });
		} catch (err) {
			detectLine = errorText(err, t('errors.server_error'));
		} finally {
			detecting = false;
		}
	}

	async function checkNow() {
		checking = true;
		checkLine = { message: '', tone: 'neutral' };
		try {
			await checkTimeSync();
			checkLine = { message: t('commercialization.clock.checked'), tone: 'ok' };
			await refreshSync();
		} catch (err) {
			checkLine = { message: errorText(err, t('errors.server_error')), tone: 'error' };
		} finally {
			checking = false;
		}
	}

	async function save() {
		if (draft.busy) return;
		const wrong = Object.keys(problems);
		if (wrong.length) {
			draft.error = tf(
				'settings_ui.fix_marked',
				'Some values are not right yet. They are marked on the page.'
			);
			if (problems.max_skew_ms) advancedOpen = true;
			document
				.getElementById(
					problems.ntp_servers ? 'time-clock-ntp-servers-control-add' : 'time-clock-max-skew-control'
				)
				?.focus();
			return;
		}
		// Natively the clock settings take effect after a server restart; Docker reloads them
		if (!dockerMode) {
			const yes = await confirm({
				title: t('settings.time.save_title'),
				message: t('commercialization.clock.save_restart'),
				confirmLabel: t('settings.time.save')
			});
			if (!yes) return;
		}
		refused = {};
		const values = $state.snapshot(draft.values) as TimeValues;
		let restart: Parameters<typeof showRestartPrompt>[0] = null;
		const saved = await saveAndConfirm(
			draft,
			async () => {
				try {
					const result = await saveClockSettings(clockPayload(values));
					if (result.dockerMode) dockerMode = true;
					restart = result.restartRequired ?? null;
				} catch (err) {
					// Server error codes in plain words, on their field
					const known = err instanceof ApiError ? TIME_ERRORS[err.message] : undefined;
					if (known) {
						const field =
							known.field === 'billing_timezone' ? (values.custom.trim() ? 'custom' : 'zone') : known.field;
						refused = { [field]: t(known.key) };
						throw new Error(t(known.key), { cause: err });
					}
					throw err;
				}
			},
			{ reload: load }
		);
		if (saved && restart) showRestartPrompt(restart);
	}

	$effect(() => {
		if (!allowed) return;
		void load();
		const offRefresh = onRefresh(() => void refreshSync());
		// Cancel in the restart prompt puts the old values back: show them
		const offCancel = onRestartCanceled(() => void load());
		return () => {
			offRefresh();
			offCancel();
		};
	});

	// A change or a refusal behind Advanced shows it
	$effect(() => {
		if (['max_skew_ms', 'trust_os_ntp'].some((f) => draft.isChanged(f) || refused[f])) advancedOpen = true;
	});

	const src = (field: string) => sourceInfo(sources[`clock.${field}`]);
	const zoneSource = $derived.by(() => {
		const info = src('billing_timezone');
		// The default is the zone the server's system uses; resetting picks it in the list
		return sourceRow(draft, 'zone', { ...info, hasDefault: info.hasDefault && !v.custom.trim() });
	});

	const zone = $derived(chosenZone(v.custom, v.zone));
	const now = $derived(zoneNow(zone, i18n.locale, new Date(minuteNow())));
	const zoneOptions = $derived.by(() => {
		const at = new Date(minuteNow());
		return zones.map((z) => ({ value: z, label: zoneLabel(z, at) }));
	});
	const zoneHint = $derived(
		now
			? t('settings.time.zone_now', { offset: now.offset, time: now.time })
			: t('settings.time.zone_unknown', { zone })
	);
	const periodFacts = $derived<Fact[]>([
		{
			label: t('settings.time.period_current'),
			value: now ? t('settings.time.period_range', { start: now.start, end: now.end }) : ''
		},
		{
			label: t('settings.time.period_next'),
			value: now ? t('settings.time.period_next_value', { date: now.next, zone }) : ''
		}
	]);
	const clockFacts = $derived<IdFact[]>([
		{
			id: 'time-clock-offset',
			label: t('commercialization.clock.status_offset'),
			value:
				sync?.offset_ms !== null && sync?.offset_ms !== undefined
					? formatNumber(sync.offset_ms, { style: 'unit', unit: 'millisecond', unitDisplay: 'short' })
					: ''
		},
		{ id: 'time-clock-ntp-server', label: t('commercialization.clock.status_server'), value: ntpSource },
		{ id: 'time-clock-os-sync', label: t('commercialization.clock.status_os'), value: osSynced },
		{ id: 'time-clock-last-error', label: t('commercialization.clock.status_error'), value: lastError }
	]);
</script>

{#snippet ntpSource()}
	<ValueCopy value={sync?.ntp_server} copy={false} />
{/snippet}
{#snippet osSynced()}
	{#if typeof sync?.os_clock_synced === 'boolean'}
		<BooleanStatus
			value={sync.os_clock_synced}
			on={t('commercialization.clock.os_yes')}
			off={t('commercialization.clock.os_no')}
		/>
	{:else}
		<Empty />
	{/if}
{/snippet}
{#snippet lastError()}
	{#if sync?.last_error}
		<Status tone="danger" label={sync.last_error} />
	{:else}
		<Empty />
	{/if}
{/snippet}

{#snippet side()}
	<div class="summary-stack">
		<section class="section" aria-labelledby="time-period-title">
			<h2 id="time-period-title">{t('settings.time.period_title')}</h2>
			<Facts items={periodFacts} />
			<p class="hint">{t('settings.time.period_hint')}</p>
			{#if can('billing.view')}
				<p class="hint"><a href={link('/billing')}>{t('settings.time.commercialization_link')}</a></p>
			{/if}
		</section>
		<section class="section" aria-labelledby="time-side-title">
			<h2 id="time-side-title">{t('settings.side.clock_title')}</h2>
			{#if sync}
				<p role="status">
					{#if sync.synced}
						<Status tone="good" label={t('commercialization.clock.synced_detail')} />
					{:else}
						<Status tone="warning" label={t('commercialization.clock.unsynced')} />
					{/if}
				</p>
			{/if}
			<IdFacts items={clockFacts} />
			<div class="inline-controls">
				<Button id="time-timesync-check-btn" icon="refresh" busy={checking} onclick={checkNow}
					>{t('commercialization.clock.check_now')}</Button
				>
				<FormStatus message={checkLine.message} tone={checkLine.tone} />
			</div>
		</section>
	</div>
{/snippet}

{#snippet bar()}<ChangesBar {draft} onsave={save} />{/snippet}

{#if !allowed}
	<States message={t('settings.page.admin_only')} />
{:else}
	<!-- On a phone the time zone comes first; the period and the clock it decides follow -->
	<SettingsBody
		summary={side}
		summaryLabel={t('settings.side.clock_title')}
		summaryLast
		{bar}
		class="side-rows"
	>
		{#if loadError && !loaded}
			<States kind="error" message={loadError} onretry={() => void load()} />
		{:else if !loaded}
			<States kind="loading" />
		{:else}
			<SettingsGroup
				id="time-zone"
				title={t('settings.time.timezone_title')}
				description={t('settings.time.timezone_desc')}
				consequence={t('settings.time.billing_month_hint')}
			>
				<SettingRow
					id="time-billing-timezone"
					label={t('settings.time.billing_timezone')}
					help={zoneHint}
					error={refused.zone ?? ''}
					keyName="BILLING_TIMEZONE"
					{...zoneSource}
				>
					{#snippet control(f)}
						<div class="inline-controls">
							<Select
								id={f.id}
								describedby={f.describedby}
								invalid={f.invalid}
								class="zone-select"
								options={zoneOptions}
								bind:value={() => v.zone, (z) => draft.set('zone', z ?? 'UTC')}
							/>
							<Button id="time-detect-timezone-btn" busy={detecting} onclick={useServerZone}
								>{t('settings.time.use_server_zone')}</Button
							>
						</div>
						{#if detectLine}<FormStatus message={detectLine} />{/if}
					{/snippet}
				</SettingRow>
				<div class="advanced" id="time-custom-zone">
					<Disclosure title={t('settings.time.custom_zone_toggle')} level={3} bind:open={customOpen}>
						<SettingRow
							id="time-billing-timezone-custom"
							label={t('settings.time.custom_zone_label')}
							help={t('settings.time.custom_timezone_hint')}
							error={refused.custom ?? ''}
							changed={draft.isChanged('custom')}
						>
							{#snippet control(f)}
								<Input
									id={f.id}
									describedby={f.describedby}
									invalid={f.invalid}
									class="mono"
									maxlength={64}
									spellcheck={false}
									value={v.custom}
									oninput={(e) => draft.set('custom', (e.currentTarget as HTMLInputElement).value)}
								/>
							{/snippet}
						</SettingRow>
					</Disclosure>
				</div>
			</SettingsGroup>

			<SettingsGroup
				id="time-ntp"
				title={t('settings.time.sync_title')}
				description={t('settings.time.ntp_desc')}
				consequence={dockerMode
					? tf('settings.time.ntp_effect_docker', 'Applies right away after saving.')
					: t('settings.time.ntp_effect')}
			>
				<SettingRow
					id="time-clock-ntp-servers"
					label={t('settings.time.servers_label')}
					help={tf(
						'settings.time.servers_help',
						'The NeoDesk server sets its clock from these, one host name or IP address per row. For example pool.ntp.org.'
					)}
					group
					error={(draft.isChanged('ntp_servers') && !v.ntp_servers.length ? problems.ntp_servers : '') ||
						(refused.ntp_servers ?? '')}
					keyName={sources['clock.ntp_servers']?.env_var}
					{...sourceRow(draft, 'ntp_servers', src('ntp_servers'), splitServers)}
				>
					{#snippet control(f)}
						<ListEditor
							id={f.id}
							describedby={f.describedby}
							items={v.ntp_servers}
							onchange={(list) => draft.set('ntp_servers', list)}
							itemLabel={(n) => tf('settings.time.server_n', 'Time server {n}', { n })}
							check={ntpServerProblem}
							addLabel={tf('settings.time.server_add', 'Add a time server')}
						/>
					{/snippet}
				</SettingRow>
				<SettingRow
					id="time-clock-require-sync"
					label={t('commercialization.clock.require_sync')}
					help={t('settings.time.require_sync_hint')}
					checked={v.require_synced_clock}
					onchange={(on) => draft.set('require_synced_clock', on)}
					keyName={sources['clock.require_synced_clock']?.env_var}
					{...sourceRow(draft, 'require_synced_clock', src('require_synced_clock'))}
				/>
				<div class="advanced" id="time-clock-advanced">
					<Disclosure title={t('settings.time.advanced_toggle')} level={3} bind:open={advancedOpen}>
						<SettingRow
							id="time-clock-max-skew"
							label={t('settings.time.max_skew_label')}
							help={t('settings.time.max_skew_hint')}
							error={problems.max_skew_ms ?? refused.max_skew_ms ?? ''}
							keyName={sources['clock.max_skew_ms']?.env_var}
							{...sourceRow(draft, 'max_skew_ms', src('max_skew_ms'))}
						>
							{#snippet control(f)}
								<Input
									id={f.id}
									describedby={f.describedby}
									invalid={f.invalid}
									type="number"
									min="1"
									max="600000"
									step="100"
									width="short"
									value={v.max_skew_ms}
									oninput={(e) => {
										const raw = (e.currentTarget as HTMLInputElement).value;
										draft.set('max_skew_ms', raw === '' ? null : Number(raw));
									}}
								/>
							{/snippet}
						</SettingRow>
						<SettingRow
							id="time-clock-trust-os"
							label={t('settings.time.trust_os_label')}
							help={t('settings.time.trust_os_hint')}
							checked={v.trust_os_ntp}
							onchange={(on) => draft.set('trust_os_ntp', on)}
							keyName={sources['clock.trust_os_ntp']?.env_var}
							{...sourceRow(draft, 'trust_os_ntp', src('trust_os_ntp'))}
						/>
					</Disclosure>
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
	.hint {
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	.inline-controls {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2) var(--space-3);
		align-items: center;
	}
	/* A select is as wide as its longest zone name unless it may shrink:
	   on a 320 px screen it fits the row (WCAG 1.4.10) */
	.inline-controls :global(.zone-select) {
		flex: 1 1 260px;
		width: auto;
		min-width: 0;
		max-width: min(48ch, 100%);
	}
	.advanced {
		padding: var(--space-2) var(--row-pad-x) 0;
	}
	.advanced :global(.setting-row:last-child) {
		border-bottom: 0;
	}
</style>
