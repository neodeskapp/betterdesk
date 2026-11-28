<!--
  A widget's settings: its title and width, and what the custom widgets
  show (the count of a Single number, a Note's text, the Links, the
  Dashboards it leads to). Apply changes the dashboard being edited; it is
  saved with Done.
-->
<script lang="ts">
	import { untrack } from 'svelte';
	import { tf } from '#lib/i18n/i18n.svelte.ts';
	import { can } from '#lib/state/session.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import Checkbox from '#lib/ui/Checkbox.svelte';
	import Dialog from '#lib/ui/Dialog.svelte';
	import Field from '#lib/ui/Field.svelte';
	import IconButton from '#lib/ui/IconButton.svelte';
	import Input from '#lib/ui/Input.svelte';
	import Select from '#lib/ui/Select.svelte';
	import Textarea from '#lib/ui/Textarea.svelte';
	import {
		boardLinksOf,
		kindOf,
		LIMITS,
		linksOf,
		METRIC_INFO,
		metricAllowed,
		metricOf,
		METRICS,
		noteText,
		safeUrl,
		SIZE_LABELS,
		SIZES,
		type LinkItem,
		type Metric,
		type Widget,
		type WidgetSize
	} from './model.ts';

	interface Props {
		widget: Widget;
		/** Other dashboards the Dashboards widget can lead to */
		boards: { key: string; name: string }[];
		onapply: (change: Partial<Omit<Widget, 'id'>>) => void;
		onclose: () => void;
	}

	let { widget, boards, onapply, onclose }: Props = $props();

	const kind = untrack(() => kindOf(widget.type));
	const start = untrack(() => widget);

	let title = $state(start.title ?? '');
	let size = $state<WidgetSize>(start.size);
	let metric = $state<Metric>(metricOf(start));
	let text = $state(noteText(start));
	let links = $state<LinkItem[]>(linksOf(start).map((l) => ({ ...l })));
	let chosen = $state<string[]>(boardLinksOf(start));
	let tried = $state(false);

	const sizeOptions = SIZES.filter((s) => kind?.sizes.includes(s)).map((s) => ({
		value: s,
		label: tf(...SIZE_LABELS[s])
	}));
	const metricOptions = METRICS.filter((m) => metricAllowed(METRIC_INFO[m], can)).map((m) => ({
		value: m,
		label: tf(METRIC_INFO[m].key, METRIC_INFO[m].label)
	}));

	const linkErrors = $derived(
		links.map((l) =>
			l.url.trim() && !safeUrl(l.url)
				? tf('dashboard.link_invalid', 'Enter a console address starting with / or an http(s) address.')
				: ''
		)
	);

	function apply(e: SubmitEvent) {
		e.preventDefault();
		tried = true;
		if (linkErrors.some(Boolean)) return;
		const change: Partial<Omit<Widget, 'id'>> = { size, title: kind?.untitled ? '' : title };
		if (widget.type === 'stat') change.config = { metric };
		if (widget.type === 'note') change.config = text.trim() ? { text: text.slice(0, LIMITS.note) } : {};
		if (widget.type === 'links') {
			const kept = links
				.filter((l) => l.url.trim())
				.map((l) => ({ label: l.label.trim().slice(0, LIMITS.linkLabel), url: l.url.trim() }));
			change.config = kept.length ? { links: kept } : {};
		}
		if (widget.type === 'boards') change.config = chosen.length ? { boards: [...chosen] } : {};
		onapply(change);
	}
</script>

<Dialog
	title={tf('dashboard.widget_settings_title', 'Widget settings')}
	size={widget.type === 'links' ? 'large' : 'default'}
	{onclose}
