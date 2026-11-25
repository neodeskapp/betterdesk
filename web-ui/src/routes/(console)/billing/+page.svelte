<!--
  Billing (/billing, Activity section; /commercialization before, which
  leads here): one page with the tabs Packages, Sessions and Reports
  (?tab=packages|sessions|reports follows the open tab; the old overview
  and settings are Packages). ?settings=1 or ?tab=settings opens the
  billing settings (#billing-settings-panel) on Packages. A warning above
  the tabs when the server clock is not in sync. The page needs
  billing.view; the tabs hide what billing.manage, billing.reports and
  billing.export allow.
-->
<script lang="ts">
	import { untrack } from 'svelte';
	import { page } from '$app/state';
	import { replaceState } from '$app/navigation';
	import { timesyncStatus } from '#lib/api/billing.ts';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import { setPageTitle } from '#lib/shell/page.svelte.ts';
	import { can } from '#lib/state/session.svelte.ts';
	import { BILLING_TABS, billingTab, opensSettings, type BillingTab } from '#lib/serverarea/billing.ts';
	import NoAccess from '#lib/shell/NoAccess.svelte';
	import Button from '#lib/ui/Button.svelte';
	import Notice from '#lib/ui/Notice.svelte';
	import ReadOnlyHint from '#lib/ui/ReadOnlyHint.svelte';
	import Tabs from '#lib/ui/Tabs.svelte';
	import { ICONS } from '#lib/util/display.ts';
	import PackagesView from './PackagesView.svelte';
	import SessionsView from './SessionsView.svelte';

	let tab = $state<BillingTab>(billingTab(untrack(() => page.url.searchParams.get('tab'))));
	const settingsOpen = $derived(
		opensSettings(page.url.searchParams) || page.url.hash === '#billing-settings-panel'
	);
	const title = $derived(t('nav.commercialization'));
	const allowed = $derived(can('billing.view'));
	const canManage = $derived(can('billing.manage'));

	$effect(() => setPageTitle(title));

	// A link to another tab of this page while it is open
	$effect(() => {
		const wanted = billingTab(page.url.searchParams.get('tab'));
		untrack(() => {
			if (wanted !== tab) tab = wanted;
		});
	});

	function pick(id: string) {
		tab = billingTab(id);
		const url = new URL(location.href);
		url.searchParams.set('tab', tab);
		url.searchParams.delete('settings');
		// This page's own address with another tab
		// eslint-disable-next-line svelte/no-navigation-without-resolve
		replaceState(url, page.state);
	}

	const tabs = $derived(BILLING_TABS.map((id) => ({ id, label: t(`commercialization.tabs.${id}`) })));

	let unsynced = $state(false);
	$effect(() => {
		if (!allowed) return;
		untrack(() =>
			timesyncStatus().then(
				(s) => (unsynced = !s.synced),
				() => {
					/* no banner when the check itself fails, as before */
				}
			)
		);
	});

	let packagesView = $state<ReturnType<typeof PackagesView> | null>(null);
</script>

{#if !allowed}
	<NoAccess />
{:else}
	<div class="page page-wide billing-page" data-tab={tab}>
		<div class="page-head">
			<h1>{title}</h1>
			{#if tab === 'packages' && canManage}
				<div class="page-actions">
					<Button
						class="package-new"
						variant="primary"
						icon={ICONS.add}
						onclick={() => packagesView?.newPackage()}>{t('commercialization.packages.new')}</Button
					>
				</div>
			{/if}
			<p class="page-description">{t(`commercialization.ui.${tab}_description`)}</p>
		</div>
		<ReadOnlyHint writes={['billing.manage', 'billing.reports']} />

		{#if unsynced}
			<Notice tone="warning" message={t('commercialization.clock.unsynced')} />
		{/if}

		<Tabs {tabs} value={tab} label={title} onchange={pick}>
			{#snippet children(current)}
				{#key current}
					{#if current === 'packages'}
						<PackagesView bind:this={packagesView} {canManage} {settingsOpen} />
					{:else}
						<SessionsView
							part={current === 'reports' ? 'reports' : 'sessions'}
							{canManage}
							canWrite={can('billing.reports')}
							canExport={can('billing.export')}
						/>
					{/if}
				{/key}
			{/snippet}
		</Tabs>
	</div>
{/if}
