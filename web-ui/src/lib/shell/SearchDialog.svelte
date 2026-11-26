<!--
  Global search (public/js/ui-search.js): one field for pages, things to
  do, settings, devices and people. Opened from the top bar, with Ctrl+K /
  Cmd+K anywhere, or "/" when focus is not in a field (the shell listens
  for the keys and sets `open`). Pages come from the navigation the server
  sent (only pages this person may open); settings from the Settings page's
  own list, filtered by the account's permissions (lib/settings/search.ts);
  actions from actions.ts; devices and people load on open from the APIs
  they may call, kept for a minute. Each group names how many matched.
  With the field empty, the account's recent searches show (kept in this
  browser only); one is picked to search it again.

  A modal dialog: combobox field with a listbox of results in groups.
  ArrowUp and ArrowDown move, Enter opens, Escape closes and returns
  focus. Once the arrows have moved into the list, Home and End jump to
  the first and last result; while typing they move the caret. Tab cycles
  through the field, Cancel and Clear recent searches (trapTab, as in
  Dialog); a click on the dimmed page closes. On
  phones and touch screens, which have no Escape key, Cancel next to the
  field closes it too.
-->
<script lang="ts">
	import { untrack } from 'svelte';
	import { goto } from '$app/navigation';
	import { listDevices } from '#lib/api/devices.ts';
	import { plural, t } from '#lib/i18n/i18n.svelte.ts';
	import { link } from '#lib/nav.ts';
	import { mayMakeTokens } from '#lib/people/apiTokens.ts';
	import { REVEAL_EVENT } from '#lib/settings/reveal-event.ts';
	import { isAdminRole } from '#lib/rules/access.ts';
	import { settingEntries } from '#lib/settings/search.ts';
	import { can, session } from '#lib/state/session.svelte.ts';
	import { theme } from '#lib/state/theme.svelte.ts';
	import Icon from '#lib/ui/Icon.svelte';
	import { trapTab } from '#lib/ui/focus.ts';
	import { openLayer } from '#lib/ui/layers.ts';
	import { portal } from '#lib/ui/portal.ts';
	import { searchActions } from './actions.ts';
	import { listUsersForSearch } from './api.ts';
	import { searchPages } from './nav-model.ts';
	import { runAction } from './run-action.ts';
	import {
		addRecent,
		groupResults,
		readRecent,
		recentKey,
		searchGrouped,
		writeRecent,
		type ResultKind,
		type SearchDevice,
		type SearchResult,
		type SearchUser
	} from './search.ts';

	let { open = $bindable(false) }: { open?: boolean } = $props();

	const uid = $props.id();
	const CACHE_MS = 60_000;

	let query = $state('');
	let active = $state(0);
	/** The arrows moved into the list: Home and End move there, not the caret */
	let inList = false;
	let dialog = $state<HTMLElement | null>(null);
	let input = $state<HTMLInputElement | null>(null);
	let overlay = $state<HTMLElement | null>(null);
	let devices = $state.raw<SearchDevice[] | null>(null);
	let users = $state.raw<SearchUser[] | null>(null);
	let loadedAt = 0;

	const pages = $derived(searchPages(session.nav));
	const settings = $derived(
		settingEntries({ t, can, admin: isAdminRole(session.user?.role), superAdmin: session.isSuperAdmin })
	);
	const actions = $derived(
		searchActions({
			can,
			mayMakeTokens: mayMakeTokens(session.user?.role),
			dark: theme.shown === 'dark',
			t
		})
	);
	const found = $derived(searchGrouped(query, { pages, actions, settings, devices, users }));

	/** Recent searches of this account, in this browser only */
	const storeKey = $derived(session.user ? recentKey(session.user.id) : '');
	let recent = $state<string[]>([]);
	function store(): Storage | null {
		try {
			return window.localStorage;
		} catch {
			return null;
		}
	}
	const recentResults = $derived(
		recent.map((q): SearchResult => ({
			kind: 'recent',
			title: q,
			sub: '',
			href: '',
			external: false,
			icon: 'history'
		}))
	);

	const typed = $derived(query.trim() !== '');
	const results = $derived(typed ? found.results : recentResults);
	const groups = $derived(groupResults(results));
	const optionId = (i: number) => `${uid}-opt-${i}`;

	const GROUP_KEY: Record<ResultKind, string> = {
		recent: 'search.group_recent',
		page: 'search.group_pages',
		action: 'search.group_actions',
		setting: 'search.group_settings',
		device: 'search.group_devices',
		user: 'search.group_people'
	};
	const ICON: Record<ResultKind, string> = {
		recent: 'history',
		page: 'arrow_forward',
		action: 'arrow_forward',
		setting: 'settings',
		device: 'desktop_windows',
		user: 'person'
	};
	const STATE_KEY = { online: 'status.online', offline: 'status.offline', banned: 'status.banned' } as const;

	/** "8 of 23" when the list leaves some out */
	function groupCount(kind: ResultKind, shown: number): string {
		const total = typed ? found.totals[kind] : shown;
		return total > shown ? t('search.group_count_of', { shown, total }) : String(shown);
	}

	const status = $derived(
		!typed
			? recent.length
				? ''
				: t('search.hint')
			: !results.length
				? t('search.no_results_for', { query: query.trim() })
				: plural(results.length, { one: t('search.count_one'), other: t('search.count') })
	);

	/** The highlighted result, kept inside the list when results change */
	const current = $derived(results.length ? Math.min(active, results.length - 1) : -1);

	async function loadData() {
		if (loadedAt && Date.now() - loadedAt < CACHE_MS) return;
		loadedAt = Date.now();
		const jobs: Promise<void>[] = [];
		if (can('device.view'))
			jobs.push(
				listDevices()
					.then((d) => void (devices = d))
					.catch(() => void (devices = null))
			);
		if (can('user.view'))
			jobs.push(
				listUsersForSearch()
					.then((u) => void (users = u))
					.catch(() => void (users = null))
			);
		await Promise.all(jobs);
	}

	function close() {
		open = false;
	}

	function setActive(i: number) {
		if (i < 0 || i >= results.length) return;
		active = i;
		document.getElementById(optionId(i))?.scrollIntoView({ block: 'nearest' });
	}

	function remember() {
		if (!typed || !storeKey) return;
		recent = addRecent(recent, query);
		writeRecent(store(), storeKey, recent);
	}

	function clearRecent() {
		recent = [];
		if (storeKey) writeRecent(store(), storeKey, []);
		input?.focus();
	}

	async function go(i: number) {
		const r: SearchResult | undefined = results[i];
		if (!r) return;
		if (r.kind === 'recent') {
			query = r.title;
			active = 0;
			inList = false;
			input?.focus();
			return;
		}
		remember();
		close();
		if (r.kind === 'action') {
			await runAction(r.id ?? '');
			return;
		}
		const href = link(r.href);
		// The same setting again on the page that shows it: no navigation for the page to follow
		if (r.kind === 'setting' && location.pathname + location.search + location.hash === href) {
			window.dispatchEvent(new CustomEvent(REVEAL_EVENT, { detail: r.id }));
			return;
		}
		if (r.external) {
			window.open(href, '_blank', 'noopener');
			return;
		}
		// eslint-disable-next-line svelte/no-navigation-without-resolve -- link() adds the base
		await goto(href);
	}

	function onkeydown(e: KeyboardEvent) {
		switch (e.key) {
			case 'ArrowDown':
				e.preventDefault();
				inList = true;
				setActive(Math.min(results.length - 1, current + 1));
				break;
			case 'ArrowUp':
				e.preventDefault();
				inList = true;
				setActive(Math.max(0, current - 1));
				break;
			case 'Home':
			case 'End':
				if (!inList || !results.length || e.shiftKey) break;
				e.preventDefault();
				setActive(e.key === 'Home' ? 0 : results.length - 1);
				break;
			case 'Enter':
				e.preventDefault();
				void go(current);
				break;
			case 'Escape':
				e.preventDefault();
				close();
				break;
		}
	}

	$effect(() => {
		const el = dialog;
		const back = overlay;
		if (!open || !el || !back) return;
		return untrack(() => {
			query = '';
			active = 0;
			recent = storeKey ? readRecent(store(), storeKey) : [];
			const closed = openLayer({
				kind: 'dialog',
				element: el,
				dismiss: close,
				replace: close,
				blocking: () => false
			});
			const down = (e: PointerEvent) => {
				if (e.target === back) close();
			};
			back.addEventListener('pointerdown', down);
			const tab = (e: KeyboardEvent) => trapTab(el, e);
			el.addEventListener('keydown', tab);
			inList = false;
			input?.focus();
			void loadData();
			return () => {
				back.removeEventListener('pointerdown', down);
				el.removeEventListener('keydown', tab);
				closed();
			};
		});
	});
