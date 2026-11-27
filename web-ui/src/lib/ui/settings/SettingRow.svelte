<!--
  One setting (settings-pass-2.md rules A to E): its name and one sentence
  of help on the left, the control on the right, aligned down the page. The
  row is two columns once its group is at least 900 px wide (a container
  query on SettingsGroup's rows) and stacks below that, or outside a group.

  An on/off setting passes `checked` and `onchange`: the row draws the
  switch itself, its name as the label (the box stays at the start of its
  name; Reset and "Changed" go to the right column).
    <SettingRow id="perm-device-view" label="View devices" help="..." checked={v} onchange={(on) => draft.set(k, on)} />
  Any other control comes as the `control` snippet, which gets the id the
  name labels and the ids that describe it:
    <SettingRow id="keep" label="Keep this many" help="...">
        {#snippet control(f)}<Input {...f} type="number" ... />{/snippet}
    </SettingRow>

  An on/off row that shows other fields while on passes `controls`, the
  id of their ShowWhen block: the switch points at it (aria-controls and
  aria-expanded), as the settings search expects.

  A row of choices (radios, ChoiceSegments) passes `group`: the name is
  then no <label> but the name of the group, which the control snippet
  gets as `labelledby` (role="radiogroup" aria-labelledby={f.labelledby}).

  `source` says where the value comes from. It shows as a muted line only
  when it says something the control does not: a value from elsewhere
  (kind inherited or environment) or one with a link. "Set here", "No
  rule" and "Built-in default" are the control's own state: they stay in
  the control's description for screen readers, and a value set here
  (kind here) marks the row with the accent rule on the start side. Pass
  `source.visible` to show or hide the line regardless.

  Reset (`onreset` with `resetLabel`) and the "Changed, not saved" word
  sit next to the control. `error` says why the value is refused at the
  control, marks the row and tells the control (`invalid`); the message
  has the id <id>-error.

  `keyName` (device.view, ENROLLMENT_MODE) is not shown: the filter finds
  the row by it, it is the row's data-setting and the name's title.
  `children` go under the row across both columns (a list, a compare line).

  The filter of the page (filter.svelte.ts) hides rows that don't match
  their name, help, key or `terms`.
-->
<script module lang="ts">
	export type SourceKind = 'here' | 'inherited' | 'default' | 'none' | 'environment';

	export interface ValueSource {
		kind: SourceKind;
		/** The sentence: "Set for this organization", "From the server: Direct first" */
		text: string;
		/** Where the inherited value is set, with the link's words */
		href?: string;
		linkLabel?: string;
		/**
		 * Show the line (true) or keep it for screen readers only (false).
		 * Left out: shown for inherited and environment values and for any
		 * line with a link, kept quiet for here, default and none.
		 */
		visible?: boolean;
	}

	export interface Dependency {
		/** "Needs View devices." */
		text: string;
		/** "Turn it on" and what it does; left out when nothing can be done here */
		fixLabel?: string;
		onfix?: () => void;
	}

	export interface RowControl {
		id: string;
		describedby: string | undefined;
		/** The name's id: for a group of choices (`group`), which a <label> cannot name */
		labelledby: string;
		/** The value is refused (`error`) */
		invalid: boolean;
	}

	/** Whether a source line says something the control does not (rule C) */
	export function sourceShown(source: ValueSource | null | undefined): boolean {
		if (!source) return false;
		if (source.visible !== undefined) return source.visible;
		return source.kind === 'inherited' || source.kind === 'environment' || !!source.href;
	}
</script>

<script lang="ts">
	import type { Snippet } from 'svelte';
	import { tf } from '#lib/i18n/i18n.svelte.ts';
	import Button from '../Button.svelte';
	import Switch from '../Switch.svelte';
	import { useFilter, useGroup } from './filter.svelte.ts';

	interface Props {
		/** The row's element id: the settings search and #links reach it */
		id: string;
		label: string;
		help?: string;
		/** On/off rows: the value, and what a click does */
		checked?: boolean;
		onchange?: (on: boolean) => void;
		/** On/off rows: the id of the block the switch shows while on */
		controls?: string;
		disabled?: boolean;
		control?: Snippet<[RowControl]>;
		source?: ValueSource | null;
		/** Changed and not saved yet */
		changed?: boolean;
		/** Back to the default or the inherited value; the button shows only with a label */
		onreset?: () => void;
		resetLabel?: string;
		needs?: readonly Dependency[];
		/** Why the setting does nothing in the current state */
		noEffect?: string;
		/** The internal name (permission key, environment variable): filter, data-setting, the name's title */
		keyName?: string;
		/** The control is a group of choices: the name labels it through `labelledby` */
		group?: boolean;
		/** Why the value is refused */
		error?: string;
		/** More words the filter finds the row by */
		terms?: readonly string[];
		/** Anything under the row, across both columns (an inherited value, a list) */
		children?: Snippet;
		class?: string;
	}

	let {
		id,
		label,
		help,
		checked,
		onchange,
		controls,
		disabled = false,
		control,
		source = null,
		changed = false,
		onreset,
		resetLabel,
		needs = [],
		noEffect,
		keyName,
		terms = [],
		group = false,
		error = '',
		children,
		class: cls = ''
	}: Props = $props();

	const controlId = $derived(`${id}-control`);
	const helpId = $derived(`${id}-help`);
	const metaId = $derived(`${id}-meta`);
	const quietId = $derived(`${id}-source`);
	const nameId = $derived(`${id}-name`);
	const errorId = $derived(`${id}-error`);
	const isSwitch = $derived(checked !== undefined);
	const loud = $derived(sourceShown(source));
	const quiet = $derived(!!source && !loud);
	const hasMeta = $derived(loud || !!noEffect || needs.length > 0);
	const hasActions = $derived(changed || !!(onreset && resetLabel));
	const describedby = $derived(
		[help && helpId, error && errorId, hasMeta && metaId, quiet && quietId].filter(Boolean).join(' ') ||
			undefined
	);
	const switchDescribedby = $derived(
		[hasMeta && metaId, quiet && quietId].filter(Boolean).join(' ') || undefined
	);

	const filterOf = useFilter();
	const filter = $derived(filterOf());
	const groupRows = useGroup();
	const shown = $derived(!filter || filter.matches([label, help, keyName, ...terms]));

	$effect(() => {
		const visible = shown;
		filter?.shown.set(id, visible);
		groupRows?.rows.set(id, visible);
	});
	$effect(() => () => {
		filter?.shown.delete(id);
		groupRows?.rows.delete(id);
	});
</script>

{#snippet actions()}
	{#if hasActions}
		<div class="setting-actions">
			{#if changed}<span class="setting-changed">{tf('settings_ui.changed', 'Changed, not saved')}</span>{/if}
			{#if onreset && resetLabel}
				<Button
					variant="link"
					size="sm"
					class="setting-reset"
					onclick={onreset}
					aria-label={`${resetLabel}: ${label}`}>{resetLabel}</Button
				>
			{/if}
		</div>
	{/if}
{/snippet}

<div
	class={[
		'setting-row',
		isSwitch ? 'is-switch' : 'is-field',
		source?.kind === 'here' && 'is-set',
		changed && 'is-changed',
		error && 'is-refused',
		cls
	]}
	{id}
	hidden={!shown}
	data-setting={keyName ?? id}
>
	<div class="setting-text">
		{#if isSwitch}
			<Switch
				id={controlId}
				{label}
				hint={help}
				checked={checked ?? false}
				{disabled}
				title={keyName}
				aria-describedby={switchDescribedby}
				aria-controls={controls}
				aria-expanded={controls ? (checked ?? false) : undefined}
				onchange={(e) => onchange?.((e.currentTarget as HTMLInputElement).checked)}
			/>
		{:else}
			{#if group}
				<span class="setting-name" id={nameId} title={keyName}>{label}</span>
			{:else}
				<label class="setting-name" for={controlId} id={nameId} title={keyName}>{label}</label>
			{/if}
			{#if help}<p class="setting-help" id={helpId}>{help}</p>{/if}
		{/if}
		{#if hasMeta}
			<div class="setting-notes" id={metaId}>
				{#if noEffect}<p class="setting-note is-warning">{noEffect}</p>{/if}
				{#each needs as need (need.text)}
					<p class="setting-note is-warning">
						{need.text}
						{#if need.fixLabel && need.onfix}
							<Button variant="link" size="sm" onclick={need.onfix}>{need.fixLabel}</Button>
						{/if}
					</p>
				{/each}
				{#if source && loud}
					<p class={['setting-source', `is-${source.kind}`]}>
						{source.text}
						<!-- An address inside the console, as the page gave it -->
						<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -->
						{#if source.href}<a href={source.href}
								>{source.linkLabel ?? tf('settings_ui.open_source', 'Open')}</a
							>{/if}
					</p>
				{/if}
			</div>
		{/if}
		{#if source && quiet}<span class="sr-only" id={quietId}>{source.text}</span>{/if}
	</div>

	{#if isSwitch}
		{@render actions()}
	{:else if control || error || hasActions}
		<div class="setting-control">
			{#if control}<div class="setting-control-main">
					{@render control({ id: controlId, describedby, labelledby: nameId, invalid: !!error })}
				</div>{/if}
			{@render actions()}
			{#if error}<p class="setting-error" id={errorId}>{error}</p>{/if}
		</div>
	{/if}

	{#if children}<div class="setting-extra">{@render children()}</div>{/if}
</div>

<style>
	.setting-row {
		display: grid;
		grid-template-columns: minmax(0, 1fr);
		align-items: start;
		gap: var(--space-2) var(--space-5);
		padding: var(--space-2) var(--row-pad-x);
		border-bottom: 1px solid var(--rule);
		border-inline-start: 3px solid transparent;
		min-width: 0;
	}
	.setting-row[hidden] {
		display: none;
	}
	/* A value set here (not inherited): the accent rule; while unsaved also the tint */
	.setting-row.is-set {
		border-inline-start-color: var(--accent);
	}
	.setting-row.is-changed {
		border-inline-start-color: var(--accent);
		background: var(--accent-tint);
	}
	.setting-row.is-refused {
		border-inline-start-color: var(--danger);
	}
	.setting-text {
		display: flex;
		flex-direction: column;
		gap: 2px;
		min-width: 0;
	}
	.setting-name {
		font-weight: 600;
		color: var(--text);
	}
	.setting-help {
		margin: 0;
		max-width: var(--measure);
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	.setting-control {
		display: flex;
		flex-wrap: wrap;
		align-items: flex-start;
		gap: var(--space-1) var(--space-3);
		min-width: 0;
		max-width: var(--measure);
	}
	.setting-control-main {
		flex: 1 1 12rem;
		min-width: 0;
	}
	/* A set of segments keeps its width; Reset wraps under it instead */
	.setting-control-main:has(> :global(.choice-segments)) {
		flex: 0 0 auto;
	}
	.setting-error {
		flex-basis: 100%;
		margin: 0;
		color: var(--danger);
		font-size: var(--text-sm);
		font-weight: 600;
	}
	.setting-extra {
		grid-column: 1 / -1;
		min-width: 0;
	}
	.setting-extra:empty {
		display: none;
	}
	.setting-notes {
		display: flex;
		flex-direction: column;
		gap: 2px;
		margin-top: 2px;
		min-width: 0;
		font-size: var(--text-sm);
	}
	/* The notes line up with the switch's label */
	.is-switch .setting-notes {
		padding-inline-start: calc(var(--check-size) + var(--check-gap));
	}
	.setting-notes p {
		margin: 0;
		max-width: var(--measure);
	}
	.setting-source {
		color: var(--text-muted);
	}
	.setting-source a {
		margin-inline-start: var(--space-1);
	}
	.setting-note.is-warning {
		color: var(--warning);
	}
	.setting-note :global(.btn) {
		margin-inline-start: var(--space-1);
	}
	/* As high as a control, so Reset lines up with the control's first line; at the column's end, so the Resets line up down the page */
	.setting-actions {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-1) var(--space-3);
		min-height: var(--control-h);
		margin-inline-start: auto;
		font-size: var(--text-sm);
	}
	.is-switch > .setting-actions {
		margin-inline-start: 0;
		padding-inline-start: calc(var(--check-size) + var(--check-gap));
	}
	.setting-changed {
		color: var(--text);
		font-weight: 600;
	}
	/* Two columns once the group's rows are wide enough: name and help on
	   the left, the control in a fixed column on the right */
	@container settings-rows (min-width: 900px) {
		.setting-row {
			grid-template-columns: minmax(0, 1fr) var(--setting-control-w, 30rem);
		}
		.setting-control {
			max-width: none;
		}
		.is-switch > .setting-actions {
			margin-inline-start: auto;
			padding-inline-start: 0;
			min-height: 24px;
		}
	}
	@media (pointer: coarse) {
		.setting-row :global(.setting-reset),
		.setting-note :global(.btn) {
			min-height: 44px;
		}
	}
</style>