>
	<form id="widget-settings" onsubmit={apply} novalidate>
		{#if !kind?.untitled}
			<Field
				label={tf('dashboard.widget_title', 'Title')}
				hint={tf('dashboard.widget_title_hint', 'Leave empty for the widget’s own name.')}
			>
				{#snippet children(f)}
					<Input {...f} maxlength={LIMITS.title} autocomplete="off" bind:value={title} />
				{/snippet}
			</Field>
		{/if}
		{#if sizeOptions.length > 1}
			<Field label={tf('dashboard.widget_width', 'Width')}>
				{#snippet children(f)}
					<Select {...f} width="medium" options={sizeOptions} bind:value={size} />
				{/snippet}
			</Field>
		{/if}

		{#if widget.type === 'stat'}
			<Field label={tf('dashboard.stat_metric', 'Number to show')}>
				{#snippet children(f)}
					<Select {...f} width="medium" options={metricOptions} bind:value={metric} />
				{/snippet}
			</Field>
		{:else if widget.type === 'note'}
			<Field
				label={tf('dashboard.note_text', 'Text')}
				hint={tf('dashboard.note_text_hint', 'Plain text. Line breaks are kept.')}
			>
				{#snippet children(f)}
					<Textarea {...f} rows={8} maxlength={LIMITS.note} bind:value={text} />
				{/snippet}
			</Field>
		{:else if widget.type === 'links'}
			<fieldset class="links">
				<legend>{tf('dashboard.widget.links', 'Links')}</legend>
				{#each links as l, i (i)}
					<div class="link-row">
						<Field label={tf('dashboard.link_label', 'Name')}>
							{#snippet children(f)}
								<Input {...f} maxlength={LIMITS.linkLabel} autocomplete="off" bind:value={l.label} />
							{/snippet}
						</Field>
						<Field label={tf('dashboard.link_url', 'Address')} error={tried ? linkErrors[i] : undefined}>
							{#snippet children(f)}
								<Input
									{...f}
									maxlength={LIMITS.linkUrl}
									autocomplete="off"
									spellcheck={false}
									placeholder={tf('dashboard.link_url_placeholder', '/devices or https://…')}
									bind:value={l.url}
								/>
							{/snippet}
						</Field>
						<IconButton
							icon="delete"
							class="link-remove"
							label={tf('dashboard.link_remove', 'Remove link')}
							onclick={() => links.splice(i, 1)}
						/>
					</div>
				{/each}
				{#if links.length < LIMITS.links}
					<Button icon="add" onclick={() => links.push({ label: '', url: '' })}
						>{tf('dashboard.link_add', 'Add link')}</Button
					>
				{/if}
			</fieldset>
		{:else if widget.type === 'boards'}
			<fieldset class="boards">
				<legend>{tf('dashboard.boards_choose', 'Dashboards to list')}</legend>
				<p class="hint">{tf('dashboard.boards_choose_hint', 'None ticked lists every other dashboard.')}</p>
				{#each boards as b (b.key)}
					<Checkbox
						label={b.name}
						checked={chosen.includes(b.key)}
						onchange={(e) => {
							const on = (e.currentTarget as HTMLInputElement).checked;
							chosen = on ? [...chosen, b.key] : chosen.filter((k) => k !== b.key);
						}}
					/>
				{/each}
			</fieldset>
		{/if}
	</form>
	{#snippet footer()}
		<Button onclick={onclose}>{tf('common.cancel', 'Cancel')}</Button>
		<Button variant="primary" type="submit" form="widget-settings">{tf('dashboard.apply', 'Apply')}</Button>
	{/snippet}
</Dialog>

<style>
	.links,
	.boards {
		margin: 0;
		padding: 0;
		border: 0;
		display: flex;
		flex-direction: column;
		gap: var(--space-3);
	}
	legend {
		padding: 0;
		margin-bottom: var(--space-2);
		font-weight: 600;
	}
	.link-row {
		display: grid;
		grid-template-columns: minmax(0, 1fr) minmax(0, 2fr) auto;
		gap: var(--space-2);
		align-items: end;
	}
	.link-row :global(.form-group) {
		margin-bottom: 0;
	}
	@media (max-width: 640px) {
		.link-row {
			grid-template-columns: minmax(0, 1fr) auto;
		}
	}
	.hint {
		margin: 0;
		color: var(--text-muted);
	}
</style>
