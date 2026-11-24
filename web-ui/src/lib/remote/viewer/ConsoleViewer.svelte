<!--
  The console's remote viewer: the shell keeps it mounted while sessions
  are open, on every page. On the session page (/remote/session/<id>) it
  fills the main area; elsewhere it is hidden and parked, with every
  session still connected (console-sessions.svelte.ts). The address
  follows the tab picked in the viewer. When the shell goes (sign-out,
  another part of the app) the sessions end.
-->
<script lang="ts">
	import { onDestroy } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { appPath, link } from '#lib/nav.ts';
	import { consoleSessions, sessionPath } from '../console-sessions.svelte.ts';
	import RemoteViewer from './RemoteViewer.svelte';

	const cs = consoleSessions;

	$effect(() => {
		const id = cs.viewer?.activeId;
		if (!cs.shown || !id) return;
		const want = sessionPath(id);
		if (appPath(page.url.pathname) !== want)
			// eslint-disable-next-line svelte/no-navigation-without-resolve -- link() adds the base
			void goto(link(want), { replaceState: true });
	});

	onDestroy(() => cs.teardown());

	function openSession(id: string) {
		// eslint-disable-next-line svelte/no-navigation-without-resolve -- link() adds the base
		void goto(link(sessionPath(id)));
	}

	/** Closing the tab or leaving the console ends the sessions: the browser asks first */
	function onbeforeunload(e: BeforeUnloadEvent) {
		if (!cs.count) return;
		e.preventDefault();
		e.returnValue = '';
	}
</script>

<svelte:window {onbeforeunload} />

{#if cs.viewer && cs.data}
	<div class="console-viewer" hidden={!cs.shown}>
		<RemoteViewer
			data={cs.data}
			viewer={cs.viewer}
			embedded
			shown={cs.shown}
			sidebarCollapsed={cs.collapsed}
			ontogglesidebar={() => cs.setCollapsed(!cs.collapsed)}
			onopen={openSession}
			onopennewtab={(id) => cs.openInNewTab(id)}
			onfullscreen={(on) => (cs.immersive = on)}
		/>
	</div>
{/if}

<style>
	.console-viewer {
		display: flex;
		flex: 1;
		flex-direction: column;
		min-height: 0;
	}
	.console-viewer[hidden] {
		display: none;
	}
</style>
