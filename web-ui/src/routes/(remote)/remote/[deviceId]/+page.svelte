<!--
  The remote viewer (/remote/<device id>): one tab per device, the
  connection states in plain words, the controls row and the side sheets.
  Opens for a console session with device.connect (remote_target.connect
  for rt_ targets), a guest link (?guest= / ?t= or its cookie) or a mesh
  share (?mesh_share=); GET /api/remote/viewer/<id> decides and gives the
  page its data. Keeps ?transport=, ?panel=files, #add=<id>, the
  BroadcastChannel "neodesk-remote" and, in development with
  ND_DEV_MOCK_REMOTE=1, ?mock=1&scenario=. With ?popout=<display> it is a
  display's window of its own: the viewer that opened it draws into it and
  keeps the connection, so this page asks the server for nothing.

  A signed-in console user's session opens inside the console instead
  (/remote/session/<id>, console-sessions.svelte.ts); this page stays the
  viewer for guest links, mesh shares, RDP and VNC targets, the other
  transports, the desktop app's window and ?standalone=1 (Open in new tab,
  quick support).
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { ApiError } from '#lib/api/client.ts';
	import { getViewerData, type ViewerData } from '#lib/api/remote.ts';
	import { tf } from '#lib/i18n/i18n.svelte.ts';
	import { base, link } from '#lib/nav.ts';
	import { loadRdClient } from '#lib/remote/rdclient.ts';
	import { isDeviceId, isGuestMode, isRemoteTarget, transportOf } from '#lib/remote/access.ts';
	import { sessionPath } from '#lib/remote/console-sessions.svelte.ts';
	import DisplayWindow from '#lib/remote/viewer/DisplayWindow.svelte';
	import RemoteViewer from '#lib/remote/viewer/RemoteViewer.svelte';
	import Button from '#lib/ui/Button.svelte';

	type Problem = { title: string; message: string; home?: boolean };

	const deviceId = $derived(page.params.deviceId ?? '');
	/** A display's own window: the display's index, or -1 for the viewer */
	const popout = $derived.by(() => {
		const v = page.url.searchParams.get('popout');
		return v !== null && /^\d{1,2}$/.test(v) ? Number(v) : -1;
	});
	let data = $state.raw<ViewerData | null>(null);
	let problem = $state.raw<Problem | null>(null);
	let busy = $state(false);

	/** The console holds this session: a signed-in account on the RustDesk transport, no guest or share link */
	function insideConsole(d: ViewerData): boolean {
		const q = new URLSearchParams(location.search);
		if (q.get('standalone') === '1' || q.has('guest') || q.has('t') || q.has('mesh_share')) return false;
		const w = window as unknown as { __TAURI__?: unknown; __NEODESK_RDCLIENT_DESKTOP__?: boolean };
		if (w.__TAURI__ || w.__NEODESK_RDCLIENT_DESKTOP__) return false;
		const caps = d.capabilities;
		return !!d.user && !isGuestMode(caps) && !isRemoteTarget(caps) && transportOf(caps) === 'rd';
	}

	function signInUrl(): string {
		const here = location.pathname + location.search + location.hash;
		return `${base}/remote/login?return=${encodeURIComponent(here)}`;
	}

	async function load() {
		busy = true;
		problem = null;
		const id = deviceId;
		if (!isDeviceId(id)) {
			location.replace(link('/devices'));
			return;
		}
		try {
			const d = await getViewerData(id, location.search);
			if (insideConsole(d)) {
				// eslint-disable-next-line svelte/no-navigation-without-resolve -- link() adds the base
				void goto(link(sessionPath(id)) + location.search + location.hash, { replaceState: true });
				return;
			}
			await loadRdClient({
				deviceId: d.deviceId,
				capabilities: d.capabilities,
				guestToken: d.guestToken,
				csrfToken: d.csrfToken,
				mock: d.mockRemote
			});
			data = d;
		} catch (err) {
			if (err instanceof ApiError && err.status === 401) {
				location.assign(signInUrl());
				return;
			}
			if (err instanceof ApiError && err.status === 403 && err.code === 'guest_denied') {
				problem = {
					title: tf('guest_access.invalid_title', 'Invalid guest link'),
					message: err.message
				};
			} else if (err instanceof ApiError && err.status === 403) {
				problem = {
					title: tf('errors.forbidden_title', 'Access denied'),
					message: tf('errors.forbidden_message', 'You do not have permission to access this page.'),
					home: true
				};
			} else if (err instanceof ApiError && err.status === 404) {
				problem = {
					title: tf('errors.not_found_title', 'Page not found'),
					message: tf('errors.not_found_message', 'The page you are looking for does not exist.'),
					home: true
				};
			} else if (err instanceof ApiError && err.status === 400) {
				location.replace(link('/devices'));
			} else {
				problem = {
					title: tf('remote_viewer.title_failed', 'Could not connect'),
					message:
						err instanceof Error
							? err.message
							: tf(
									'errors.server_error_message',
									'The server could not finish this request. Try again in a moment.'
								)
				};
			}
		} finally {
			busy = false;
		}
	}

	onMount(() => {
		if (popout < 0) void load();
	});
</script>

<svelte:head>
	{#if popout < 0}<title>{tf('remote.title', 'Remote Desktop')} - {deviceId}</title>{/if}
</svelte:head>

{#if popout >= 0 && isDeviceId(deviceId)}
	<DisplayWindow {deviceId} display={popout} />
{:else if data}
	<RemoteViewer {data} />
{:else if problem}
	<main class="viewer-problem">
		<h1>{problem.title}</h1>
		<p>{problem.message}</p>
		<div class="actions">
			{#if problem.home}
				<Button variant="primary" href={link('/remote')}
					>{tf('remote_viewer.back_to_list', 'Back to device list')}</Button
				>
			{:else}
				<Button onclick={() => void load()} {busy}>{tf('errors.try_again', 'Try again')}</Button>
			{/if}
		</div>
	</main>
{:else}
	<div class="viewer-loading" aria-busy="true"></div>
{/if}

<style>
	.viewer-loading {
		height: 100vh;
		height: 100dvh;
		background: var(--bg);
	}
	.viewer-problem {
		display: flex;
		flex-direction: column;
		gap: var(--space-3);
		width: min(36rem, 100%);
		margin: 0 auto;
		padding: min(18vh, 160px) var(--space-4) var(--space-6);
	}
	.viewer-problem h1 {
		margin: 0;
		font-size: var(--text-xl);
	}
	.viewer-problem p {
		margin: 0;
		color: var(--text-muted);
	}
	.actions {
		display: flex;
		gap: var(--space-2);
	}
</style>
