<!--
  Settings › Joining (server.config): how a new device gets in and what
  people see of it, as one draft with one save bar (PATTERN.md rule 1).
  The join mode with where it comes from (it is changed in one place,
  Device tokens, under enrollment.manage: BUGS L100), the approval form,
  the default device visibility and the availability history. Save sends
  the parts that changed to their own endpoints, one after the other
  (lib/settings/general.ts saveInParts); the toast after saving offers
  Undo, since visibility decides who sees which devices. Each row says
  where its value comes from (lib/settings/sources.ts).

  Element ids are the old page's (the rows and groups carry them), for
  links and the settings search.
-->
<script lang="ts">
	import type { EnrollmentMode } from '#lib/api/enrollment.ts';
	import {
		getAvailabilityHistory,
		getDeviceScope,
		getEnrollment,
		getSettingSources,
		saveAvailabilityHistory,
		saveDeviceScope,
		saveEnrollment,
		type SettingSources
	} from '#lib/api/settings.ts';
	import { t, tf } from '#lib/i18n/i18n.svelte.ts';
	import { link } from '#lib/nav.ts';
	import { can } from '#lib/state/session.svelte.ts';
	import ChoiceList from '#lib/ui/ChoiceList.svelte';
	import Input from '#lib/ui/Input.svelte';
	import Notice from '#lib/ui/Notice.svelte';
	import States from '#lib/ui/States.svelte';
	import ChangesBar from '#lib/ui/settings/ChangesBar.svelte';
	import SettingRow from '#lib/ui/settings/SettingRow.svelte';
	import SettingsBody from '#lib/ui/settings/SettingsBody.svelte';
	import SettingsGroup from '#lib/ui/settings/SettingsGroup.svelte';
	import { SettingsDraft, saveAndConfirm, type Change } from '#lib/ui/settings/draft.svelte.ts';
	import { partsWithChanges, saveInParts } from '../general.ts';
	import { wholeInRange } from '../numbers.ts';
	import { sourceInfo, sourceRow } from '../sources.ts';

	const canConfig = can('server.config');
	/** The join mode is changed on Device tokens, under the permission that page asks */
	const canChangeMode = can('enrollment.manage');
	const errText = (err: unknown) =>
		err instanceof Error && err.message ? err.message : t('errors.server_error');

	interface Values {
		join_mode: EnrollmentMode;
		rich_approve: boolean;
		tag_picker: boolean;
		scope: 'open' | 'restricted';
		availability_enabled: boolean;
		availability_days: number | null;
		[key: string]: unknown;
	}

	type Part = 'approvals' | 'scope' | 'availability';
	const ORDER: readonly Part[] = ['approvals', 'scope', 'availability'];
	const PARTS: Record<Part, readonly string[]> = {
		approvals: ['rich_approve', 'tag_picker'],
		scope: ['scope'],
		availability: ['availability_enabled', 'availability_days']
	};
	const TITLES: Record<Part, [string, string]> = {
		approvals: ['settings.joining.approval_title', 'Approving a device'],
		scope: ['settings.device_scope_title', 'Device visibility default'],
		availability: ['settings.availability_history_title', 'Availability history']
	};

	const onOff = (v: unknown) => (v ? t('settings.side.on') : t('settings.side.off'));
	const modeName = (v: unknown) => t(`tokens.mode_${String(v)}`);
	const scopeName = (v: unknown) =>
		v === 'restricted' ? t('settings.general.scope_restricted') : t('settings.general.scope_open');

	const draft = new SettingsDraft<Values>(
		{
			join_mode: 'open',
			rich_approve: true,
			tag_picker: true,
			scope: 'open',
			availability_enabled: false,
			availability_days: 30
		},
		{
			join_mode: { label: t('tokens.enrollment_mode'), format: modeName },
			rich_approve: { label: t('settings.enrollment_rich_approve'), format: onOff },
			tag_picker: { label: t('settings.enrollment_tag_picker'), format: onOff },
			scope: { label: t('settings.device_scope_title'), format: scopeName },
			availability_enabled: { label: t('settings.availability_history_enable'), format: onOff },
			availability_days: { label: t('settings.general.retention_label') }
		}
	);
	$effect(() => draft.track());

	let loaded = $state(false);
	let loadError = $state('');
	let pendingCount = $state(0);
	let sources = $state.raw<SettingSources>({});

	const v = $derived(draft.values);
	const daysProblem = $derived(
		wholeInRange(v.availability_days, 1, 3650) === null ? t('settings.general.retention_error') : ''
	);

	async function loadForm() {
		try {
			const [enrollment, scope, availability] = await Promise.all([
				getEnrollment(),
				getDeviceScope(),
				getAvailabilityHistory(),
				getSettingSources()
					.then((s) => (sources = s))
					.catch(() => (sources = {}))
			]);
			pendingCount = Number(enrollment.pending_count) || 0;
			const mode = enrollment.mode;
			draft.load({
				join_mode: mode === 'managed' || mode === 'locked' ? mode : 'open',
				rich_approve: enrollment.rich_approve !== false,
				tag_picker: enrollment.tag_picker !== false,
				scope,
				availability_enabled: availability.enabled === true,
				availability_days: Number(availability.retention_days) || 30
			});
			loadError = '';
			loaded = true;
		} catch (err) {
			// Without the saved values the form would overwrite them with defaults
			loadError = errText(err);
		}
	}

	async function savePart(part: Part, values: Values) {
		if (part === 'approvals')
			await saveEnrollment({ rich_approve: values.rich_approve, tag_picker: values.tag_picker });
		else if (part === 'scope') await saveDeviceScope(values.scope);
		else await saveAvailabilityHistory(values.availability_enabled, Number(values.availability_days));
	}

	const title = (p: Part) => tf(TITLES[p][0], TITLES[p][1]);

	async function save() {
		if (draft.busy) return;
		const parts = partsWithChanges(draft.changes, PARTS, ORDER);
		if (parts.includes('availability') && daysProblem) {
			draft.error = tf(
				'settings_ui.fix_marked',
				'Some values are not right yet. They are marked on the page.'
			);
			document.getElementById('availability-history-retention-control')?.focus();
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

	const modeInfo = $derived(sourceInfo(sources['enrollment.mode']));

	const sections = $derived([
		{ id: 'joining-mode-section', label: t('tokens.enrollment_mode') },
		{ id: 'enrollment-settings-section', label: title('approvals') },
		{ id: 'device-scope-settings-panel', label: t('settings.device_scope_title') },
		{ id: 'availability-history-settings-panel', label: t('settings.availability_history_title') }
	]);
</script>

{#snippet bar()}<ChangesBar {draft} onsave={save} />{/snippet}

{#if !canConfig}
	<States message={t('settings.page.admin_only')} />
{:else}
	<SettingsBody {sections} {bar}>
		{#if loadError && !loaded}
			<States kind="error" message={loadError} onretry={() => void loadForm()} />
		{:else if !loaded}
			<States kind="loading" />
		{:else}
			<SettingsGroup
				id="joining-mode-section"
				title={t('tokens.enrollment_mode')}
				description={tf('settings.joining.mode_note', 'Which new devices may join this server, and how.')}
				consequence={tf(
					'settings.joining.mode_effect',
					'Applies to the next device that asks to join. Devices that already joined stay.'
				)}
			>
				<SettingRow
					id="enrollment-mode"
					label={t('tokens.enrollment_mode')}
					help={t(`tokens.mode_${v.join_mode}_desc`)}
					group
					keyName="ENROLLMENT_MODE"
					source={modeInfo.source}
				>
					{#snippet control(f)}
						<div id={f.id} role="group" aria-labelledby={f.labelledby} aria-describedby={f.describedby}>
							<p class="mode-now"><strong>{t(`tokens.mode_${v.join_mode}`)}</strong></p>
							{#if canChangeMode && !modeInfo.lockedBy}
								<a href={link('/devices/add?tab=tokens')}>{t('settings.enrollment_mode_link')}</a>
							{:else if !canChangeMode}
								<p class="mode-note">
									{tf('settings.joining.mode_needs', 'Changing it needs the permission {permission}.', {
										permission: t('permissions.perm_enrollment_manage')
									})}
								</p>
							{/if}
						</div>
					{/snippet}
				</SettingRow>
			</SettingsGroup>

			<SettingsGroup
				id="enrollment-settings-section"
				title={title('approvals')}
				description={t('settings.enrollment_desc')}
				consequence={t('settings.signin.enrollment_effect')}
			>
				{#snippet note()}
					<p class="group-line">
						{#if pendingCount > 0}<span id="enrollment-pending-hint"
								>{t('settings.enrollment_pending_count', { count: pendingCount })}</span
							>{/if}
						<a href={link('/registrations')}>{t('settings.enrollment_open_requests')}</a>
					</p>
				{/snippet}
				<SettingRow
					id="enrollment-rich-approve"
					label={t('settings.enrollment_rich_approve')}
					help={t('settings.enrollment_rich_approve_hint')}
					checked={v.rich_approve}
					onchange={(on) => draft.set('rich_approve', on)}
					changed={draft.isChanged('rich_approve')}
					noEffect={v.join_mode === 'open'
						? tf(
								'settings.joining.approve_open',
								'Applies only to Mesh agents while the join mode is Open: RustDesk clients join without approval.'
							)
						: undefined}
				/>
				<SettingRow
					id="enrollment-tag-picker"
					label={t('settings.enrollment_tag_picker')}
					help={t('settings.enrollment_tag_picker_hint')}
					checked={v.tag_picker}
					onchange={(on) => draft.set('tag_picker', on)}
					changed={draft.isChanged('tag_picker')}
					noEffect={v.rich_approve
						? undefined
						: tf(
								'settings.joining.tags_needs_form',
								'Has no effect while the full approval form is off: Approve then asks for no tags.'
							)}
				/>
			</SettingsGroup>

			<SettingsGroup
				id="device-scope-settings-panel"
				title={t('settings.device_scope_title')}
				description={t('settings.general.scope_note')}
				consequence={t('settings.general.scope_effect')}
			>
				<SettingRow
					id="device-scope"
					label={tf('settings.joining.scope_label', 'What people see without access')}
					help={tf(
						'settings.joining.scope_help',
						'Administrators always see every device. Access to folders, groups and devices is given on their pages.'
					)}
					group
					keyName="DEVICE_SCOPE_DEFAULT"
					{...sourceRow(draft, 'scope', sourceInfo(sources['device_scope.mode']))}
				>
					{#snippet control(f)}
						<div id={f.id}>
							<ChoiceList
								name="device_scope_mode"
								labelledby={f.labelledby}
								describedby={f.describedby}
								bind:value={() => v.scope, (s) => draft.set('scope', s)}
								choices={[
									{
										id: 'device-scope-open',
										value: 'open',
										title: t('settings.general.scope_open'),
										description: t('settings.general.scope_open_desc')
									},
									{
										id: 'device-scope-restricted',
										value: 'restricted',
										title: t('settings.general.scope_restricted'),
										description: t('settings.general.scope_restricted_desc')
									}
								]}
							/>
						</div>
						{#if v.scope === 'restricted'}<Notice
								tone="warning"
								id="device-scope-warning"
								message={t('settings.general.scope_restricted_warning')}
							/>{/if}
					{/snippet}
				</SettingRow>
			</SettingsGroup>

			<SettingsGroup
				id="availability-history-settings-panel"
				title={t('settings.availability_history_title')}
				description={t('settings.general.availability_note')}
				consequence={t('settings.general.availability_effect')}
			>
				<SettingRow
					id="availability-history-enabled"
					label={t('settings.availability_history_enable')}
					help={t('settings.general.availability_enable_hint')}
					checked={v.availability_enabled}
					onchange={(on) => draft.set('availability_enabled', on)}
					{...sourceRow(draft, 'availability_enabled', sourceInfo(sources['availability_history.enabled']))}
				/>
				<SettingRow
					id="availability-history-retention"
					label={t('settings.general.retention_label')}
					help={tf('settings.joining.retention_help', 'Older entries are deleted automatically.')}
					error={daysProblem}
					noEffect={v.availability_enabled
						? undefined
						: tf('settings.joining.retention_off', 'Has no effect while availability history is off.')}
					{...sourceRow(
						draft,
						'availability_days',
						sourceInfo(sources['availability_history.retention_days'])
					)}
				>
					{#snippet control(f)}
						<Input
							id={f.id}
							describedby={f.describedby}
							invalid={f.invalid}
							type="number"
							min="1"
							max="3650"
							width="short"
							value={v.availability_days}
							oninput={(e) => {
								const raw = (e.currentTarget as HTMLInputElement).value;
								draft.set('availability_days', raw === '' ? null : Number(raw));
							}}
						/>
					{/snippet}
				</SettingRow>
			</SettingsGroup>
		{/if}
	</SettingsBody>
{/if}

<style>
	.mode-now,
	.mode-note {
		margin: 0;
	}
	.mode-note {
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	.group-line {
		margin: 0;
		color: var(--text-muted);
		font-size: var(--text-sm);
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-1) var(--space-3);
	}
</style>
