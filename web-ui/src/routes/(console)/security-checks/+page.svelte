<!--
  Security checks (/security-checks, audit.view): what is wrong, beside the
  Audit log that says what happened. Two tabs: Checks (each check with
  Passed or Failed) and Hardening (the hardening settings the server runs
  with). Each tab loads when it is first opened; ?tab=checks|hardening
  follows the open tab. These were tabs of Security and audit before
  (/security-audit?tab=overview and hardening lead here, lib/redirects.ts).
-->
<script lang="ts">
	import { untrack } from 'svelte';
	import { SvelteSet } from 'svelte/reactivity';
	import { page } from '$app/state';
	import { replaceState } from '$app/navigation';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import NoAccess from '#lib/shell/NoAccess.svelte';
	import { checksTab, type ChecksTab as Tab } from '#lib/people/audit.ts';
	import { onRefresh } from '#lib/shell/page.svelte.ts';
	import { can } from '#lib/state/session.svelte.ts';
	import Tabs from '#lib/ui/Tabs.svelte';
	import ChecksTab from './ChecksTab.svelte';
	import HardeningTab from './HardeningTab.svelte';

	const allowed = can('audit.view');

	let tab = $state<Tab>(checksTab(untrack(() => page.url.searchParams.get('tab'))));
	/** Tabs opened once stay mounted (hidden), so going back does not load again */
	const opened = new SvelteSet<Tab>([untrack(() => tab)]);
	/** Bumped by Refresh: the open tab mounts again and loads */
	let generation = $state(0);

	$effect(() =>
		onRefresh(() => {
			opened.clear();
			opened.add(tab);
			generation++;
		})
	);

	function pick(id: string) {
		tab = checksTab(id);
		opened.add(tab);
		const url = new URL(location.href);
		url.searchParams.set('tab', tab);
		// This page's own address with another tab
		// eslint-disable-next-line svelte/no-navigation-without-resolve
		replaceState(url, page.state);
	}

	const tabs = $derived([
		{ id: 'checks', label: t('security_audit.tab_checks') },
		{ id: 'hardening', label: t('security_audit.tab_hardening') }
	]);
</script>

{#if !allowed}
	<NoAccess />
{:else}
	<div class="page page-wide checks-page">
		<div class="page-head">
			<h1>{t('security_audit.tab_checks')}</h1>
			<p class="page-description">{t('security_audit.checks_page_description')}</p>
		</div>

		<Tabs {tabs} value={tab} label={t('security_audit.tab_checks')} onchange={pick}>
			{#snippet children(current)}
				{#key generation}
					<div class="panels">
						{#if opened.has('checks')}
							<div hidden={current !== 'checks'}><ChecksTab /></div>
						{/if}
						{#if opened.has('hardening')}
							<div hidden={current !== 'hardening'}><HardeningTab /></div>
						{/if}
					</div>
				{/key}
			{/snippet}
		</Tabs>
	</div>
{/if}
