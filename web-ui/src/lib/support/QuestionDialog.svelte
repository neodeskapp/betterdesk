<!--
  Add or change one question of the feedback form after quick support
  (Settings > Quick support): the kind, the question as the person reads
  it, a long field for a text, the options of a choice, and whether it
  must be answered. A short form, so a centred dialog. Done hands the
  question back; the page saves it with the rest of the form.
-->
<script lang="ts">
	import { untrack } from 'svelte';
	import { tf } from '#lib/i18n/i18n.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import Checkbox from '#lib/ui/Checkbox.svelte';
	import Dialog from '#lib/ui/Dialog.svelte';
	import Field from '#lib/ui/Field.svelte';
	import Input from '#lib/ui/Input.svelte';
	import Select from '#lib/ui/Select.svelte';
	import ListEditor from '#lib/ui/settings/ListEditor.svelte';
	import {
		MAX_LABEL,
		MAX_OPTIONS,
		QUESTION_KINDS,
		cleanQuestion,
		length,
		questionProblem,
		withKind,
		type FeedbackQuestion,
		type QuestionKind
	} from './feedback-form.ts';
	import { kindName, problemText } from './feedback-words.ts';

	interface Props {
		question: FeedbackQuestion;
		/** A new question: the title and the button say Add */
		adding: boolean;
		ondone: (q: FeedbackQuestion) => void;
		onclose: () => void;
	}

	let { question, adding, ondone, onclose }: Props = $props();

	const uid = $props.id();
	let q = $state<FeedbackQuestion>(untrack(() => ({ ...question, options: [...question.options] })));
	/** Problems show once Done was pressed, not while the first word is typed */
	let tried = $state(false);

	const problem = $derived(questionProblem(cleanQuestion(q)));
	const labelProblem = $derived(
		tried && (problem === 'label_missing' || problem === 'label_too_long') ? problemText(problem) : ''
	);
	const optionProblem = $derived(
		tried && problem.startsWith('option') && problem ? problemText(problem) : ''
	);

	const kinds = $derived(QUESTION_KINDS.map((k) => ({ value: k, label: kindName(k) })));

	function done() {
		tried = true;
		if (problem) {
			document.getElementById(labelProblem ? `${uid}-label` : `${uid}-options-add`)?.focus();
			return;
		}
		ondone(cleanQuestion(q));
	}
</script>

<Dialog
	title={adding
		? tf('quick_support_feedback.add_title', 'Add a question')
		: tf('quick_support_feedback.edit_title', 'Change the question')}
	{onclose}
>
	<Field label={tf('quick_support_feedback.kind', 'Kind of answer')} id="{uid}-kind">
		{#snippet children(f)}
			<Select
				{...f}
				options={kinds}
				value={q.kind}
				onchange={(e) => (q = withKind(q, (e.currentTarget as HTMLSelectElement).value as QuestionKind))}
			/>
		{/snippet}
	</Field>
	<Field
		label={tf('quick_support_feedback.label', 'Question')}
		id="{uid}-label"
		hint={tf('quick_support_feedback.label_hint', 'As the person reads it. Up to {max} characters.', {
			max: MAX_LABEL
		})}
		error={labelProblem}
		required
	>
		{#snippet children(f)}
			<Input
				{...f}
				data-autofocus
				value={q.label}
				oninput={(e) => (q.label = (e.currentTarget as HTMLInputElement).value)}
			/>
		{/snippet}
	</Field>
	{#if length(q.label) > MAX_LABEL - 20}
		<p class="count" aria-live="polite">
			{tf('quick_support_feedback.label_count', '{count} of {max} characters', {
				count: length(q.label),
				max: MAX_LABEL
			})}
		</p>
	{/if}
	{#if q.kind === 'text'}
		<Checkbox
			label={tf('quick_support_feedback.long', 'Long answer')}
			hint={tf('quick_support_feedback.long_hint', 'A field of several lines instead of one.')}
			checked={q.long}
			onchange={(e) => (q.long = (e.currentTarget as HTMLInputElement).checked)}
		/>
	{:else if q.kind === 'choice'}
		<div class="options">
			<p class="form-label" id="{uid}-options-label">
				{tf('quick_support_feedback.options', 'Options')}
			</p>
			<p class="hint" id="{uid}-options-hint">
				{tf('quick_support_feedback.options_hint', 'The person picks one. From 2 to {max} options.', {
					max: MAX_OPTIONS
				})}
			</p>
			<ListEditor
				id="{uid}-options"
				items={q.options}
				onchange={(list) => (q.options = list)}
				itemLabel={(n) => tf('quick_support_feedback.option_n', 'Option {n}', { n })}
				addLabel={tf('quick_support_feedback.add_option', 'Add an option')}
				describedby="{uid}-options-hint{optionProblem ? ` ${uid}-options-error` : ''}"
			/>
			{#if optionProblem}<p class="error" id="{uid}-options-error">{optionProblem}</p>{/if}
		</div>
	{/if}
	<Checkbox
		label={tf('quick_support_feedback.required', 'Must be answered')}
		checked={q.required}
		onchange={(e) => (q.required = (e.currentTarget as HTMLInputElement).checked)}
	/>
	{#snippet footer()}
		<Button onclick={onclose}>{tf('common.cancel', 'Cancel')}</Button>
		<Button variant="primary" onclick={done}
			>{adding ? tf('quick_support_feedback.add', 'Add') : tf('quick_support_feedback.done', 'Done')}</Button
		>
	{/snippet}
</Dialog>

<style>
	.options {
		display: flex;
		flex-direction: column;
		gap: var(--space-2);
		margin-block: var(--space-3);
	}
	.options .form-label,
	.hint,
	.count,
	.error {
		margin: 0;
	}
	.hint,
	.count {
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	.count {
		margin-top: calc(-1 * var(--space-2));
		margin-bottom: var(--space-3);
	}
	.error {
		color: var(--danger);
		font-size: var(--text-sm);
	}
</style>
