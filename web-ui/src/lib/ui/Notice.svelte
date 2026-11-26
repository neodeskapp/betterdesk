<!--
  One message that must stand out on a page or in a sheet (components.css
  "notice"): a rule on the start side in the tone's colour, a faint
  background, no box. tone: info (default), warning, danger, ok.
  `message` as text, or children for a sentence with a link or button.
  Pass role="status" or role="alert" when it appears after an action.

  The tone is not only the rule's colour (WCAG 1.4.1): an icon per tone,
  and for warning, danger and ok a word for screen readers before the
  text ("Warning:"). Info is the plain case and gets no word.
-->
<script lang="ts">
	import type { Snippet } from 'svelte';
	import type { HTMLAttributes } from 'svelte/elements';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import Icon from './Icon.svelte';

	interface Props extends Omit<HTMLAttributes<HTMLDivElement>, 'class'> {
		tone?: 'info' | 'warning' | 'danger' | 'ok';
		title?: string;
		message?: string;
		class?: string;
		children?: Snippet;
	}

	let { tone = 'info', title, message, class: cls = '', children, ...rest }: Props = $props();

	const ICON = { info: 'info', warning: 'warning', danger: 'error', ok: 'check_circle' } as const;
	const WORD = { warning: 'common.warning', danger: 'common.error', ok: 'common.success' } as const;
	const word = $derived(tone === 'info' ? '' : t(WORD[tone]));
</script>

<div class={['notice', `is-${tone}`, cls]} {...rest}>
	<Icon name={ICON[tone]} class="notice-icon" />
	<div class="notice-body">
		{#if word}<span class="sr-only">{word}:</span>{/if}
		{#if title}<p class="notice-title">{title}</p>{/if}
		{#if message}<p>{message}</p>{/if}
		{@render children?.()}
	</div>
</div>

<style>
	.notice {
		display: flex;
		align-items: flex-start;
		gap: var(--space-2);
		padding: var(--space-2) var(--space-3);
		border-inline-start: 3px solid var(--info);
		background: var(--bg-subtle);
	}
	.is-danger {
		border-inline-start-color: var(--danger);
	}
	.is-warning {
		border-inline-start-color: var(--warning);
	}
	.is-ok {
		border-inline-start-color: var(--success);
	}
	.notice p {
		margin: 0;
	}
	.notice > :global(.notice-icon) {
		flex-shrink: 0;
		font-size: 18px;
		line-height: inherit;
		color: var(--info);
	}
	.is-danger > :global(.notice-icon) {
		color: var(--danger);
	}
	.is-warning > :global(.notice-icon) {
		color: var(--warning);
	}
	.is-ok > :global(.notice-icon) {
		color: var(--success);
	}
	.notice-body {
		flex: 1;
		min-width: 0;
	}
	.notice-title {
		font-weight: 600;
	}
	@media (forced-colors: active) {
		.notice {
			border-inline-start-color: CanvasText;
		}
	}
</style>
