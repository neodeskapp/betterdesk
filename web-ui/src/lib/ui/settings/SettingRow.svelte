<!--
  One setting (PATTERN.md rules 2, 5, 6): its name, one sentence of help
  that is always visible, the control, and under it what applies now and
  where it comes from, a dependency or "no effect" note with its fix, a
  "Changed" mark while unsaved, and Reset. An internal key (device.view,
  ENROLLMENT_MODE) is muted after the rest and copyable; it never competes
  with the name.

  An on/off setting passes `checked` and `onchange`: the row draws the
  switch itself, its name as the label.
    <SettingRow id="perm-device-view" label="View devices" help="..." checked={v} onchange={(on) => draft.set(k, on)} />
  Any other control comes as the `control` snippet, which gets the id the
  name labels and the ids that describe it:
    <SettingRow id="keep" label="Keep this many" help="...">
        {#snippet control(f)}<Input {...f} type="number" ... />{/snippet}
    </SettingRow>

  An on/off row that shows other fields while on passes `controls`, the
  id of their ShowWhen block: the switch points at it (aria-controls and
  aria-expanded), as the settings search expects.

  A row of choices (radios, a set of buttons) passes `group`: the name is
  then no <label> but the name of the group, which the control snippet
  gets as `labelledby` (role="radiogroup" aria-labelledby={f.labelledby}).

  `error` says why the value is refused ("Use a colour like #1f6feb.") at
  the row, marks the row and tells the control (`invalid`), and describes
  it (the message has the id <id>-error).

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
</script>

<script lang="ts">
	import type { Snippet } from 'svelte';
	import { tf } from '#lib/i18n/i18n.svelte.ts';
	import Button from '../Button.svelte';
	import Switch from '../Switch.svelte';
	import ValueCopy from '../ValueCopy.svelte';
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
		/** The internal name (permission key, environment variable) */
		keyName?: string;
		/** The control is a group of choices: the name labels it through `labelledby` */
		group?: boolean;
		/** Why the value is refused */
		error?: string;
		/** More words the filter finds the row by */
		terms?: readonly string[];
		/** Anything under the row (an inherited value, a list) */
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
	const nameId = $derived(`${id}-name`);
	const errorId = $derived(`${id}-error`);
	const isSwitch = $derived(checked !== undefined);
	const hasMeta = $derived(!!source || !!noEffect || needs.length > 0);
	const describedby = $derived(
		[help && helpId, error && errorId, hasMeta && metaId].filter(Boolean).join(' ') || undefined
	);

	const filter = useFilter();
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

<div
	class={[
		'setting-row',
		isSwitch ? 'is-switch' : 'is-field',
		changed && 'is-changed',
		error && 'is-refused',
		cls
	]}
	{id}
	hidden={!shown}
	data-setting={keyName ?? id}
>
	{#if isSwitch}
		<Switch
			id={controlId}
			{label}
			hint={help}
			checked={checked ?? false}
			{disabled}
			aria-describedby={hasMeta ? metaId : undefined}
			aria-controls={controls}
			aria-expanded={controls ? (checked ?? false) : undefined}
			onchange={(e) => onchange?.((e.currentTarget as HTMLInputElement).checked)}
		/>
	{:else}
		<div class="setting-text">
			{#if group}
				<span class="setting-name" id={nameId}>{label}</span>
			{:else}
				<label class="setting-name" for={controlId} id={nameId}>{label}</label>
			{/if}
			{#if help}<p class="setting-help" id={helpId}>{help}</p>{/if}
		</div>
		{#if control}<div class="setting-control">
				{@render control({ id: controlId, describedby, labelledby: nameId, invalid: !!error })}
			</div>{/if}
		{#if error}<p class="setting-error" id={errorId}>{error}</p>{/if}
	{/if}

	{#if hasMeta || changed || keyName || (onreset && resetLabel)}
		<div class="setting-meta">
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
					{#if source}
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
			<div class="setting-actions">
				{#if changed}<span class="setting-changed">{tf('settings_ui.changed', 'Changed, not saved')}</span
					>{/if}
				{#if onreset && resetLabel}
					<Button
						variant="link"
						size="sm"
						class="setting-reset"
						onclick={onreset}
						aria-label={`${resetLabel}: ${label}`}>{resetLabel}</Button
					>
				{/if}
				{#if keyName}
					<span class="setting-key"
						><span class="sr-only">{tf('settings_ui.internal_name', 'Internal name')}</span><ValueCopy
							value={keyName}
							label={keyName}
						/></span
					>
				{/if}
			</div>
		</div>
	{/if}
	{@render children?.()}
</div>

<style>
	.setting-row {
		display: flex;
		flex-direction: column;
		gap: var(--space-2);
		padding: var(--space-3) var(--row-pad-x);
		border-bottom: 1px solid var(--rule);
		border-inline-start: 3px solid transparent;
		min-width: 0;
	}
	.setting-row[hidden] {
		display: none;
	}
	.setting-row.is-changed {
		border-inline-start-color: var(--accent);
		background: var(--accent-tint);
	}
	.setting-text {
		display: flex;
		flex-direction: column;
		gap: var(--space-1);
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
	.setting-row.is-refused {
		border-inline-start-color: var(--danger);
	}
	.setting-error {
		margin: 0;
		max-width: var(--measure);
		color: var(--danger);
		font-size: var(--text-sm);
		font-weight: 600;
	}
	.setting-control {
		max-width: var(--measure);
		min-width: 0;
	}
	/* The notes line up with the switch's label */
	.is-switch > .setting-meta {
		padding-inline-start: calc(var(--check-size) + var(--check-gap));
	}
	.setting-meta {
		display: flex;
		flex-wrap: wrap;
		align-items: baseline;
		gap: var(--space-1) var(--space-3);
		font-size: var(--text-sm);
	}
	.setting-notes {
		display: flex;
		flex-direction: column;
		gap: var(--space-1);
		flex: 1 1 260px;
		min-width: 0;
	}
	.setting-notes p {
		margin: 0;
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
	.setting-actions {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-1) var(--space-3);
		margin-inline-start: auto;
	}
	.setting-changed {
		color: var(--text);
		font-weight: 600;
	}
	.setting-actions :global(.setting-key) {
		font-family: var(--mono);
		font-size: var(--text-xs);
		color: var(--text-muted);
	}
	@media (pointer: coarse) {
		.setting-row :global(.setting-reset),
		.setting-note :global(.btn) {
			min-height: 44px;
		}
	}
</style>
