<!--
  Server status (/server-management, Server section): one page with the
  tabs Overview, Files and Services (?tab=overview|files|services follows
  the open tab; unknown or missing is the overview, as the server read it;
  ?tab=terminal is not found, +page.ts). The page and every call need
  server.config; without it the page says so and asks for nothing.
-->
<script lang="ts">
	import { untrack } from 'svelte';
	import { page } from '$app/state';
	import { replaceState } from '$app/navigation';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import { setPageTitle } from '#lib/shell/page.svelte.ts';
	import { can } from '#lib/state/session.svelte.ts';
	import { TABS, TAB_TITLE, tabFrom, type Tab } from '#lib/serverarea/management.ts';
	import NoAccess from '#lib/shell/NoAccess.svelte';
	import Button from '#lib/ui/Button.svelte';
	import ReadOnlyHint from '#lib/ui/ReadOnlyHint.svelte';
	import Tabs from '#lib/ui/Tabs.svelte';
	import { ICONS } from '#lib/util/display.ts';
	import FilesPanel from './FilesPanel.svelte';
	import Overview from './Overview.svelte';
	import ServicesPanel from './ServicesPanel.svelte';

	let tab = $state<Tab>(tabFrom(untrack(() => page.url.searchParams.get('tab'))));
	const title = $derived(t('nav.server_status'));
	const allowed = $derived(can('server.config'));

	$effect(() => setPageTitle(title));

	// A link to another tab of this page while it is open
	$effect(() => {
		const wanted = tabFrom(page.url.searchParams.get('tab'));
		untrack(() => {
			if (wanted !== tab) tab = wanted;
		});
	});

	function pick(id: string) {
		tab = tabFrom(id);
		const url = new URL(location.href);
		url.searchParams.set('tab', tab);
		// This page's own address with another tab
		// eslint-disable-next-line svelte/no-navigation-without-resolve
		replaceState(url, page.state);
	}

	const tabs = $derived(TABS.map((id: Tab) => ({ id, label: t(TAB_TITLE[id]) })));

	let services = $state<ReturnType<typeof ServicesPanel> | null>(null);
	let refreshing = $state(false);

	async function refreshServices() {
		refreshing = true;
		try {
			await services?.refresh();
		} finally {
			refreshing = false;
		}
	}
</script>

{#if !allowed}
	<NoAccess />
{:else}
	<div class="page page-wide sm-page" data-tab={tab}>
		<div class="page-head">
			<h1>{title}</h1>
			<p class="page-description">{t(`server_mgmt.${tab}_description`)}</p>
			{#if tab === 'services'}
				<div class="page-actions">
					<Button class="services-refresh" icon={ICONS.refresh} busy={refreshing} onclick={refreshServices}
						>{t('actions.refresh')}</Button
					>
				</div>
			{/if}
		</div>
		<ReadOnlyHint writes={['server.config']} />

		<Tabs {tabs} value={tab} label={title} onchange={pick}>
			{#snippet children(current)}
				{#key current}
					{#if current === 'overview'}
						<Overview />
					{:else if current === 'files'}
						<FilesPanel />
					{:else}
						<ServicesPanel bind:this={services} />
					{/if}
				{/key}
			{/snippet}
		</Tabs>
	</div>
{/if}
