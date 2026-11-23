<!--
  The device panel, mounted once by the shell. Opens whenever
  devicePanel.id is set (state/devicePanel.svelte.ts) and closes when it is
  cleared. The panel's code loads with the first device that opens, so
  pages that never open one don't download it. When it cannot be loaded
  (offline, or the page was left while it loaded), a toast says so and the
  panel closes; the next device that opens loads it again. A different
  device opens a fresh panel (its own tabs, data and polling).
-->
<script lang="ts">
	import type { Component } from 'svelte';
	import { closeDevice, devicePanel } from '#lib/state/devicePanel.svelte.ts';
	import { lazy, type Lazy } from '#lib/util/lazy.ts';

	let Panel = $state<Component<{ id: string }> | null>(null);
	// Made in the effect, which the server's build leaves out: an import there
	// would put the panel's stylesheets into every page's head
	let panel: Lazy<typeof import('./DevicePanel.svelte')> | null = null;

	$effect(() => {
		if (!devicePanel.id || Panel) return;
		panel ??= lazy(() => import('./DevicePanel.svelte'));
		void panel.open(closeDevice).then((m) => (Panel = m.default));
	});
</script>

{#if devicePanel.id && Panel}
	{#key devicePanel.id}
		<Panel id={devicePanel.id} />
	{/key}
{/if}
