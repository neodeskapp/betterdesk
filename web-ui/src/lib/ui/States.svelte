<!--
  Loading, empty and error states (components.css "Loading, empty and
  error states"): one sentence, muted; an error is red and has a retry
  button when `onretry` is given. `message` defaults to "Loading..." for
  loading. With `colspan` it is a table row (td.state-cell spanning the
  columns) to put in a tbody.
  Loading is announced politely, an error right away, through the
  announcer (announce.svelte.ts): a live region that arrives together with
  its text is often not read, and this block comes and goes with the
  state. An empty state is not announced; the page's own result does that.
-->
<script lang="ts">
	import { t, tf } from '#lib/i18n/i18n.svelte.ts';
	import Button from './Button.svelte';
	import { announce } from './announce.svelte.ts';

	interface Props {
		kind?: 'loading' | 'empty' | 'error';
		message?: string;
		colspan?: number;
		retryLabel?: string;
		onretry?: () => void;
		class?: string;
	}

	let { kind = 'empty', message, colspan, retryLabel, onretry, class: cls = '' }: Props = $props();

	const text = $derived(message ?? (kind === 'loading' ? t('common.loading') : ''));

	$effect(() => {
		if (kind === 'empty' || !text) return;
		announce(text, { assertive: kind === 'error' });
	});
</script>

{#snippet content()}
	<span>{text}</span>
	{#if kind === 'error' && onretry}
		<Button size="sm" icon="retry" onclick={onretry}>{retryLabel ?? tf('actions.retry', 'Retry')}</Button>
	{/if}
{/snippet}

{#if colspan}
	<tr>
		<td class={['state-cell', kind === 'error' && 'is-error', cls]} {colspan}>
			<span class="state-line">{@render content()}</span>
		</td>
	</tr>
{:else}
	<div class={['state', kind === 'error' && 'is-error', cls]}>{@render content()}</div>
{/if}

<style>
	.state,
	.state-line {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-2) var(--space-3);
	}
	.state {
		padding: var(--space-4) 0;
		color: var(--text-muted);
	}
	.state-cell {
		padding: var(--space-4) var(--row-pad-x);
		color: var(--text-muted);
	}
	.is-error {
		color: var(--danger);
	}
</style>
