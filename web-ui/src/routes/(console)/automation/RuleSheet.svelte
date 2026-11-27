<!--
  Create or edit an alert rule: name and description, what it watches
  (condition; comparison and threshold with its unit for every kind but
  Custom; all devices or one), and the alert (severity, on or off). The
  condition's help says when the server checks it; the device ID field
  shows only for "One device". Enter in a field saves; refusals go on the
  field and in the status line.
-->
<script lang="ts">
	import { untrack } from 'svelte';
	import { createRule, updateRule, type AlertRule } from '#lib/api/automation.ts';
	import { t, tf } from '#lib/i18n/i18n.svelte.ts';
	import {
		CONDITION_TYPES,
		checkedWhenKey,
		labelFor,
		OPERATORS,
		ruleBody,
		ruleValues,
		SEVERITIES,
		unitKey
	} from '#lib/rules/automation.ts';
	import Button from '#lib/ui/Button.svelte';
	import ChoiceSegments from '#lib/ui/settings/ChoiceSegments.svelte';
	import Checkbox from '#lib/ui/Checkbox.svelte';
	import Field from '#lib/ui/Field.svelte';
	import FormSection from '#lib/ui/FormSection.svelte';
	import Input from '#lib/ui/Input.svelte';
	import Select from '#lib/ui/Select.svelte';
	import Sheet from '#lib/ui/Sheet.svelte';
	import ShowWhen from '#lib/ui/ShowWhen.svelte';
	import { createForm, FormError } from '#lib/ui/form.svelte.ts';
	import { toast } from '#lib/ui/toasts.svelte.ts';

	let { rule, onclose, onsaved }: { rule: AlertRule | null; onclose: () => void; onsaved: () => void } =
		$props();

	const uid = $props.id();
	const limitId = `${uid}-limit`;
	const deviceId = `${uid}-device`;

	/** "One device": the rule watches the device whose ID is typed; else every device */
	let oneDevice = $state(untrack(() => !!rule?.scope_device_id));

	const form = createForm({
		initial: untrack(() => ruleValues(rule)),
		validate: (v) => {
			const check = ruleBody(v);
			const errors: { name?: string; condition_value?: string; scope_device_id?: string } = {};
			if (!check.ok) {
				if (check.field === 'name') errors.name = t('automation.name_required');
				else errors.condition_value = t('automation.threshold_required');
			}
			if (oneDevice && !v.scope_device_id.trim())
				errors.scope_device_id = tf(
					'automation.device_required',
					'Enter the device ID, or choose All devices.'
				);
			return Object.keys(errors).length ? errors : null;
		},
		submit: async (v) => {
			const check = ruleBody(oneDevice ? v : { ...v, scope_device_id: '' });
			if (!check.ok) throw new FormError(t('automation.threshold_required'), check.field);
			try {
				if (rule) await updateRule(rule.id, check.body);
				else await createRule(check.body);
			} catch (err) {
				throw new FormError(
					`${t('automation.rule_save_failed')} ${err instanceof Error ? err.message : ''}`.trim()
				);
			}
			toast.success(t('automation.rule_saved', { name: check.body.name }));
			onsaved();
		}
	});

	const custom = $derived(form.values.condition_type === 'custom');
	const checkedWhen = $derived(checkedWhenKey(form.values.condition_type));
	const unit = $derived(unitKey(form.values.condition_type));

	const conditionOptions = $derived(
		CONDITION_TYPES.map((c) => ({ value: c, label: labelFor(t, 'type', c) }))
	);
	const opOptions = $derived(OPERATORS.map((o) => ({ value: o, label: labelFor(t, 'op', o) })));
	const severityOptions = $derived(SEVERITIES.map((s) => ({ value: s, label: labelFor(t, 'severity', s) })));
	const scopeOptions = $derived([
		{ value: 'all', label: t('automation.scope_all') },
		{ value: 'one', label: tf('automation.scope_one', 'One device') }
	]);
</script>

<Sheet
	title={rule ? t('automation.edit_rule') : t('automation.create_rule')}
	subtitle={t('automation.rule_sheet_desc')}
	status={form.status.message}
	statusTone={form.status.state === 'error' ? 'error' : 'neutral'}
	class="rule-sheet"
	{onclose}
