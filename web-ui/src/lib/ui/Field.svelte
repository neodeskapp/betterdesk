<!--
  A labelled field (components.css "Form fields", "Forms that change
  settings"): label above, the control, a hint under it, the error when the
  value was refused, then what happens after a change (effect: "Applies
  right away", "After a restart"; effectTone restart or logout shows it in
  the warning colour).

  The control comes as a snippet that gets the wiring as its argument:
    <Field label={t('x.port')} hint={t('x.port_hint')} error={form.errors.port} width="short">
        {#snippet children(f)}<Input {...f} type="number" bind:value={form.values.port} />{/snippet}
    </Field>
  f.id is what the label's `for` points at; f.describedby lists the error,
  hint and effect; f.invalid sets aria-invalid; f.required sets required;
  f.width is the width class (short: ports and numbers, medium: names and
  short choices, host: host names, addresses and longer choices).

  A required field says "(required)" after its label, for everyone (WCAG
  3.3.2): with `required` on the Field, or when the control is required
  by the page itself. Screen readers hear "required" from the control, so
  the word next to the label is hidden from them.
-->
<script module lang="ts">
	export type FieldWidth = 'short' | 'medium' | 'host';

	export interface FieldControl {
		id: string;
		describedby: string | undefined;
		invalid: boolean;
		required: boolean | undefined;
		width: FieldWidth | undefined;
	}
</script>

<script lang="ts">
	import type { Snippet } from 'svelte';
	import { tf } from '#lib/i18n/i18n.svelte.ts';

	interface Props {
		label: string;
		hint?: string;
		/** Why the value was refused; empty or missing when it is fine */
		error?: string | undefined;
		effect?: string;
		effectTone?: 'default' | 'restart' | 'logout';
		width?: FieldWidth;
		/** The value can't be left empty: the control gets `required` and the label says so */
		required?: boolean;
		/** The control's id; one is made up when missing */
		id?: string;
		class?: string;
		children: Snippet<[FieldControl]>;
	}

	let {
		label,
		hint,
		error,
		// Named apart, so that $effect stays the rune
		effect: effectText,
		effectTone = 'default',
		width,
		required = false,
		id: givenId,
		class: cls = '',
		children
	}: Props = $props();

	const uid = $props.id();
	const id = $derived(givenId ?? `field-${uid}`);
	const hintId = $derived(`${id}-hint`);
	const errorId = $derived(`${id}-error`);
	const effectId = $derived(`${id}-effect`);
	const describedby = $derived(
		[error && errorId, hint && hintId, effectText && effectId].filter(Boolean).join(' ') || undefined
	);
	const control = $derived<FieldControl>({
		id,
		describedby,
		invalid: !!error,
		required: required || undefined,
		width
	});

	/* A control the page makes required itself (required or aria-required
	   on it) shows the mark too; it is watched, as pages switch it */
	let controlRequired = $state(false);
	$effect(() => {
		const el = document.getElementById(id);
		if (!el) return;
		const read = () =>
			(controlRequired = el.hasAttribute('required') || el.getAttribute('aria-required') === 'true');
		read();
		const watch = new MutationObserver(read);
		watch.observe(el, { attributes: true, attributeFilter: ['required', 'aria-required'] });
		return () => watch.disconnect();
	});
	const showRequired = $derived(required || controlRequired);
</script>

<div class={['form-group', cls]}>
	{#if showRequired}
		<!-- Next to the label, not in it: the label stays the control's exact name (voice control) -->
		<div class="form-label-line">
			<label class="form-label" for={id}>{label}</label>
			<span class="form-required" aria-hidden="true">{tf('common.required_mark', '(required)')}</span>
		</div>
	{:else}
		<label class="form-label" for={id}>{label}</label>
	{/if}
	{@render children(control)}
	{#if hint}<p class="form-hint" id={hintId}>{hint}</p>{/if}
	{#if error}<p class="form-hint form-error" id={errorId}>{error}</p>{/if}
	{#if effectText}
		<p class={['field-effect', effectTone !== 'default' && `is-${effectTone}`]} id={effectId}>{effectText}</p>
	{/if}
</div>

<style>
	.form-group {
		display: flex;
		flex-direction: column;
		gap: var(--space-1);
		margin-bottom: var(--space-4);
		min-width: 0;
	}
	.form-group:last-child {
		margin-bottom: 0;
	}
	.form-label {
		font-weight: 600;
		color: var(--text);
	}
	.form-label-line {
		display: flex;
		flex-wrap: wrap;
		align-items: baseline;
		gap: 0 var(--space-1);
	}
	.form-required {
		color: var(--text-muted);
	}
	.form-hint {
		margin: 0;
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	.form-error {
		color: var(--danger);
	}
	.field-effect {
		margin: 0;
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	.field-effect.is-restart,
	.field-effect.is-logout {
		color: var(--warning);
	}
</style>
