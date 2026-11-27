<!--
  Help requests › Feedback (/help-requests/feedback): what people answered
  after quick support, newest first. On top the average stars and the
  number of answers, and a filter by helper; then one block per answer:
  the stars, who helped whom on which computer and when, and the other
  answers with the questions as they were asked. Reading needs Connect to
  devices (device.connect), the quick support permission; the form is set
  up in Settings › Quick support (server.config).

  Data: GET /api/support/feedback[?helper=<id>] on open, on a change of
  the filter and on Refresh. 50 answers show, Show more adds 50.
-->
<script lang="ts">
	import { untrack } from 'svelte';
	import { listFeedback, type FeedbackItem, type FeedbackList } from '#lib/api/feedback.ts';
	import { formatNumber } from '#lib/i18n/format.ts';
	import { tf } from '#lib/i18n/i18n.svelte.ts';
	import { link } from '#lib/nav.ts';
	import { onRefresh } from '#lib/shell/page.svelte.ts';
	import { can } from '#lib/state/session.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import Notice from '#lib/ui/Notice.svelte';
	import Select from '#lib/ui/Select.svelte';
	import States from '#lib/ui/States.svelte';
	import Time from '#lib/ui/Time.svelte';
	import Stars from '#lib/support/Stars.svelte';
	import { answerText } from '#lib/support/feedback-words.ts';
	import { feedbackRest, starsLabel } from '#lib/support/feedback-list.ts';

	const PAGE = 50;

	const allowed = $derived(can('device.connect'));
	const canSetUp = $derived(can('server.config'));

	let data = $state.raw<FeedbackList | null>(null);
	let loadError = $state('');
	let helper = $state(0);
	let shown = $state(PAGE);
	let inflight: AbortController | null = null;

	async function load() {
		inflight?.abort();
		const ctrl = new AbortController();
		inflight = ctrl;
		try {
			const next = await listFeedback(helper || undefined, ctrl.signal);
			if (inflight !== ctrl) return;
			data = next;
			loadError = '';
		} catch (err) {
			if (err instanceof DOMException && err.name === 'AbortError') return;
			if (inflight !== ctrl) return;
			loadError =
				err instanceof Error && err.message
					? err.message
					: tf('quick_support_feedback.load_failed', 'The answers could not be loaded.');
		}
	}

	$effect(() => {
		void helper;
		if (!allowed) return;
		untrack(() => {
			shown = PAGE;
			void load();
		});
	});
	$effect(() => onRefresh(() => void load()));

	const helperOptions = $derived([
		{ value: 0, label: tf('quick_support_feedback.all_helpers', 'All helpers') },
		...(data?.helpers ?? []).map((h) => ({ value: h.id, label: h.name }))
	]);

	const items = $derived((data?.feedback ?? []).slice(0, shown));

	function who(f: FeedbackItem): string {
		const person = f.name || f.email;
		const computer = f.hostname || f.device_id;
		if (person && computer)
			return tf('quick_support_feedback.helped_on', '{helper} helped {person} on {computer}', {
				helper: f.helper,
				person,
				computer
			});
		if (person)
			return tf('quick_support_feedback.helped', '{helper} helped {person}', { helper: f.helper, person });
		if (computer)
			return tf('quick_support_feedback.helped_computer', '{helper} helped on {computer}', {
				helper: f.helper,
				computer
			});
		return f.helper;
	}
</script>

