<!--
  The top bar (views/partials/ux35-topbar.ejs): the logo mark below
  1100 px, with the drawer button from 768 to 1099 px; the breadcrumb
  (section, page), the global search, Quick support (device.connect; its
  name from 1280 px, an icon below), notifications, Help (an icon that
  opens the Help panel) and the account menu at the end, at every width.
  Theme, language and accessibility are in the account menu under
  Preferences. Pages whose data others change load it again by themselves
  (lib/state/live.ts reloadWhileShown), so there is no Refresh.

  Phones (767 px and below): logo, search, Quick support, notifications
  and the account. The bottom bar's More opens the sidebar, which has Help
  at its end.

  The bar never gets wider than the screen: its buttons keep their width
  and their one line, the breadcrumb takes the room that is left, and the
  search field gives way first (down to its icon). A short breadcrumb shows
  only the current page. Below 420 px the breadcrumb is gone from view;
  hidden names stay for screen readers.
-->
<script lang="ts">
	import { i18n, t } from '#lib/i18n/i18n.svelte.ts';
	import { link } from '#lib/nav.ts';
	import { closeQuickSupport, openQuickSupport, quickSupport } from '#lib/onboard/open.svelte.ts';
	import QuickSupportDialog from '#lib/onboard/QuickSupportDialog.svelte';
	import { can, session } from '#lib/state/session.svelte.ts';
	import IconButton from '#lib/ui/IconButton.svelte';
	import { ICONS } from '#lib/util/display.ts';
	import Icon from '#lib/ui/Icon.svelte';
	import AccountMenu from './AccountMenu.svelte';
	import BrandLogo from './BrandLogo.svelte';
	import ChatButton from '#lib/chat/ChatButton.svelte';
	import NotificationsMenu from './NotificationsMenu.svelte';
	import { shortcutHint } from './search.ts';

	interface Props {
		crumbs: string[];
		drawer: boolean;
		drawerOpen: boolean;
		sidebarId: string;
		helpOpen: boolean;
		helpId: string;
		helpButton?: HTMLButtonElement | null;
		ondrawer: (opener: HTMLElement) => void;
		onsearch: () => void;
		onhelp: () => void;
	}

	let {
		crumbs,
		drawer,
		drawerOpen,
		sidebarId,
		helpOpen,
		helpId,
		helpButton = $bindable(null),
		ondrawer,
		onsearch,
		onhelp
	}: Props = $props();

	const isMac =
		typeof navigator !== 'undefined' && /Mac|iP(hone|ad|od)/.test(navigator.platform || navigator.userAgent);
	const kbd = $derived(shortcutHint(i18n.lang, isMac));
</script>

