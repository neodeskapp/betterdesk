<!--
  Note: text someone wrote for this dashboard (instructions, contacts, a
  checklist). Shown as plain text with its line breaks; never as HTML.
-->
<script lang="ts">
	import { tf } from '#lib/i18n/i18n.svelte.ts';
	import { noteText } from '../model.ts';
	import type { WidgetProps } from './types.ts';

	let { widget }: WidgetProps = $props();
	const uid = $props.id();
	const text = $derived(noteText(widget));
</script>

<section
	class="section"
	aria-labelledby={widget.title ? `${uid}-title` : undefined}
	aria-label={widget.title ? undefined : tf('dashboard.widget.note', 'Note')}
>
	{#if widget.title}
		<div class="section-head"><h2 id="{uid}-title">{widget.title}</h2></div>
	{/if}
	{#if text.trim()}
		<p class="note">{text}</p>
	{:else}
		<p class="note empty">
			{tf('dashboard.widget.note_empty', 'This note is empty. Customize the dashboard to write in it.')}
		</p>
	{/if}
</section>

<style>
	.note {
		margin: 0;
		max-width: var(--measure);
		white-space: pre-line;
		overflow-wrap: anywhere;
	}
	.empty {
		color: var(--text-muted);
	}
</style>
