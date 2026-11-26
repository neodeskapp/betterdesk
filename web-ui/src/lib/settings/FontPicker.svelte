<!--
  One font choice on the Appearance tab (heading or text): the font in use
  ("Now: Inter", Use default), and a search field with a list of matching
  fonts under it: Google Fonts and the built-in list (source google), or
  the fonts on the server, downloaded or uploaded (source local). Picking a
  Google font that is not on the server yet downloads it first; the
  console loads fonts only from itself, so a font that could not be
  downloaded is not used and the field says why. Arrow down moves into the
  list (the fonts are not Tab stops, so Tab goes on to the next control),
  Escape closes it, and so do a click outside and focus leaving the picker;
  only one list is open at a time (openPopup).

  In a setting row (`bare`) the row names and describes it: the picker
  draws no label or hint, its field takes the row's id (`inputId`) and
  description (`describedby`).
-->
<script lang="ts">
	import { searchFonts, downloadFont, listLocalFonts, type FontInfo } from '#lib/api/settings.ts';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import Input from '#lib/ui/Input.svelte';
	import { openPopup } from '#lib/ui/popups.svelte.ts';

	interface Props {
		slot: 'heading' | 'body';
		value: string;
		/** google: Google Fonts and the built-in list; local: the fonts on the server */
		source: 'google' | 'local';
		category: string;
		canEdit: boolean;
		disabled?: boolean;
		/** A font was downloaded to the server */
		ondownload?: () => void;
		/** In a setting row: no label or hint of its own */
		bare?: boolean;
		inputId?: string;
		describedby?: string | undefined;
	}

	let {
		slot,
		value = $bindable(),
		source,
		category,
		canEdit,
		disabled = false,
		ondownload,
		bare = false,
		inputId,
		describedby: outer
	}: Props = $props();

	const id = $derived(inputId ?? `font-${slot}-search`);
	const listId = $derived(`font-${slot}-dropdown`);

	let query = $state('');
	let open = $state(false);
	let fonts = $state.raw<FontInfo[] | null>(null);
	let problem = $state('');
	let picking = $state<string | null>(null);
	/** Why the font picked last is not used */
	let pickProblem = $state('');
	const described = $derived(
		[bare ? outer : `${id}-hint`, pickProblem && `font-${slot}-problem`].filter(Boolean).join(' ') ||
			undefined
	);
	let root = $state<HTMLElement | null>(null);
	let input = $state<HTMLInputElement | null>(null);
	let list = $state<HTMLElement | null>(null);
	let closePopup: (() => void) | null = null;
	let timer: ReturnType<typeof setTimeout> | undefined;
	let ctrl: AbortController | null = null;

	function show() {
		if (open) return;
		open = true;
		closePopup = openPopup(() => (open = false));
	}

	function hide() {
		open = false;
		closePopup?.();
		closePopup = null;
	}

	async function search() {
		ctrl?.abort();
		const c = new AbortController();
		ctrl = c;
		try {
			const q = query.trim().toLowerCase();
			const out =
				source === 'local'
					? (await listLocalFonts(c.signal))
							.filter((f) => !q || f.family.toLowerCase().includes(q))
							.map((f) => ({ family: f.family, category: '', downloaded: true }))
					: await searchFonts(query.trim(), category, true, c.signal);
			if (ctrl !== c) return;
			fonts = out;
			problem = '';
		} catch (err) {
			if (err instanceof DOMException && err.name === 'AbortError') return;
			fonts = [];
			problem = err instanceof Error && err.message ? err.message : t('errors.server_error');
		}
		show();
	}

	function oninput() {
		clearTimeout(timer);
		timer = setTimeout(() => void search(), 300);
	}

	function onfocus() {
		if (fonts === null) void search();
		else show();
	}

	// Another category or source: search again while the list is open
	$effect(() => {
		void category;
		void source;
		if (open) void search();
	});

	function onkeydown(e: KeyboardEvent) {
		if (e.key === 'Escape' && open) {
			e.preventDefault();
			e.stopPropagation();
			hide();
		} else if (e.key === 'ArrowDown') {
			e.preventDefault();
			if (!open) void search();
			else list?.querySelector<HTMLElement>('button')?.focus();
		} else if (e.key === 'Enter') {
			// Not the page's save
			e.preventDefault();
		}
	}

	function onListKey(e: KeyboardEvent) {
		const items = [...(list?.querySelectorAll<HTMLElement>('button') ?? [])];
		const at = items.indexOf(document.activeElement as HTMLElement);
		if (e.key === 'Escape') {
			e.preventDefault();
			e.stopPropagation();
			hide();
			input?.focus();
		} else if (e.key === 'ArrowDown') {
			e.preventDefault();
			items[Math.min(items.length - 1, at + 1)]?.focus();
		} else if (e.key === 'ArrowUp') {
			e.preventDefault();
			if (at <= 0) input?.focus();
			else items[at - 1]?.focus();
		}
	}

	async function pick(font: FontInfo) {
		pickProblem = '';
		if (!font.downloaded) {
			if (!canEdit) return;
			picking = font.family;
			let stored: boolean;
			try {
				stored = await downloadFont(font.family);
			} catch {
				stored = false;
			}
			picking = null;
			if (!stored) {
				// The console loads fonts only from itself: a font not on the server would not show
				pickProblem = t('settings.appearance.font_download_failed', { family: font.family });
				hide();
				input?.focus();
				return;
			}
			ondownload?.();
		}
		value = font.family;
		hide();
		input?.focus();
	}

	$effect(() => {
		if (!open) return;
		const away = (e: MouseEvent) => {
			if (root && e.target instanceof Node && !root.contains(e.target)) hide();
		};
		document.addEventListener('click', away);
		return () => document.removeEventListener('click', away);
	});

	/**
	 * Focus went to a control outside the picker: the list must not cover
	 * it. (No next element is a click, which the click handler above checks:
	 * Safari does not focus a font button when it is clicked.)
	 */
	function onfocusout(e: FocusEvent) {
		const next = e.relatedTarget;
		if (open && root && next instanceof Node && !root.contains(next)) hide();
	}

	$effect(() => () => {
		clearTimeout(timer);
		ctrl?.abort();
		closePopup?.();
	});
