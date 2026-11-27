<!--
  The save bar of a settings page (PATTERN.md rule 1). It appears with the
  first unsaved change and sticks to the bottom of the view; it says how
  many changes there are, lists them by name with old and new value
  (the first ones at once, the rest behind "Show all"), and offers Discard
  and Save. Ctrl+S (Cmd+S) saves while it shows. A failed save keeps the
  changes and says why. The text is a polite live region.

    <ChangesBar {draft} onsave={save} />

  onsave runs the save (usually draft.save(...) plus the toast); Discard
  puts every field back as it was loaded.
-->
<script lang="ts">
	import { tf } from '#lib/i18n/i18n.svelte.ts';
	import Button from '../Button.svelte';
	import type { SettingsDraft } from './draft.svelte.ts';

	interface Props {
		draft: SettingsDraft;
		onsave: () => unknown;
		/** The save button's words; "Save changes" by default */
		saveLabel?: string;
		/** Changes listed before "Show all" */
		visible?: number;
	}

	let { draft, onsave, saveLabel, visible = 3 }: Props = $props();

	let open = $state(false);
	const changes = $derived(draft.changes);
	const show = $derived(changes.length > 0 || draft.busy || !!draft.error);
	const listed = $derived(open ? changes : changes.slice(0, visible));
	const count = $derived(
		changes.length === 1
			? tf('settings_ui.one_change', '1 unsaved change')
			: tf('settings_ui.n_changes', '{count} unsaved changes', { count: changes.length })
	);

	/* The bar's height, for base.css: a control that gets focus scrolls
	   clear of it (WCAG 2.4.11). It grows with the listed changes. */
	let height = $state(0);
	$effect(() => {
		if (!show || !height) return;
		const root = document.documentElement.style;
		root.setProperty('--changes-bar-h', `${height}px`);
		return () => root.removeProperty('--changes-bar-h');
	});

	$effect(() => {
		if (!show) return;
		const onKey = (e: KeyboardEvent) => {
			if ((e.ctrlKey || e.metaKey) && !e.altKey && e.key.toLowerCase() === 's') {
				e.preventDefault();
				if (changes.length && !draft.busy) void onsave();
			}
		};
		window.addEventListener('keydown', onKey);
		return () => window.removeEventListener('keydown', onKey);
	});
</script>

{#if show}
	<div
		class="changes-bar"
		bind:offsetHeight={height}
		role="region"
		aria-label={tf('settings_ui.unsaved_region', 'Unsaved changes')}
	>
		<div class="changes-text" aria-live="polite">
			{#if draft.error}
				<p class="changes-error">{tf('settings_ui.not_saved', 'Not saved:')} {draft.error}</p>
			{/if}
			{#if changes.length}
				<p class="changes-count">{count}</p>
				<ul class="changes-list">
					{#each listed as c (c.field)}
						<li><span class="change-name">{c.label}:</span> {c.from} → {c.to}</li>
					{/each}
				</ul>
				{#if changes.length > visible}
					<Button variant="link" size="sm" aria-expanded={open} onclick={() => (open = !open)}
						>{open
							? tf('settings_ui.show_fewer', 'Show fewer')
							: tf('settings_ui.show_all_changes', 'Show all {count}', { count: changes.length })}</Button
					>
				{/if}
			{/if}
		</div>
		<div class="changes-buttons">
			<Button disabled={draft.busy || !changes.length} onclick={() => draft.discard()}
				>{tf('settings_ui.discard', 'Discard')}</Button
			>
			<Button variant="primary" busy={draft.busy} disabled={!changes.length} onclick={() => void onsave()}
				>{saveLabel ?? tf('settings_ui.save_changes', 'Save changes')}</Button
			>
		</div>
	</div>
{/if}

<style>
	.changes-bar {
		position: sticky;
		bottom: 0;
		z-index: 5;
		display: flex;
		flex-wrap: wrap;
		align-items: flex-end;
		justify-content: space-between;
		gap: var(--space-3) var(--space-5);
		margin-top: var(--space-5);
		padding: var(--space-3) var(--space-4);
		border: 1px solid var(--border);
		border-inline-start: 3px solid var(--accent);
		border-radius: var(--radius);
		background: var(--bg-float);
		box-shadow: var(--shadow-float);
	}
	.changes-text {
		display: flex;
		flex-direction: column;
		gap: var(--space-1);
		flex: 1 1 320px;
		min-width: 0;
	}
	.changes-text p {
		margin: 0;
	}
	.changes-count {
		font-weight: 600;
	}
	.changes-error {
		color: var(--danger);
		font-weight: 600;
	}
	.changes-list {
		margin: 0;
		padding: 0;
		list-style: none;
		color: var(--text-muted);
		font-size: var(--text-sm);
		overflow-wrap: anywhere;
	}
	.change-name {
		color: var(--text);
	}
	.changes-buttons {
		display: flex;
		gap: var(--space-2);
		margin-inline-start: auto;
	}
	/* Above the bottom navigation on phones; buttons share the row */
	@media (max-width: 767px) {
		.changes-bar {
			bottom: calc(56px + env(safe-area-inset-bottom, 0px));
		}
		.changes-buttons {
			flex: 1 1 100%;
		}
		.changes-buttons > :global(*) {
			flex: 1;
			min-height: 44px;
		}
	}
</style>
