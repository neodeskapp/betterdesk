<!--
  The way back to the setup guide, on a page a guide step led to
  (?guide=<step>): one line above the page with a link to the overview and
  the step it came for. It scrolls with the page and covers nothing.
-->
<script lang="ts">
	import { page } from '$app/state';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import { link } from '#lib/nav.ts';
	import Icon from '#lib/ui/Icon.svelte';
	import { guideAvailable } from './guide.svelte.ts';
	import { guideStep, stepTitleKey } from './steps.ts';

	const step = $derived(guideAvailable() ? guideStep(page.url.searchParams) : null);
</script>

{#if step}
	<nav class="guide-return" aria-label={t('setup_guide.title')}>
		<a href={link('/')}><Icon name="arrow_back" class="guide-back" />{t('setup_guide.back')}</a>
		<span class="guide-return-step">{t('setup_guide.back_step', { step: t(stepTitleKey(step)) })}</span>
	</nav>
{/if}

<style>
	.guide-return {
		display: flex;
		flex-wrap: wrap;
		align-items: baseline;
		gap: var(--space-1) var(--space-3);
		margin-bottom: var(--space-4);
		font-size: var(--text-sm);
	}
	.guide-return a {
		display: inline-flex;
		align-items: center;
		gap: var(--space-1);
		min-height: 24px;
		font-weight: 500;
	}
	.guide-return :global(.guide-back) {
		font-size: 18px;
	}
	.guide-return :global(.guide-back:dir(rtl)) {
		transform: scaleX(-1);
	}
	.guide-return-step {
		color: var(--text-muted);
	}
</style>
