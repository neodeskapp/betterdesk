<!--
  The clipboard's state in the session bar: on, off, needs the browser's
  permission, or content copied on the remote that waits to be taken. One
  click does what the state needs: asks the browser for the permission
  (Chromium), writes the waiting content (a click is a user gesture), or
  turns the clipboard on and off (Disable clipboard in the Display menu).
  Off for a reason the person can't change here (the device, view only)
  it only says why.
-->
<script lang="ts">
	import { tf } from '#lib/i18n/i18n.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import type { Viewer } from '../viewer.svelte.ts';

	let { viewer }: { viewer: Viewer } = $props();

	const state = $derived(viewer.clipboard.state());
	const off = $derived(viewer.clipboardOff());
	const fixed = $derived(off === 'device' || off === 'view_only' || off === 'not_streaming');

	const label = $derived(
		state === 'pending'
			? tf('remote.clipboard_take', 'Copy here')
			: state === 'permission'
				? tf('remote.clipboard_allow', 'Allow clipboard')
				: state === 'off'
					? tf('remote.clipboard_is_off', 'Clipboard off')
					: tf('remote.clipboard_is_on', 'Clipboard on')
	);
	const hint = $derived(
		state === 'pending'
			? tf(
					'remote.clipboard_pending_hint',
					'Something was copied on the remote computer. Click here, or press a key or click in the remote screen, to copy it to this computer.'
				)
			: state === 'permission'
				? tf(
						'remote.clipboard_permission_hint',
						'Copy and paste work. Allow clipboard access once, and what you copy here is on the remote computer as soon as you come back to the viewer.'
					)
				: off === 'device'
					? tf(
							'remote.clipboard_off_device',
							'The remote computer or this server has the clipboard turned off for this session.'
						)
					: off === 'view_only'
						? tf('remote.clipboard_off_view_only', 'View only: the clipboard is off.')
						: state === 'off'
							? tf('remote.clipboard_off_hint', 'The clipboard is off. Click to turn it on.')
							: tf(
									'remote.clipboard_on_hint',
									'Copy and paste work both ways: Ctrl+C and Ctrl+V in the remote screen, text and images. Click to turn the clipboard off.'
								)
	);

	function onclick() {
		if (state === 'pending') viewer.clipboard.gesture();
		else if (state === 'permission') void viewer.clipboard.requestPermission();
		else if (!fixed) viewer.setToggle('disableClipboard', state !== 'off');
		viewer.focusScreen();
	}
</script>

<Button
	icon="content_paste"
	class={['bar-btn', 'clip-btn', `is-${state}`].join(' ')}
	title={hint}
	aria-pressed={state === 'on' || state === 'pending' ? true : state === 'off' && !fixed ? false : undefined}
	aria-disabled={fixed ? 'true' : undefined}
	data-clipboard={state}
	{onclick}
	><span class="clip-dot" aria-hidden="true"></span><span class="bar-label">{label}</span><span
		class="sr-only">. {hint}</span
	></Button
>

<style>
	/* The state as a mark next to the glyph: the tab dots' tones (good, waiting, neutral) */
	.clip-dot {
		display: inline-block;
		flex: none;
		box-sizing: border-box;
		width: 8px;
		height: 8px;
		margin-inline-start: -4px;
		border: 1.5px solid var(--text-muted);
		border-radius: 50%;
		forced-color-adjust: none;
	}
	:global(.clip-btn.is-on) .clip-dot {
		border-color: var(--success);
		background: var(--success);
	}
	:global(.clip-btn.is-pending) .clip-dot,
	:global(.clip-btn.is-permission) .clip-dot {
		border-color: var(--accent);
		background: var(--accent);
	}
	:global(.clip-btn[aria-disabled='true']) {
		opacity: 0.7;
	}
</style>
