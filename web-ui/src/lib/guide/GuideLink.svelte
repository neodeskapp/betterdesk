<!--
  Brings the setup guide back: a short section with one button that opens
  the guide for this account and goes to the overview, where it is. In
  Help and in Settings, for accounts with server.config only (the guide is
  about the server). level: the heading level of the place it sits in.
-->
<script lang="ts">
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import { toast } from '#lib/ui/toasts.svelte.ts';
	import { bringGuideBack, guideAvailable } from './guide.svelte.ts';

	let { level = 2, onopen }: { level?: 2 | 3; onopen?: () => void } = $props();

	const uid = $props.id();
	const available = guideAvailable();
	let busy = $state(false);

	async function show() {
		if (busy) return;
		busy = true;
		try {
			await bringGuideBack();
			onopen?.();
		} catch {
			toast.error(t('setup_guide.show_failed'));
		} finally {
			busy = false;
		}
	}
</script>

{#if available}
	<section class="section guide-link" aria-labelledby={`${uid}-title`}>
		<svelte:element this={`h${level}`} id={`${uid}-title`}>{t('setup_guide.link_title')}</svelte:element>
		<p class="section-note">{t('setup_guide.link_note')}</p>
		<div>
			<Button {busy} onclick={show}>{t('setup_guide.link_button')}</Button>
		</div>
	</section>
{/if}

<style>
	/* In the Help panel: its own section headings' size */
	.guide-link > h3 {
		margin: 0;
		font-size: var(--text-base);
		font-weight: 600;
	}
</style>