<div class="page feedback-page">
	<div class="page-head">
		<h1>{tf('nav.quick_support_feedback', 'Feedback')}</h1>
		<p class="page-description">
			{tf(
				'quick_support_feedback.page_description',
				'What people answered after quick support, newest first.'
			)}
		</p>
		{#if canSetUp}
			<div class="page-actions">
				<Button icon="tune" href={link('/settings?tab=quicksupport')}
					>{tf('quick_support_feedback.set_up', 'Set up the form')}</Button
				>
			</div>
		{/if}
	</div>

	{#if !allowed}
		<Notice
			tone="info"
			message={tf(
				'quick_support_feedback.needs_connect',
				'Feedback is shown to roles that may connect to devices. Ask an administrator if you need it.'
			)}
		/>
	{:else if loadError && !data}
		<States kind="error" message={loadError} onretry={() => void load()} />
	{:else if !data}
		<States kind="loading" />
	{:else}
		<div class="summary">
			<div class="summary-figures">
				<div class="figure">
					<span class="figure-label">{tf('quick_support_feedback.average', 'Average')}</span>
					{#if data.average !== null}
						<span class="figure-value">
							<Stars value={data.average} size={20} label={starsLabel(data.average)} />
							<span aria-hidden="true">{formatNumber(data.average, { maximumFractionDigits: 1 })}</span>
						</span>
					{:else}
						<span class="figure-value muted">{tf('quick_support_feedback.no_stars', 'No stars yet')}</span>
					{/if}
				</div>
				<div class="figure">
					<span class="figure-label">{tf('quick_support_feedback.answers', 'Answers')}</span>
					<span class="figure-value">{formatNumber(data.count)}</span>
				</div>
			</div>
			{#if data.helpers.length > 1 || helper}
				<Select
					aria-label={tf('quick_support_feedback.filter_helper', 'Helper')}
					options={helperOptions}
					bind:value={helper}
				/>
			{/if}
		</div>

		{#if !data.feedback.length}
			<div class="empty">
				<p>
					{helper
						? tf('quick_support_feedback.none_for_helper', 'No answers for this helper yet.')
						: tf(
								'quick_support_feedback.none',
								'No answers yet. When feedback is on, people are asked after a quick support session they allowed.'
							)}
				</p>
				{#if canSetUp && !helper}
					<a href={link('/settings?tab=quicksupport')}
						>{tf('quick_support_feedback.set_up', 'Set up the form')}</a
					>
				{/if}
			</div>
		{:else}
			<ol class="answers" aria-label={tf('quick_support_feedback.answers', 'Answers')}>
				{#each items as f (f.session_id)}
					{@const rest = feedbackRest(f)}
					<li class="answer">
						<div class="answer-head">
							{#if f.stars !== null}
								<Stars value={f.stars} label={starsLabel(f.stars)} />
							{/if}
							<span class="answer-who">{who(f)}</span>
							<span class="answer-when"><Time value={f.answered_at} /></span>
						</div>
						{#if f.email && f.name}<p class="answer-mail">{f.email}</p>{/if}
						{#if rest.length}
							<dl class="answer-list">
								{#each rest as a (a.id)}
									<div class="answer-item">
										<dt>{a.label}</dt>
										<dd>{answerText(a.kind, a.value)}</dd>
									</div>
								{/each}
							</dl>
						{/if}
					</li>
				{/each}
			</ol>
			{#if data.feedback.length > shown}
				<div class="more">
					<Button onclick={() => (shown += PAGE)}
						>{tf('quick_support_feedback.show_more', 'Show more')}</Button
					>
					<span class="muted"
						>{tf('quick_support_feedback.shown_of', '{shown} of {count}', {
							shown: items.length,
							count: data.feedback.length
						})}</span
					>
				</div>
			{/if}
		{/if}
	{/if}
</div>

<style>
	.feedback-page {
		max-width: 52rem;
	}
	.summary {
		display: flex;
		flex-wrap: wrap;
		align-items: flex-end;
		justify-content: space-between;
		gap: var(--space-4);
		margin-block: var(--space-4) var(--space-5);
	}
	.summary-figures {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-6);
	}
	.figure {
		display: flex;
		flex-direction: column;
		gap: var(--space-1);
	}
	.figure-label {
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	.figure-value {
		display: inline-flex;
		align-items: center;
		gap: var(--space-2);
		font-size: var(--text-lg);
		font-variant-numeric: tabular-nums;
	}
	.muted {
		color: var(--text-muted);
	}
	.figure-value.muted {
		font-size: inherit;
	}
	.answers {
		list-style: none;
		margin: 0;
		padding: 0;
		display: flex;
		flex-direction: column;
	}
	.answer {
		padding-block: var(--space-4);
		display: flex;
		flex-direction: column;
		gap: var(--space-2);
	}
	.answer + .answer {
		border-top: 1px solid var(--border);
	}
	.answer-head {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-1) var(--space-3);
	}
	.answer-who {
		flex: 1;
		min-width: 12rem;
		overflow-wrap: anywhere;
	}
	.answer-when,
	.answer-mail {
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	.answer-mail {
		margin: 0;
		overflow-wrap: anywhere;
	}
	.answer-list {
		margin: 0;
		display: flex;
		flex-direction: column;
		gap: var(--space-2);
	}
	.answer-item {
		display: grid;
		grid-template-columns: minmax(8rem, 14rem) 1fr;
		gap: var(--space-1) var(--space-4);
	}
	.answer-item dt {
		color: var(--text-muted);
		overflow-wrap: anywhere;
	}
	.answer-item dd {
		margin: 0;
		white-space: pre-line;
		overflow-wrap: anywhere;
	}
	.empty {
		display: flex;
		flex-direction: column;
		gap: var(--space-2);
		color: var(--text-muted);
	}
	.empty p {
		margin: 0;
	}
	.more {
		display: flex;
		align-items: center;
		gap: var(--space-3);
		margin-top: var(--space-3);
	}
	@media (max-width: 560px) {
		.answer-item {
			grid-template-columns: 1fr;
		}
	}
</style>