</script>

{#if open}
	<div class="search-overlay" bind:this={overlay} {@attach portal}>
		<div
			bind:this={dialog}
			class="search-dialog"
			role="dialog"
			aria-modal="true"
			aria-label={t('search.title')}
			tabindex="-1"
		>
			<div class="search-field">
				<Icon name="search" />
				<input
					bind:this={input}
					bind:value={query}
					type="text"
					role="combobox"
					aria-expanded="true"
					aria-controls={`${uid}-results`}
					aria-autocomplete="list"
					aria-activedescendant={current >= 0 ? optionId(current) : undefined}
					aria-label={t('search.title')}
					placeholder={t('search.placeholder')}
					autocomplete="off"
					spellcheck="false"
					oninput={() => {
						active = 0;
						inList = false;
					}}
					{onkeydown}
				/>
				<button type="button" class="search-cancel" onclick={close}>{t('common.cancel')}</button>
			</div>
			<div class="search-results" id={`${uid}-results`} role="listbox" aria-label={t('search.results')}>
				{#each groups as g (g.kind)}
					<div role="group" aria-labelledby={`${uid}-g-${g.kind}`}>
						<div class="search-group" id={`${uid}-g-${g.kind}`} role="presentation">
							<span>{t(GROUP_KEY[g.kind])}</span>
							<span class="search-group-count">{groupCount(g.kind, g.items.length)}</span>
						</div>
						{#each g.items as { r, index } (index)}
							<!-- Options are chosen with the keyboard in the field; the mouse picks them here -->
							<!-- svelte-ignore a11y_click_events_have_key_events -->
							<div
								id={optionId(index)}
								class="search-result"
								role="option"
								aria-selected={index === current}
								tabindex="-1"
								onmousemove={() => (active = index)}
								onclick={() => go(index)}
							>
								<Icon name={r.icon ?? ICON[r.kind]} />
								<span class="search-result-main">
									<span class="search-result-title">{r.title}</span>
									{#if r.kind === 'device'}
										<span class="search-result-sub">
											<span class="mono">{r.id}</span>
											{#if r.sub}<span>{r.sub}</span>{/if}
											{#if r.state}<span>{t(STATE_KEY[r.state])}</span>{/if}
										</span>
									{:else if r.sub}
										<span class="search-result-sub">{r.sub}</span>
									{/if}
								</span>
							</div>
						{/each}
					</div>
				{/each}
			</div>
			<div class="search-foot">
				<p class="search-status" role="status">{status}</p>
				{#if !typed && recent.length}
					<button type="button" class="search-clear" onclick={clearRecent}>{t('search.clear_recent')}</button>
				{/if}
			</div>
			<p class="search-keys">{t('search.keys')}</p>
		</div>
	</div>
{/if}

<style>
	.search-overlay {
		position: fixed;
		inset: 0;
		z-index: var(--layer-search);
		display: flex;
		align-items: flex-start;
		justify-content: center;
		padding: 10vh var(--space-4) var(--space-4);
		background: rgba(0, 0, 0, 0.45);
	}
	.search-dialog {
		display: flex;
		flex-direction: column;
		width: min(640px, 100%);
		max-height: 70vh;
		border: 1px solid var(--rule);
		border-radius: var(--radius);
		background: var(--bg-float);
		box-shadow: var(--shadow-float);
		color: var(--text);
		text-align: start;
	}
	.search-dialog:focus {
		outline: none;
	}
	.search-field {
		position: relative;
		display: flex;
		align-items: center;
		gap: var(--space-2);
		padding: var(--space-3);
		border-bottom: 1px solid var(--rule);
	}
	.search-field > :global(.material-icons) {
		position: absolute;
		top: 50%;
		inset-inline-start: calc(var(--space-3) + var(--space-2));
		color: var(--text-muted);
		font-size: 20px;
		transform: translateY(-50%);
		pointer-events: none;
	}
	.search-field input {
		flex: 1;
		box-sizing: border-box;
		width: 100%;
		min-width: 0;
		min-height: 44px;
		padding-block: 0;
		padding-inline: calc(var(--space-2) + 20px + var(--space-2)) var(--control-pad-x-sm);
		border: 1px solid var(--border);
		border-radius: var(--control-radius);
		background: var(--bg);
		color: var(--text);
		font: inherit;
		font-size: var(--text-md);
	}
	.search-field input::placeholder {
		color: var(--text-muted);
		opacity: 1;
	}
	.search-field input:focus {
		border-color: var(--focus);
		outline: 2px solid var(--focus);
		outline-offset: -1px;
	}
	/* Only where there is no Escape key to press */
	.search-cancel {
		display: none;
		flex: none;
		min-height: 44px;
		padding: 0 var(--space-2);
		border: 0;
		border-radius: var(--radius);
		background: transparent;
		color: var(--accent);
		font: inherit;
		font-weight: var(--control-weight);
		cursor: pointer;
	}
	.search-cancel:hover {
		background: var(--hover);
	}
	@media (max-width: 767px), (pointer: coarse) {
		.search-cancel {
			display: inline-flex;
			align-items: center;
		}
	}
	.search-results {
		flex: 1;
		min-height: 0;
		padding: var(--space-1) 0;
		overflow-y: auto;
	}
	.search-results:empty {
		display: none;
	}
	.search-group {
		display: flex;
		align-items: baseline;
		gap: var(--space-2);
		padding: var(--space-3) var(--space-3) var(--space-1);
		color: var(--text-muted);
		font-size: var(--text-sm);
		font-weight: 600;
	}
	.search-group-count {
		font-weight: 400;
		font-variant-numeric: tabular-nums;
	}
	.search-result {
		display: flex;
		align-items: center;
		gap: var(--space-3);
		min-height: var(--row-h);
		padding: var(--space-1) var(--space-3);
		cursor: pointer;
	}
	.search-result > :global(.material-icons) {
		color: var(--text-muted);
		font-size: 20px;
	}
	.search-result[aria-selected='true'] {
		background: var(--accent-tint);
	}
	.search-result-main {
		display: flex;
		flex-direction: column;
		min-width: 0;
	}
	.search-result-title {
		font-weight: 600;
		overflow-wrap: anywhere;
	}
	.search-result-sub {
		display: flex;
		flex-wrap: wrap;
		gap: 0 var(--space-3);
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	.search-status,
	.search-keys {
		margin: 0;
		padding: var(--space-2) var(--space-3);
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	.search-foot {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: var(--space-2);
		border-top: 1px solid var(--rule);
	}
	.search-clear {
		flex: none;
		margin-inline-end: var(--space-2);
		padding: var(--space-1) var(--space-2);
		border: 0;
		border-radius: var(--radius);
		background: transparent;
		color: var(--accent);
		font: inherit;
		font-size: var(--text-sm);
		cursor: pointer;
	}
	.search-clear:hover {
		background: var(--hover);
	}
	.search-clear:focus-visible {
		outline: 2px solid var(--focus);
		outline-offset: 1px;
	}
	.search-keys {
		padding-top: 0;
	}
	@media (pointer: coarse) {
		.search-keys {
			display: none;
		}
	}
	@media (forced-colors: active) {
		.search-result[aria-selected='true'] {
			outline: 2px solid Highlight;
			outline-offset: -2px;
		}
	}
</style>
