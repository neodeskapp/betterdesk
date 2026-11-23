<!--
  The setup guide on the overview: a checklist of what a new admin does
  first, in that order. The server says which steps the account gets and
  ticks each one from what it sees (password changed, two-factor sign-in
  on, an address clients reach, HTTPS, a computer, a session, a second
  person, a backup); nothing here marks a step done. Each row leads to the
  place where the step is done: a page that offers the way back
  (GuideReturn), Add a computer right here, or the docs for HTTPS.

  Shown only while the server says the guide is open for this account
  (the first admin of a new install, or someone who brought it back from
  Help or Settings). Hide closes it for this account. Nothing shows while
  the status loads or when it cannot be read: the overview works without
  it.

  Data: GET /api/setup-guide on load, every 30 s while the tab is visible,
  on Refresh and after Add a computer closes.
-->
<script lang="ts">
	import type { Folder } from '#lib/api/devices.ts';
	import { listFolders } from '#lib/api/devices.ts';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import { link } from '#lib/nav.ts';
	import { onRefresh } from '#lib/shell/page.svelte.ts';
	import { poll } from '#lib/state/live.ts';
	import Button from '#lib/ui/Button.svelte';
	import CountLine from '#lib/ui/CountLine.svelte';
	import Icon from '#lib/ui/Icon.svelte';
	import { toast } from '#lib/ui/toasts.svelte.ts';
	import { lazy } from '#lib/util/lazy.ts';
	import { guide, guideAvailable, loadGuide, setGuideOpen } from './guide.svelte.ts';
	import { STEP_TARGETS, stepHintKey, stepTitleKey } from './steps.ts';

	const available = guideAvailable();

	$effect(() => (available ? poll(() => loadGuide(), { every: 30_000 }) : undefined));
	$effect(() => onRefresh(() => void loadGuide().catch(() => undefined)));

	const status = $derived(guide.status);
	const steps = $derived(status?.steps ?? []);
	const visible = $derived(available && !!status?.visible && steps.length > 0);
	const doneCount = $derived(steps.filter((s) => s.done).length);
	const allDone = $derived(steps.length > 0 && doneCount === steps.length);

	let hiding = $state(false);

	async function hide() {
		if (hiding) return;
		hiding = true;
		try {
			await setGuideOpen(false);
			toast.info(t('setup_guide.hidden'));
		} catch {
			toast.error(t('setup_guide.hide_failed'));
		} finally {
			hiding = false;
		}
	}

	/* ── Add a computer, on this page ── */

	const addComputer = lazy(() => import('#lib/onboard/AddComputerDialog.svelte'));
	let addOpen = $state(false);
	let folders = $state.raw<Folder[]>([]);

	async function openAddComputer() {
		folders = await listFolders().catch(() => []);
		addOpen = true;
	}

	function closeAddComputer() {
		addOpen = false;
		void loadGuide().catch(() => undefined);
	}
</script>

{#if visible}
	<section class="section setup-guide" aria-labelledby="setup-guide-title">
		<div class="section-head">
			<h2 id="setup-guide-title">{t('setup_guide.title')}</h2>
			<Button variant="link" busy={hiding} onclick={hide}>{t('setup_guide.hide')}</Button>
		</div>
		<div class="section-note" aria-live="polite">
			{#if allDone}
				<p>{t('setup_guide.all_done')}</p>
			{:else}
				<CountLine text={t('setup_guide.progress')} values={{ done: doneCount, total: steps.length }} />
			{/if}
		</div>
		<ol class="guide-steps">
			{#each steps as step (step.id)}
				{@const target = STEP_TARGETS[step.id]}
				{#snippet row()}
					<Icon
						name={step.done ? 'check_circle' : 'radio_button_unchecked'}
						class={step.done ? 'guide-mark is-done' : 'guide-mark'}
					/>
					<span class="guide-main">
						<span class="sr-only"
							>{step.done ? t('setup_guide.state_done') : t('setup_guide.state_todo')}:</span
						>
						<span class="guide-title">{t(stepTitleKey(step.id))}</span>
						<span class="guide-hint">{t(stepHintKey(step.id))}</span>
					</span>
				{/snippet}
				<li class={['guide-step', step.done && 'is-done']} data-step={step.id}>
					{#if target.kind === 'add-computer'}
						<button type="button" class="guide-row" onclick={() => void openAddComputer()}>
							{@render row()}
							<Icon name="chevron_right" class="guide-end is-chevron" />
						</button>
					{:else if target.kind === 'docs'}
						<a class="guide-row" href={target.href} target="_blank" rel="noopener noreferrer">
							{@render row()}
							<span class="sr-only">({t('common.opens_in_new_tab')})</span>
							<Icon name="open_in_new" class="guide-end" />
						</a>
					{:else}
						<a class="guide-row" href={link(target.href)}>
							{@render row()}
							<Icon name="chevron_right" class="guide-end is-chevron" />
						</a>
					{/if}
				</li>
			{/each}
		</ol>
	</section>
{/if}

{#if addOpen}
	{#await addComputer.open(() => (addOpen = false)) then { default: AddComputerDialog }}
		<AddComputerDialog {folders} onclose={closeAddComputer} />
	{/await}
{/if}

<style>
	.setup-guide .section-note > :global(p) {
		margin: 0;
	}
	/* Rows on rules, like a RowList, hanging their padding outside the column */
	.guide-steps {
		margin: 0 calc(-1 * var(--row-pad-x));
		padding: 0;
		list-style: none;
		border-top: 1px solid var(--rule);
	}
	.guide-step {
		display: block;
		border-bottom: 1px solid var(--rule);
	}
	.guide-row {
		display: flex;
		align-items: flex-start;
		gap: var(--space-3);
		width: 100%;
		min-height: var(--row-h);
		padding: var(--row-pad-y) var(--row-pad-x);
		border: 0;
		background: transparent;
		color: var(--text);
		font: inherit;
		text-align: start;
		text-decoration: none;
		cursor: pointer;
	}
	.guide-row:hover {
		background: var(--bg-subtle);
		color: var(--text);
	}
	.guide-main {
		display: flex;
		flex: 1;
		flex-direction: column;
		min-width: 0;
	}
	.guide-title {
		font-weight: 600;
	}
	.guide-hint {
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	.is-done .guide-title {
		font-weight: 400;
		color: var(--text-muted);
	}
	.guide-row :global(.guide-mark) {
		flex: none;
		margin-top: 1px;
		color: var(--text-muted);
		font-size: 20px;
	}
	.guide-row :global(.guide-mark.is-done) {
		color: var(--success);
	}
	.guide-row :global(.guide-end) {
		flex: none;
		align-self: center;
		color: var(--text-muted);
		font-size: 20px;
	}
	.guide-row :global(.is-chevron:dir(rtl)) {
		transform: scaleX(-1);
	}
</style>