<header class="shell-topbar">
	{#if drawer}
		<!-- From 768 to 1099 px; on phones the bottom bar's More opens the sidebar -->
		<span class="menu-btn">
			<IconButton
				icon="menu"
				label={t('ux35.open_menu')}
				aria-controls={sidebarId}
				aria-expanded={drawerOpen}
				onclick={(e) => ondrawer(e.currentTarget)}
			/>
		</span>
		<a class="topbar-logo" href={link('/')} title={session.appName}>
			<BrandLogo mark name={session.appName} /><span class="sr-only">{session.appName}</span>
		</a>
	{/if}

	<nav class="shell-breadcrumb" aria-label={t('ux35.breadcrumb')}>
		{#each crumbs as crumb, i (i)}
			{#if i > 0}<span class="crumb-sep" aria-hidden="true">/</span>{/if}
			{#if i === crumbs.length - 1}
				<span class="crumb current" aria-current="page">{crumb}</span>
			{:else}
				<span class="crumb">{crumb}</span>
			{/if}
		{/each}
	</nav>

	<button
		type="button"
		class="search-trigger"
		aria-haspopup="dialog"
		aria-keyshortcuts="Control+K Meta+K /"
		onclick={onsearch}
	>
		<span class="search-field">
			<Icon name="search" />
			<span class="search-label">{t('search.placeholder')}</span>
			<kbd class="search-kbd" aria-hidden="true">{kbd}</kbd>
		</span>
	</button>

	<div class="topbar-actions">
		{#if can('device.connect')}
			<button
				type="button"
				class="topbar-text-btn quick-support-btn"
				aria-haspopup="dialog"
				title={t('onboard.support.button')}
				onclick={() => openQuickSupport()}
				><Icon name={ICONS.help_request} /><span class="quick-support-label"
					>{t('onboard.support.button')}</span
				></button
			>
		{/if}
		{#if can('chat.support') || can('chat.team')}<ChatButton />{/if}
		<NotificationsMenu />
		<!-- Help: on phones it is at the end of the sidebar -->
		<span class="help-btn">
			<IconButton
				bind:element={helpButton}
				icon="menu_book"
				label={t('nav.help')}
				aria-haspopup="dialog"
				aria-expanded={helpOpen}
				aria-controls={helpOpen ? helpId : undefined}
				onclick={onhelp}
			/>
		</span>
		<AccountMenu />
	</div>
</header>

{#if quickSupport.open}
	{#key quickSupport.session}
		<QuickSupportDialog session={quickSupport.session} onclose={closeQuickSupport} />
	{/key}
{/if}

<style>
	.quick-support-btn {
		display: inline-flex;
		align-items: center;
		gap: var(--space-1);
	}
	.shell-topbar {
		/* The bar's icon buttons are the default control height, like Help and search */
		--icon-btn-size: var(--control-h);
		position: sticky;
		/* Below the Sign in as bar while there is one */
		top: var(--viewing-banner-h, 0px);
		z-index: var(--layer-topbar);
		display: flex;
		align-items: center;
		gap: var(--space-2);
		box-sizing: border-box;
		height: var(--topbar-h);
		padding-block: 0;
		padding-inline: var(--page-pad) var(--space-3);
		border-bottom: 1px solid var(--rule);
		background: var(--bg);
	}
	.topbar-logo {
		display: inline-flex;
		flex: none;
		align-items: center;
		min-height: var(--control-h);
		padding-inline: var(--space-1);
		color: var(--text);
	}
	/* The breadcrumb takes the room the buttons leave. Its own text never
	   counts (it is a size container), so it never pushes them off the
	   screen; when room is short the search field shrinks first */
	.shell-breadcrumb {
		display: flex;
		flex: 1 1 10rem;
		align-items: center;
		gap: var(--space-2);
		min-width: 0;
		overflow: hidden;
		container: crumbs / inline-size;
		color: var(--text-muted);
		font-size: var(--text-sm);
		white-space: nowrap;
	}
	.topbar-logo + .shell-breadcrumb {
		padding-inline-start: var(--space-3);
		border-inline-start: 1px solid var(--rule);
	}
	.crumb.current {
		overflow: hidden;
		color: var(--text);
		text-overflow: ellipsis;
	}
	@container crumbs (max-width: 180px) {
		.crumb:not(.current),
		.crumb-sep {
			display: none;
		}
	}

	/* Global search: looks like a field, opens the search dialog. When the
	   bar is short of room it shrinks, and below about 120 px it is an icon
	   button like the others */
	.search-trigger {
		display: inline-flex;
		flex: 0 10 min(380px, 30vw);
		align-items: center;
		min-width: var(--control-h);
		margin-inline-end: var(--space-3);
		padding: 0;
		border: 0;
		border-radius: var(--radius);
		background: transparent;
		color: var(--text-muted);
		font: inherit;
		text-align: start;
		cursor: pointer;
		container: search-trigger / inline-size;
	}
	.search-field {
		display: flex;
		flex: 1;
		align-items: center;
		gap: var(--space-2);
		box-sizing: border-box;
		min-width: 0;
		min-height: var(--control-h-sm);
		padding: 0 var(--space-2);
		border: 1px solid var(--border);
		border-radius: var(--radius);
		background: var(--bg);
	}
	.search-trigger:hover .search-field {
		border-color: var(--text-muted);
	}
	.search-field > :global(.material-icons) {
		font-size: 20px;
	}
	.search-label {
		flex: 1;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.search-kbd {
		flex: none;
		padding: 0 var(--space-1);
		border: 1px solid var(--rule);
		border-radius: var(--radius-sm);
		color: var(--text-muted);
		font-family: var(--font);
		font-size: var(--text-xs);
	}
	@container search-trigger (max-width: 120px) {
		.search-field {
			justify-content: center;
			min-height: var(--control-h);
			padding: 0;
			border-color: transparent;
			background: transparent;
		}
		.search-trigger:hover .search-field {
			border-color: transparent;
			background: var(--hover);
			color: var(--text);
		}
		.search-label {
			position: absolute;
			width: 1px;
			height: 1px;
			overflow: hidden;
			clip: rect(0, 0, 0, 0);
		}
		.search-kbd {
			display: none;
		}
	}

	.topbar-actions {
		display: flex;
		flex: none;
		align-items: center;
		gap: 2px;
	}
	/* Quick support: a supporter's most common start, with its name where there is room */
	.topbar-text-btn {
		flex: none;
		min-height: var(--control-h);
		padding: 0 var(--space-3);
		border: 0;
		border-radius: var(--radius);
		background: transparent;
		color: var(--text);
		font: inherit;
		white-space: nowrap;
		cursor: pointer;
	}
	.topbar-text-btn:hover {
		background: var(--hover);
	}
	.menu-btn,
	.help-btn {
		display: inline-flex;
	}

	/* Below 1280 px Quick support is an icon; its name stays for screen readers */
	@media (max-width: 1279px) {
		.quick-support-label {
			position: absolute;
			width: 1px;
			height: 1px;
			overflow: hidden;
			clip: rect(0, 0, 0, 0);
			white-space: nowrap;
		}
		.quick-support-btn {
			justify-content: center;
			width: var(--icon-btn-size);
			padding: 0;
		}
	}

	@media (max-width: 767px), (pointer: coarse) {
		.search-kbd {
			display: none;
		}
	}

	@media (max-width: 1099px) {
		.shell-topbar {
			padding-inline: var(--space-2) var(--space-3);
		}
	}

	@media (max-width: 767px) {
		.shell-topbar {
			gap: var(--space-1);
			padding-inline: var(--space-2);
		}
		.topbar-logo + .shell-breadcrumb {
			padding-inline-start: var(--space-2);
		}
		.crumb:not(.current),
		.crumb-sep {
			display: none;
		}
		.search-trigger {
			flex: none;
			width: var(--control-h);
			margin-inline-end: 0;
		}
		.menu-btn,
		.help-btn {
			display: none;
		}
	}
	/* Narrow phones: no room for a page name worth reading (the page's own
	   heading says it); screen readers still hear it */
	@media (max-width: 419px) {
		.topbar-logo + .shell-breadcrumb {
			padding-inline-start: 0;
			border-inline-start: 0;
		}
		.crumb.current {
			position: absolute;
			width: 1px;
			height: 1px;
			overflow: hidden;
			clip: rect(0, 0, 0, 0);
			white-space: nowrap;
		}
	}
	@media (pointer: coarse) {
		.topbar-text-btn {
			min-height: 44px;
		}
		.topbar-logo {
			min-width: 44px;
			min-height: 44px;
			justify-content: center;
		}
		.shell-topbar {
			--icon-btn-size: 44px;
		}
	}
</style>
