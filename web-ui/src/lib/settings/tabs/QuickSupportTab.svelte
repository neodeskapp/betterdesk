<!--
  Settings › Quick support (server.config): the feedback form the quick
  support app shows the person after a session they allowed, and whether
  the app offers to keep NeoDesk on the computer afterwards (Offer to
  install after quick support, GET/PUT /api/support/receiver-offer). One draft
  with one save bar (PATTERN.md rule 1): the switch and the questions. A
  question is added and changed in a dialog (lib/support/QuestionDialog),
  moved and removed in its row; Save sends the whole form
  (PUT /api/support/feedback/form), and the answer gives new questions
  their ids. Switched on without questions, the form starts with two in
  the console's language. The answers are on Help requests › Feedback.
-->
<script lang="ts">
	import {
		getFeedbackForm,
		getReceiverOffer,
		saveFeedbackForm,
		saveReceiverOffer
	} from '#lib/api/feedback.ts';
	import { plural, t, tf } from '#lib/i18n/i18n.svelte.ts';
	import { link } from '#lib/nav.ts';
	import { can } from '#lib/state/session.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import IconButton from '#lib/ui/IconButton.svelte';
	import States from '#lib/ui/States.svelte';
	import { announce } from '#lib/ui/announce.svelte.ts';
	import ChangesBar from '#lib/ui/settings/ChangesBar.svelte';
	import SettingRow from '#lib/ui/settings/SettingRow.svelte';
	import SettingsBody from '#lib/ui/settings/SettingsBody.svelte';
	import SettingsGroup from '#lib/ui/settings/SettingsGroup.svelte';
	import { SettingsDraft, saveAndConfirm } from '#lib/ui/settings/draft.svelte.ts';
	import { ICONS } from '#lib/util/display.ts';
	import QuestionDialog from '#lib/support/QuestionDialog.svelte';
	import {
		MAX_QUESTIONS,
		addQuestion,
		canAdd,
		defaultQuestions,
		firstProblem,
		moveQuestion,
		newQuestion,
		removeQuestion,
		replaceQuestion,
		switchedOn,
		type FeedbackQuestion
	} from '#lib/support/feedback-form.ts';
	import { problemText, questionLine } from '#lib/support/feedback-words.ts';
	import { tick } from 'svelte';

	const canConfig = can('server.config');
	const errText = (err: unknown) =>
		err instanceof Error && err.message ? err.message : t('errors.server_error');

	interface Values {
		enabled: boolean;
		questions: FeedbackQuestion[];
		receiver: boolean;
		[key: string]: unknown;
	}

	const onOff = (v: unknown) => (v ? t('settings.side.on') : t('settings.side.off'));
	const count = (v: unknown) => {
		const n = Array.isArray(v) ? v.length : 0;
		return plural(n, {
			one: tf('quick_support_feedback.question_count_one', '{count} question', { count: n }),
			other: tf('quick_support_feedback.question_count_many', '{count} questions', { count: n })
		});
	};

	const draft = new SettingsDraft<Values>(
		{ enabled: false, questions: [], receiver: true },
		{
			enabled: { label: tf('quick_support_feedback.enabled', 'Ask for feedback'), format: onOff },
			questions: { label: tf('quick_support_feedback.questions', 'Questions'), format: count },
			receiver: {
				label: tf('quick_support_receiver.offer', 'Offer to install after quick support'),
				format: onOff
			}
		}
	);
	$effect(() => draft.track());

	let loaded = $state(false);
	let loadError = $state('');
	/** The question in the dialog: its place (-1 for a new one) */
	let editing = $state<{ index: number; question: FeedbackQuestion } | null>(null);

	const v = $derived(draft.values);
	const problem = $derived(firstProblem(v.questions));

	async function loadForm() {
		try {
			const [form, receiver] = await Promise.all([getFeedbackForm(), getReceiverOffer()]);
			draft.load({ enabled: form.enabled, questions: form.questions, receiver });
			loadError = '';
			loaded = true;
		} catch (err) {
			loadError = errText(err);
		}
	}

	$effect(() => {
		if (canConfig) void loadForm();
	});

	const defaults = () =>
		defaultQuestions(
			tf('quick_support_feedback.default_rating', 'How was the help?'),
			tf('quick_support_feedback.default_text', 'Anything else you want to tell us?')
		);

	function toggle(on: boolean) {
		draft.set('enabled', on);
		if (on && !v.questions.length) {
			draft.set('questions', switchedOn(v.questions, defaults));
			announce(tf('quick_support_feedback.defaults_added', 'Two questions to start with were added.'));
		}
	}

	const setQuestions = (list: FeedbackQuestion[]) => draft.set('questions', list);

	async function focusRow(i: number) {
		await tick();
		const rows = document.querySelectorAll<HTMLElement>('#qs-feedback-questions .question');
		(
			rows[Math.min(i, rows.length - 1)]?.querySelector<HTMLElement>('.question-edit') ??
			document.getElementById('qs-feedback-add')
		)?.focus();
	}

	async function move(i: number, delta: number) {
		setQuestions(moveQuestion(v.questions, i, delta));
		await tick();
		// The focus follows the question, on the same button when it is still there
		const rows = document.querySelectorAll<HTMLElement>('#qs-feedback-questions .question');
		const row = rows[i + delta];
		const same = row?.querySelector<HTMLButtonElement>(delta < 0 ? '.question-up' : '.question-down');
		(same && !same.disabled ? same : row?.querySelector<HTMLElement>('.question-edit'))?.focus();
	}

	function remove(i: number) {
		setQuestions(removeQuestion(v.questions, i));
		void focusRow(i);
	}

	function done(q: FeedbackQuestion) {
		const at = editing?.index ?? -1;
		setQuestions(at < 0 ? addQuestion(v.questions, q) : replaceQuestion(v.questions, at, q));
		editing = null;
		void focusRow(at < 0 ? v.questions.length - 1 : at);
	}

	async function save() {
		if (draft.busy) return;
		if (problem) {
			draft.error = tf(
				'settings_ui.fix_marked',
				'Some values are not right yet. They are marked on the page.'
			);
			return;
		}
		const values = $state.snapshot(draft.values) as Values;
		const formChanged = draft.isChanged('enabled') || draft.isChanged('questions');
		const receiverChanged = draft.isChanged('receiver');
		await saveAndConfirm(draft, async () => {
			let saved = { enabled: draft.loaded.enabled, questions: draft.loaded.questions };
			let receiver = draft.loaded.receiver;
			if (formChanged)
				saved = await saveFeedbackForm({ enabled: values.enabled, questions: values.questions });
			if (receiverChanged) receiver = await saveReceiverOffer(values.receiver);
			// New questions get their ids from the server
			draft.load({ enabled: saved.enabled, questions: saved.questions, receiver });
		});
	}

	const sections = $derived([
		{ id: 'qs-feedback-section', label: tf('quick_support_feedback.title', 'Feedback after quick support') },
		{ id: 'qs-receiver-section', label: tf('quick_support_receiver.title', 'Keep NeoDesk on the computer') }
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
				id="qs-feedback-section"
				title={tf('quick_support_feedback.title', 'Feedback after quick support')}
				description={tf(
					'quick_support_feedback.description',
					'When a session the person allowed has ended, the quick support app asks them these questions. Answering is up to them.'
				)}
				consequence={tf(
					'quick_support_feedback.effect',
					'Applies to the next quick support session that ends. Answers already given stay as they were asked.'
				)}
			>
				{#snippet note()}
					{#if can('device.connect')}
						<p class="group-line">
							<a href={link('/help-requests/feedback')}
								>{tf('quick_support_feedback.read_answers', 'Read the answers')}</a
							>
						</p>
					{/if}
				{/snippet}
				<SettingRow
					id="qs-feedback-enabled"
					label={tf('quick_support_feedback.enabled', 'Ask for feedback')}
					help={tf(
						'quick_support_feedback.enabled_help',
						'The person sees a short form after the session, in the quick support app.'
					)}
					checked={v.enabled}
					onchange={toggle}
					changed={draft.isChanged('enabled')}
				/>
				<SettingRow
					id="qs-feedback-questions"
					label={tf('quick_support_feedback.questions', 'Questions')}
					help={tf(
						'quick_support_feedback.questions_help',
						'In this order. At most {max}; the person may leave out the optional ones.',
						{ max: MAX_QUESTIONS }
					)}
					group
					changed={draft.isChanged('questions')}
					error={problem
						? tf('quick_support_feedback.question_problem', 'Question {n}: {problem}', {
								n: problem.index + 1,
								problem: problemText(problem.problem)
							})
						: ''}
					noEffect={v.enabled
						? undefined
						: tf('quick_support_feedback.off_note', 'Nobody is asked while feedback is off.')}
				>
					{#snippet control(f)}
						<div id={f.id} role="group" aria-labelledby={f.labelledby} aria-describedby={f.describedby}>
							{#if v.questions.length}
								<ol class="questions">
									{#each v.questions as q, i (i)}
										{@const name =
											q.label.trim() || tf('quick_support_feedback.untitled', 'Question without text')}
										<li class="question">
											<span class="question-n" aria-hidden="true">{i + 1}</span>
											<div class="question-text">
												<span class="question-label">{name}</span>
												<span class="question-line">{questionLine(q)}</span>
											</div>
											<div class="question-actions">
												<IconButton
													class="question-up"
													icon="expand_less"
													label={tf('quick_support_feedback.move_up', 'Move up: {name}', { name })}
													disabled={i === 0}
													onclick={() => void move(i, -1)}
												/>
												<IconButton
													class="question-down"
													icon="expand_more"
													label={tf('quick_support_feedback.move_down', 'Move down: {name}', { name })}
													disabled={i === v.questions.length - 1}
													onclick={() => void move(i, 1)}
												/>
												<IconButton
													class="question-edit"
													icon="edit"
													label={tf('quick_support_feedback.edit', 'Change: {name}', { name })}
													onclick={() => (editing = { index: i, question: q })}
												/>
												<IconButton
													icon={ICONS.delete}
													label={tf('quick_support_feedback.remove', 'Remove: {name}', { name })}
													onclick={() => remove(i)}
												/>
											</div>
										</li>
									{/each}
								</ol>
							{:else}
								<p class="questions-empty">
									{tf('quick_support_feedback.no_questions', 'No questions yet.')}
								</p>
							{/if}
							<div class="questions-add">
								<Button
									id="qs-feedback-add"
									icon="add"
									size="sm"
									disabled={!canAdd(v.questions)}
									onclick={() => (editing = { index: -1, question: newQuestion() })}
									>{tf('quick_support_feedback.add_question', 'Add a question')}</Button
								>
								{#if !canAdd(v.questions)}
									<span class="questions-full"
										>{tf('quick_support_feedback.full', 'The form has the most questions it can hold.')}</span
									>
								{/if}
							</div>
						</div>
					{/snippet}
				</SettingRow>
			</SettingsGroup>
			<SettingsGroup
				id="qs-receiver-section"
				title={tf('quick_support_receiver.title', 'Keep NeoDesk on the computer')}
				description={tf(
					'quick_support_receiver.description',
					'After the session the quick support app can ask the person whether to keep NeoDesk on the computer. If they agree, it installs the NeoDesk app and the computer joins this server, also when new computers need a token or approval.'
				)}
				consequence={tf(
					'quick_support_receiver.effect',
					'Applies to quick support sessions that start from now on.'
				)}
			>
				<SettingRow
					id="qs-receiver-offer"
					label={tf('quick_support_receiver.offer', 'Offer to install after quick support')}
					help={tf(
						'quick_support_receiver.offer_help',
						'The person is asked after the feedback. Installing needs administrator rights on the computer.'
					)}
					checked={v.receiver}
					onchange={(on: boolean) => draft.set('receiver', on)}
					changed={draft.isChanged('receiver')}
				/>
			</SettingsGroup>
		{/if}
	</SettingsBody>
{/if}

{#if editing}
	<QuestionDialog
		question={editing.question}
		adding={editing.index < 0}
		ondone={done}
		onclose={() => (editing = null)}
	/>
{/if}

<style>
	.group-line {
		margin: 0;
		font-size: var(--text-sm);
	}
	.questions {
		list-style: none;
		margin: 0;
		padding: 0;
		display: flex;
		flex-direction: column;
		border: 1px solid var(--border);
		border-radius: var(--radius);
	}
	.question {
		display: flex;
		align-items: center;
		gap: var(--space-3);
		padding: var(--space-2) var(--space-3);
	}
	.question + .question {
		border-top: 1px solid var(--border);
	}
	.question-n {
		flex: none;
		width: 1.5rem;
		color: var(--text-muted);
		font-variant-numeric: tabular-nums;
	}
	.question-text {
		flex: 1;
		min-width: 0;
		display: flex;
		flex-direction: column;
		gap: 2px;
	}
	.question-label {
		overflow-wrap: anywhere;
	}
	.question-line {
		color: var(--text-muted);
		font-size: var(--text-sm);
		overflow-wrap: anywhere;
	}
	.question-actions {
		flex: none;
		display: flex;
		gap: var(--space-1);
	}
	.questions-empty,
	.questions-full {
		margin: 0;
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	.questions-add {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-2) var(--space-3);
		margin-top: var(--space-3);
	}
	@media (max-width: 480px) {
		.question {
			flex-wrap: wrap;
		}
		.question-actions {
			width: 100%;
			justify-content: flex-end;
		}
	}
</style>