</script>

<div class="form-group font-slot" id="font-{slot}-slot" bind:this={root} {onfocusout}>
	{#if !bare}
		<label class="form-label" for={id}>{t(`branding.font_${slot}`)}</label>
		<p class="form-hint" id="{id}-hint">{t(`branding.font_${slot}_hint`)}</p>
	{/if}
	<p class="font-current-line">
		{t('settings.appearance.font_current')}
		<strong id="font-{slot}-current">{value || t('branding.font_system_default')}</strong>
		{#if value && !disabled}
			<Button id="font-{slot}-clear" variant="link" size="sm" onclick={() => (value = '')}
				>{t('settings.appearance.font_use_default')}</Button
			>
		{/if}
	</p>
	{#if pickProblem}<p class="form-hint form-error" id="font-{slot}-problem">{pickProblem}</p>{/if}
	<div class="font-search">
		<Input
			bind:element={input}
			{id}
			type="search"
			role="combobox"
			aria-expanded={open}
			aria-controls={listId}
			aria-autocomplete="list"
			describedby={described}
			invalid={!!pickProblem}
			autocomplete="off"
			placeholder={t('branding.font_search_placeholder')}
			{disabled}
			bind:value={query}
			{oninput}
			{onfocus}
			{onkeydown}
		/>
		<!-- svelte-ignore a11y_no_noninteractive_element_interactions (keys move between the font buttons) -->
		<div
			class="font-dropdown"
			id={listId}
			role="group"
			aria-label={t(`branding.font_${slot}`)}
			hidden={!open}
			bind:this={list}
			onkeydown={onListKey}
		>
			{#if problem}
				<p class="state is-error">{problem}</p>
			{:else if fonts && !fonts.length}
				<p class="state">{t('settings.appearance.font_none')}</p>
			{:else if fonts}
				{#each fonts as font (font.family)}
					<button
						type="button"
						class="font-item"
						tabindex="-1"
						disabled={picking !== null}
						onclick={() => void pick(font)}
					>
						<span>{font.family}</span>
						<span class="font-meta"
							>{[font.category, font.downloaded ? t('branding.font_local') : '']
								.filter(Boolean)
								.join(', ')}</span
						>
					</button>
				{/each}
			{/if}
		</div>
	</div>
</div>

<style>
	.form-group {
		display: flex;
		flex-direction: column;
		gap: var(--space-1);
		min-width: 0;
	}
	.form-label {
		font-weight: 600;
	}
	.form-hint {
		margin: 0;
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	.form-error {
		color: var(--danger);
	}
	.font-current-line {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-1) var(--space-2);
		margin: 0;
	}
	.font-search {
		position: relative;
	}
	.font-dropdown {
		position: absolute;
		inset-inline: 0;
		top: calc(100% + 2px);
		z-index: 20;
		max-height: 280px;
		overflow-y: auto;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		background: var(--bg-float);
		box-shadow: 0 6px 20px rgba(0, 0, 0, 0.16);
	}
	.font-dropdown[hidden] {
		display: none;
	}
	.font-item {
		display: flex;
		justify-content: space-between;
		gap: var(--space-3);
		width: 100%;
		min-height: var(--control-h);
		padding: var(--space-2) var(--space-3);
		border: 0;
		border-bottom: 1px solid var(--rule);
		background: transparent;
		color: var(--text);
		font: inherit;
		text-align: start;
		cursor: pointer;
	}
	.font-item:hover,
	.font-item:focus-visible {
		background: var(--hover);
	}
	.font-item:focus-visible {
		outline: var(--focus-ring);
		outline-offset: calc(-1 * var(--focus-offset) - 2px);
	}
	.font-meta {
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	.state {
		margin: 0;
		padding: var(--space-3);
		color: var(--text-muted);
	}
	.state.is-error {
		color: var(--danger);
	}
	@media (pointer: coarse) {
		.font-item {
			min-height: 44px;
		}
	}
</style>
