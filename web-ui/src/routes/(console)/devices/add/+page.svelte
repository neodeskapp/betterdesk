<!--
  Devices › Add computers (/devices/add): every way a computer gets into
  this console, as tabs, each for the accounts that may use it
  (lib/devices-area/add-computers.ts):
  - Invites and install links (enrollment.approve; nd-onboard fills it)
  - Device tokens (enrollment.manage; /tokens before)
  - Pairing codes (enrollment.approve; /pairing before); ?pair=1 opens
    Pair with code at once (/?activateAgent=1 leads here)
  - Support agents (the admin roles; /agents and /generator before)
  - Receiver app (enrollment.manage): the silent install of the NeoDesk app
  - Manual setup (every account; /keys, Client setup, before)
  The old addresses lead here (lib/redirects.ts). ?tab= follows the open
  tab; a tab the account doesn't have opens the first one it has. The Add
  a computer button stays on All devices.
-->
<script lang="ts">
	import { untrack } from 'svelte';
	import { page } from '$app/state';
	import { replaceState } from '$app/navigation';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import {
		ADD_TAB_DESC,
		ADD_TAB_TITLE,
		addTabs,
		pickAddTab,
		type AddTab
	} from '#lib/devices-area/add-computers.ts';
	import DeviceTokens from '#lib/devices-area/add/DeviceTokens.svelte';
	import Invites from '#lib/devices-area/add/Invites.svelte';
	import ManualSetup from '#lib/devices-area/add/ManualSetup.svelte';
	import PairingCodes from '#lib/devices-area/add/PairingCodes.svelte';
	import ReceiverApp from '#lib/devices-area/add/ReceiverApp.svelte';
	import SupportAgents from '#lib/devices-area/add/SupportAgents.svelte';
	import { can, session } from '#lib/state/session.svelte.ts';
	import Tabs from '#lib/ui/Tabs.svelte';

	const tabs = $derived(addTabs({ can, role: session.user?.role }));
	let tab = $state<AddTab>(untrack(() => pickAddTab(page.url.searchParams.get('tab'), tabs)));
	/** ?pair=1: Pair with code opens once, when the page opens on Pairing codes */
	const pair = untrack(() => page.url.searchParams.get('pair') === '1');
	const title = $derived(t('nav.devices_add'));

	function writeUrl() {
		const url = new URL(location.href);
		url.searchParams.set('tab', tab);
		url.searchParams.delete('pair');
		// This page's own address with another tab
		// eslint-disable-next-line svelte/no-navigation-without-resolve
		replaceState(url, page.state);
	}

	// The address names the tab that shows, also when it asked for one the account doesn't have
	$effect(() => {
		const raw = page.url.searchParams.get('tab');
		untrack(() => {
			const wanted = pickAddTab(raw, tabs);
			if (wanted !== tab) tab = wanted;
			if (raw !== tab) writeUrl();
		});
	});

	function pick(id: string) {
		tab = pickAddTab(id, tabs);
		writeUrl();
	}

	const items = $derived(tabs.map((id) => ({ id, label: t(ADD_TAB_TITLE[id]) })));
</script>

<div class="page page-wide devices-add-page" data-tab={tab}>
	<div class="page-head">
		<h1>{title}</h1>
		<p class="page-description">{t(ADD_TAB_DESC[tab])}</p>
	</div>

	<Tabs tabs={items} value={tab} label={title} onchange={pick}>
		{#snippet children(current)}
			{#key current}
				{#if current === 'invites'}
					<Invites />
				{:else if current === 'tokens'}
					<DeviceTokens />
				{:else if current === 'pairing'}
					<PairingCodes {pair} />
				{:else if current === 'agents'}
					<SupportAgents />
				{:else if current === 'receiver'}
					<ReceiverApp />
				{:else}
					<ManualSetup />
				{/if}
			{/key}
		{/snippet}
	</Tabs>
</div>

<style>
	.devices-add-page :global(.add-tab) {
		display: flex;
		flex-direction: column;
		gap: var(--space-6);
		min-width: 0;
	}
	/* What a tab offers to do, above its content */
	.devices-add-page :global(.add-tab-actions) {
		justify-content: flex-end;
	}
</style>
