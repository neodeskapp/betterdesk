<!--
  "New entries, show them" above a list whose server pushes are held while
  live updates are paused (state/held-pushes.svelte.ts). Pressing it loads
  the list; the button then goes away, so focus moves to `focus()` (the
  list) instead of dropping to the page. When the pause ends, the held
  changes load by themselves.
-->
<script lang="ts">
	import { tick } from 'svelte';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import type { HeldPushes } from '#lib/state/held-pushes.svelte.ts';
	import { liveUpdates } from '#lib/state/live-updates.svelte.ts';
	import Button from './Button.svelte';

	interface Props {
		held: HeldPushes;
		/** Where focus goes once the entries are shown: the list */
		focus?: () => HTMLElement | null | undefined;
	}

	let { held, focus }: Props = $props();
	let busy = $state(false);

	$effect(() => {
		if (!liveUpdates.paused && held.waiting) void held.show().catch(() => undefined);
	});

	async function show() {
		busy = true;
		try {
			await held.show();
		} catch {
			// The list keeps what it shows; the button stays for another try
			return;
		} finally {
			busy = false;
		}
		await tick();
		const el = focus?.();
		if (!el) return;
		if (!el.hasAttribute('tabindex')) el.setAttribute('tabindex', '-1');
		el.focus();
	}
</script>

{#if held.waiting && liveUpdates.paused}
	<div class="new-entries">
		<Button size="sm" icon="arrow_upward" {busy} onclick={() => void show()}>{t('live.new_entries')}</Button>
	</div>
{/if}

<style>
	.new-entries {
		margin-bottom: var(--space-2);
	}
</style>
