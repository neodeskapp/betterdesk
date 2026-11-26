<!--
  One display of a remote session in a window of its own
  (/remote/<id>?popout=<display>). The viewer window that opened it keeps
  the connection and draws the display into this window's canvas
  (client.showDisplay with this window): no second connection, no video
  copied between windows. This page has the canvas, a bar with the
  display's name, its zoom and the way back, and tells the viewer by
  postMessage when it is ready, hidden or shown again, and closing. When
  the viewer window closes, this one closes too; opened without a viewer,
  it says so.

  Keys typed here go to the device, Tab too; F8 leaves the screen for the
  bar, Ctrl+Alt+Shift+Z changes the zoom.
-->
<script lang="ts">
	import { onMount, tick } from 'svelte';
	import { tf } from '#lib/i18n/i18n.svelte.ts';
	import type { DisplayZoom } from '#lib/remote/rdclient.ts';
	import { isReleaseKey } from '#lib/remote/screen-focus.ts';
	import type { DisplayWindowMessage } from '#lib/remote/viewer.svelte.ts';
	import Button from '#lib/ui/Button.svelte';

	let { deviceId, display }: { deviceId: string; display: number } = $props();

	const uid = $props.id();
	let label = $state('');
	let device = $state('');
	let zoom = $state<DisplayZoom>('fit');
	let attached = $state(false);
	let orphan = $state(false);
	let canvas = $state<HTMLCanvasElement | null>(null);
	let backBtn = $state<HTMLElement | null>(null);

	const title = $derived(label || tf('remote_viewer.display_n', 'Display {n}', { n: display + 1 }));

	/** The viewer window that opened this one, while it is open */
	function viewerWindow(): Window | null {
		try {
			const w = window.opener as Window | null;
			return w && !w.closed ? w : null;
		} catch {
			return null;
		}
	}

	function send(msg: DisplayWindowMessage): void {
		viewerWindow()?.postMessage(msg, location.origin);
	}

	function setZoom(z: DisplayZoom): void {
		zoom = z;
		send({ type: 'nd-display-zoom', zoom: z });
		canvas?.focus();
	}

	onMount(() => {
		if (!viewerWindow()) {
			orphan = true;
			return;
		}
		const onMessage = (e: MessageEvent<unknown>) => {
			if (e.origin !== location.origin || e.source !== viewerWindow()) return;
			const msg = e.data as DisplayWindowMessage;
			if (msg.type === 'nd-display-attached') {
				label = msg.label;
				device = msg.device;
				zoom = msg.zoom;
				attached = true;
				void tick().then(() => canvas?.focus());
			} else if (msg.type === 'nd-display-zoom') zoom = msg.zoom;
			else if (msg.type === 'nd-display-bye') window.close();
		};
		const onVisibility = () =>
			send({ type: 'nd-display-visible', visible: document.visibilityState !== 'hidden' });
		const onHide = () => send({ type: 'nd-display-closed' });
		const onKey = (e: KeyboardEvent) => {
			if (isReleaseKey(e)) {
				e.preventDefault();
				e.stopPropagation();
				backBtn?.focus();
			} else if (e.ctrlKey && e.altKey && e.shiftKey && e.code === 'KeyZ') {
				e.preventDefault();
				e.stopPropagation();
				setZoom(zoom === 'fit' ? '1:1' : 'fit');
			}
		};
		// The viewer window closed: this one goes with it
		const watch = setInterval(() => {
			if (viewerWindow()) return;
			clearInterval(watch);
			orphan = true;
			window.close();
		}, 1000);
		window.addEventListener('message', onMessage);
		document.addEventListener('visibilitychange', onVisibility);
		window.addEventListener('pagehide', onHide);
		window.addEventListener('keydown', onKey, true);
		send({ type: 'nd-display-ready', deviceId, display });
		return () => {
			clearInterval(watch);
			window.removeEventListener('message', onMessage);
			document.removeEventListener('visibilitychange', onVisibility);
			window.removeEventListener('pagehide', onHide);
			window.removeEventListener('keydown', onKey, true);
		};
	});