>
	<form id={form.id} onsubmit={form.onsubmit} novalidate>
		<Field label={t('automation.rule_name')} error={form.errors.name} width="host">
			{#snippet children(f)}
				<Input {...f} maxlength={100} required autocomplete="off" bind:value={form.values.name} />
			{/snippet}
		</Field>
		<Field label={t('automation.description')} hint={t('automation.description_hint')}>
			{#snippet children(f)}
				<Input {...f} maxlength={500} autocomplete="off" bind:value={form.values.description} />
			{/snippet}
		</Field>
		<FormSection title={t('automation.section_condition')}>
			<Field
				label={t('automation.condition_type')}
				hint={checkedWhen
					? tf(checkedWhen.key, checkedWhen.fallback)
					: tf(
							'automation.custom_not_checked',
							'A custom rule has no value the server can measure, so it raises no alerts by itself.'
						)}
				width="host"
			>
				{#snippet children(f)}
					<Select
						{...f}
						aria-controls={limitId}
						options={conditionOptions}
						bind:value={form.values.condition_type}
					/>
				{/snippet}
			</Field>
			<!-- A custom rule has no limit: comparison and threshold show for the other kinds only -->
			<ShowWhen id={limitId} when={!custom}>
				<Field label={t('automation.condition_op')} width="medium">
					{#snippet children(f)}<Select
							{...f}
							options={opOptions}
							bind:value={form.values.condition_op}
						/>{/snippet}
				</Field>
				<Field
					label={t('automation.condition_value')}
					hint={t('automation.threshold_hint')}
					error={form.errors.condition_value}
				>
					{#snippet children(f)}
						<span class="value-row">
							<Input
								{...f}
								describedby={[unit ? `${uid}-unit` : '', f.describedby].filter(Boolean).join(' ') ||
									undefined}
								type="number"
								step="any"
								width="short"
								bind:value={form.values.condition_value}
							/>
							{#if unit}<span class="unit" id="{uid}-unit">{t(unit)}</span>{/if}
						</span>
					{/snippet}
				</Field>
			</ShowWhen>
			<div class="scope">
				<p class="scope-label" id="{uid}-scope-label">{t('automation.scope_device')}</p>
				<ChoiceSegments
					id="{uid}-scope"
					labelledby="{uid}-scope-label"
					options={scopeOptions}
					value={oneDevice ? 'one' : 'all'}
					onchange={(v) => {
						oneDevice = v === 'one';
						if (!oneDevice) form.values.scope_device_id = '';
					}}
				/>
			</div>
			<ShowWhen id={deviceId} when={oneDevice}>
				<Field
					label={tf('automation.device_id', 'Device ID')}
					hint={tf('automation.device_id_hint', 'The ID the device shows in its NeoDesk app.')}
					error={form.errors.scope_device_id}
					width="medium"
				>
					{#snippet children(f)}
						<Input
							{...f}
							class="mono"
							autocomplete="off"
							spellcheck={false}
							bind:value={form.values.scope_device_id}
						/>
					{/snippet}
				</Field>
			</ShowWhen>
		</FormSection>
		<FormSection title={t('automation.section_alert')}>
			<Field label={t('automation.severity')} hint={t('automation.severity_hint')} width="medium">
				{#snippet children(f)}<Select
						{...f}
						options={severityOptions}
						bind:value={form.values.severity}
					/>{/snippet}
			</Field>
			<Checkbox
				label={t('automation.enabled')}
				hint={t('automation.enabled_hint')}
				effect={t('automation.rule_effect')}
				bind:checked={form.values.enabled}
			/>
		</FormSection>
	</form>
	{#snippet footer()}
		<Button class="sheet-footer-start" onclick={onclose}>{t('actions.cancel')}</Button>
		<Button variant="primary" type="submit" form={form.id} busy={form.busy}
			>{rule ? t('actions.save') : t('automation.create_rule')}</Button
		>
	{/snippet}
</Sheet>

<style>
	/* Addresses, IDs, lists and JSON in the monospace font */
	form :global(.form-input.mono) {
		font-family: var(--mono);
	}
	.value-row {
		display: flex;
		align-items: center;
		gap: var(--space-2);
	}
	.unit {
		color: var(--text-muted);
	}
	.scope {
		display: flex;
		flex-direction: column;
		gap: var(--space-1);
		margin: 0;
	}
	.scope-label {
		margin: 0;
		font-weight: 600;
	}
</style>