</script>

<svelte:head>
	<title>{device ? `${title} - ${device}` : title}</title>
</svelte:head>

<div class="display-window">
	<header class="bar">
		<h1 class="title">
			{title}{#if device}<span class="device">{device}</span>{/if}
		</h1>
		<div class="zoom" role="group" aria-label={tf('remote_viewer.zoom', 'Zoom')}>
			<Button size="sm" aria-pressed={zoom === 'fit'} disabled={!attached} onclick={() => setZoom('fit')}
				>{tf('remote.scale_fit', 'Fit to window')}</Button
			>
			<Button size="sm" aria-pressed={zoom === '1:1'} disabled={!attached} onclick={() => setZoom('1:1')}
				>{tf('remote.scale_original', 'Original size')}</Button
			>
		</div>
		<Button size="sm" icon="close" bind:element={backBtn} onclick={() => window.close()}
			>{tf('remote_viewer.back_to_main', 'Back to the main window')}</Button
		>
	</header>
	<div class="screen">
		<!-- svelte-ignore a11y_no_interactive_element_to_noninteractive_role (ARIA in HTML allows any role on canvas; role application makes screen readers pass the keys to the remote computer) -->
		<canvas
			id="nd-popout-canvas"
			bind:this={canvas}
			class="screen-canvas"
			tabindex="0"
			role="application"
			aria-roledescription={tf('remote_viewer.screen_role', 'remote screen')}
			aria-label={device
				? tf('remote_viewer.display_of', 'Display {n} of {name}', { n: display + 1, name: device })
				: title}
			aria-describedby="{uid}-keys"
		></canvas>
		<p id="{uid}-keys" class="sr-only">
			{tf(
				'remote_viewer.screen_keys',
				'Keys go to the device, Tab too. Press F8 or Ctrl+Alt+Shift+G to leave the screen.'
			)}
		</p>
		{#if orphan}
			<div class="orphan" role="status">
				<p>
					{tf(
						'remote_viewer.window_without_viewer',
						'This window shows a display of a remote session. Open it from the viewer with Open in a window of its own.'
					)}
				</p>
			</div>
		{/if}
	</div>
</div>

<style>
	.display-window {
		display: flex;
		flex-direction: column;
		height: 100vh;
		height: 100dvh;
		background: var(--bg);
	}
	.bar {
		display: flex;
		flex: none;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-2) var(--space-3);
		min-height: var(--viewer-bar-h, 44px);
		padding: var(--space-1) var(--space-3);
		border-bottom: 1px solid var(--rule);
		background: var(--bg-subtle);
	}
	.title {
		flex: 1;
		min-width: 0;
		margin: 0;
		overflow: hidden;
		font-size: var(--text-md);
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.device {
		margin-inline-start: var(--space-2);
		color: var(--text-muted);
		font-weight: 400;
	}
	.zoom {
		display: inline-flex;
		gap: var(--space-1);
	}
	.zoom :global([aria-pressed='true']) {
		border-color: var(--accent);
		background: var(--accent-tint);
	}
	/* The letterbox around the picture stays black, as in the viewer */
	.screen {
		position: relative;
		flex: 1;
		min-height: 0;
		overflow: hidden;
		background: #000000;
	}
	.screen-canvas {
		display: block;
		width: 100%;
		height: 100%;
		outline: none;
		cursor: crosshair;
	}
	.screen-canvas:focus-visible {
		outline: 2px solid var(--focus);
		outline-offset: -2px;
	}
	.orphan {
		position: absolute;
		inset: 0;
		display: flex;
		align-items: flex-start;
		justify-content: center;
		padding: min(18vh, 160px) var(--space-4);
		background: var(--bg);
		color: var(--text);
	}
	.orphan p {
		max-width: 36rem;
		margin: 0;
	}
</style>
