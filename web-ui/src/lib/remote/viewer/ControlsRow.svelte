<!--
  The session controls at the end of the session bar while a picture
  shows. From the start: what is switched on (recording, view only,
  blocked input, privacy mode, sound off, the clipboard when it is off or
  waits for a click), each as a pressed button that switches it off again;
  then Displays, Keys and View, Chat, File transfer and More, Full screen
  and Disconnect. Settings that are off most of the time live in the
  menus and show up at the start once they are on.

  Narrower viewers drop the menus' words first (tooltips and accessible
  names keep them), then the words of the switched-on settings and
  Disconnect; below 760 px Chat and File transfer move into More.
-->
<script lang="ts">
	import { untrack } from 'svelte';
	import { tf } from '#lib/i18n/i18n.svelte.ts';
	import { FPS_MODES, KEYBOARD_MODES, QUALITIES, SCALES } from '#lib/remote/prefs.ts';
	import { SHEET_BUTTONS, type Viewer } from '#lib/remote/viewer.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import MenuDivider from '#lib/ui/MenuDivider.svelte';
	import MenuItem from '#lib/ui/MenuItem.svelte';
	import { openPopup } from '#lib/ui/popups.svelte.ts';
	import ClipboardStatus from './ClipboardStatus.svelte';
	import DisplayMenu from './DisplayMenu.svelte';
	import MenuChoice from './MenuChoice.svelte';
	import { isMacPlatform } from './clipboard-keys.ts';
	import ToolMenu from './ToolMenu.svelte';

	interface Props {
		viewer: Viewer;
		id: string;
		/** Inside the console: move the session to a browser tab of its own */
		onopennewtab?: () => void;
		ontogglefullscreen?: () => void;
	}

	let { viewer, id, onopennewtab, ontogglefullscreen }: Props = $props();

	const uid = $props.id();
	/** This computer is a Mac: Cmd as Ctrl is offered */
	const mac = isMacPlatform();
	const s = $derived(viewer.active);
	const monitors = $derived(s?.monitors ?? []);
	const vd = $derived(s?.virtualDisplays ?? { supported: false });
	// One Displays menu for every device; it leaves out what the device can't do
	const displaysMenu = $derived(
		!!s?.together || monitors.length > 1 || (s?.displays.length ?? 0) > 1 || !!vd.supported
	);
	const winSessions = $derived(s?.windowsSessions ?? { sessions: [], currentSid: 0 });
	const check = $derived(viewer.billingCheck);
	const hasPackage = $derived(
		!!check &&
			check.allowed !== false &&
			!!(check.contract_id || check.package_name || check.remaining_minutes != null)
	);
	const packageText = $derived(
		check && hasPackage
			? [check.package_name, check.remaining_minutes != null ? `${check.remaining_minutes} min` : '']
					.filter(Boolean)
					.join(', ')
			: ''
	);
	const packageTitle = $derived(
		check
			? [
					check.org_name || check.company || '',
					check.device_name || s?.deviceId || '',
					check.package_name || '',
					check.remaining_minutes != null ? `${check.remaining_minutes} min` : ''
				]
					.filter(Boolean)
					.join('\n')
			: ''
	);
	// A watch-only guest link: no chat or files, and nothing that sends input to the device
	const watchOnly = $derived(viewer.viewOnlyGuest);
	const recording = $derived(!!(s?.recording || s?.meshServerRecord));
	const clipState = $derived(viewer.clipboard.state());
	const anyState = $derived(
		(hasPackage && !!packageText) ||
			recording ||
			(!watchOnly && !!s?.viewOnly) ||
			!!s?.toggles.blockInput ||
			!!s?.toggles.privacy ||
			!!s?.audioMuted ||
			clipState !== 'on'
	);

	/* ── Width: below 760 px Chat and File transfer move into More ── */

	let shellWidth = $state(Infinity);
	const narrow = $derived(shellWidth < 760);
	$effect(() => {
		const shell = document.getElementById('rd-viewer-shell');
		if (!shell || typeof ResizeObserver !== 'function') return;
		const ro = new ResizeObserver(([entry]) => {
			if (entry) shellWidth = entry.contentRect.width;
		});
		ro.observe(shell);
		return () => ro.disconnect();
	});

	/* ── View: the settings people seldom change wait behind Advanced settings ── */

	const ADVANCED_KEY = 'nd_viewer_view_advanced';
	let advanced = $state(readAdvanced());
	function readAdvanced(): boolean {
		try {
			return localStorage.getItem(ADVANCED_KEY) === '1';
		} catch {
			return false;
		}
	}
	function toggleAdvanced() {
		advanced = !advanced;
		try {
			localStorage.setItem(ADVANCED_KEY, advanced ? '1' : '0');
		} catch {
			/* storage blocked: closed again next time */
		}
	}

	const qualityLabel = (q: string) =>
		q === 'Best'
			? tf('remote.quality_best', 'Best')
			: q === 'Balanced'
				? tf('remote.quality_balanced', 'Balanced')
				: tf('remote.quality_low', 'Low');
	const scaleLabel = (m: string) =>
		m === 'fit'
			? tf('remote.scale_fit', 'Fit to window')
			: m === 'fill'
				? tf('remote.scale_fill', 'Fill window')
				: m === '1:1'
					? tf('remote.scale_original', 'Original size')
					: tf('remote.scale_stretch', 'Stretch');
	const keyboardLabel = (m: string) =>
		m === 'Auto'
			? tf('remote.keyboard_mode_auto', 'Auto')
			: m === 'Legacy'
				? tf('remote.keyboard_mode_legacy', 'Legacy')
				: tf('remote.keyboard_mode_map', 'Map');

	/** A switched-on setting was turned off from the bar: back to the screen */
	function switchOff(fn: () => void) {
		fn();
		viewer.focusScreen();
	}

	/* ── Type text: a small panel under Keys ── */

	let typeOpen = $state(false);
	let typeText = $state('');
	let keysAnchor = $state<HTMLElement | null>(null);
	let typePanel = $state<HTMLElement | null>(null);
	let typeInput = $state<HTMLTextAreaElement | null>(null);

	function closeType(returnFocus: boolean) {
		if (!typeOpen) return;
		typeOpen = false;
		if (returnFocus) keysAnchor?.querySelector<HTMLElement>('button')?.focus();
	}

	$effect(() => {
		if (!typeOpen) return;
		return untrack(() => {
			const release = openPopup(() => closeType(false));
			// After the menu has closed and given focus back to Keys
			const focusTimer = setTimeout(() => typeInput?.focus(), 0);
			const outside = (e: Event) => {
				const t = e.target as Node | null;
				if (t && (typePanel?.contains(t) || keysAnchor?.contains(t))) return;
				closeType(false);
			};
			document.addEventListener('pointerdown', outside, true);
			return () => {
				clearTimeout(focusTimer);
				document.removeEventListener('pointerdown', outside, true);
				release();
			};
		});
	});

	function sendTyped() {
		if (viewer.typeText(typeText)) {
			typeText = '';
			closeType(false);
			viewer.focusScreen();
		}
	}
</script>

<div class="controls" {id} role="group" aria-label={tf('remote_viewer.session_controls', 'Session controls')}>
	{#if anyState}
		<div class="group state">
			{#if hasPackage && packageText}
				<span class="package-badge" title={packageTitle}>{packageText}</span>
			{/if}
			{#if recording}
				<Button
					class="toolbar-btn recording"
					icon="stop_circle"
					title={tf('remote_viewer.stop_recording', 'Stop recording')}
					onclick={() => switchOff(() => viewer.toggleRecording())}
					><span class="st-label">{tf('remote_viewer.recording', 'Recording')}</span></Button
				>
			{/if}
			{#if !watchOnly && s?.viewOnly}
				<Button
					class="toolbar-btn"
					icon="visibility"
					title={tf('remote.view_only', 'View only')}
					aria-pressed="true"
					disabled={!(s?.allowed.keyboard ?? true)}
					onclick={() => switchOff(() => viewer.toggleViewOnly())}
					><span class="st-label">{tf('remote.view_only', 'View only')}</span></Button
				>
			{/if}
			{#if s?.toggles.blockInput}
				<Button
					class="toolbar-btn"
					icon="block"
					title={tf('remote_viewer.input_blocked', 'Input blocked')}
					aria-pressed="true"
					onclick={() => switchOff(() => viewer.setToggle('blockInput', false))}
					><span class="st-label">{tf('remote_viewer.input_blocked', 'Input blocked')}</span></Button
				>
			{/if}
			{#if s?.toggles.privacy}
				<Button
					class="toolbar-btn"
					icon="visibility_off"
					title={tf('remote.privacy_mode', 'Privacy mode')}
					aria-pressed="true"
					onclick={() => switchOff(() => viewer.setToggle('privacy', false))}
					><span class="st-label">{tf('remote.privacy_mode', 'Privacy mode')}</span></Button
				>
			{/if}
			{#if s?.audioMuted}
				<Button
					class="toolbar-btn"
					icon="volume_off"
					title={tf('remote_viewer.unmute', 'Turn sound on')}
					aria-pressed="true"
					onclick={() => switchOff(() => viewer.toggleAudio())}
					><span class="st-label">{tf('remote_viewer.sound_off', 'Sound off')}</span></Button
				>
			{/if}
			{#if clipState !== 'on'}
				<ClipboardStatus {viewer} />
			{/if}
		</div>
	{/if}

	{#if displaysMenu && s}
		<div class="group">
			<DisplayMenu {viewer} session={s} />
		</div>
	{/if}

	<div class="group">
		{#if !watchOnly}
			<span class="tool-anchor" bind:this={keysAnchor}>
				<ToolMenu
					label={tf('remote_viewer.keys', 'Keys')}
					icon="keyboard"
					onopen={() => s && viewer.refreshMonitors(s)}
				>
					<MenuItem icon="keyboard" onselect={() => viewer.ctrlAltDel()}
						><span translate="no">Ctrl+Alt+Del</span></MenuItem
					>
					<!-- Keys the browser keeps for itself (all of them in Firefox and Safari, which have no Keyboard Lock) -->
					<MenuItem icon="keyboard" onselect={() => viewer.sendSpecialKey('Meta')}
						>{tf('remote.send_windows_key', 'Windows key')}</MenuItem
					>
					<MenuItem icon="keyboard" onselect={() => viewer.sendSpecialKey('Alt+Tab')}
						><span translate="no">Alt+Tab</span></MenuItem
					>
					<MenuItem icon="lock" onselect={() => viewer.lockScreen()}
						>{tf('remote.lock_screen', 'Lock screen')}</MenuItem
					>
					<MenuDivider />
					<MenuItem icon="text_fields" haspopup="dialog" onselect={() => (typeOpen = true)}
						>{tf('remote.type_text', 'Type text')}</MenuItem
					>
					<MenuItem icon="content_paste" onselect={() => void viewer.pasteClipboard()}
						>{tf('remote.clipboard_paste', 'Paste clipboard')}</MenuItem
					>
					{#if s?.signinOffer.kind === 'button'}
						<MenuItem icon="login" onselect={() => s && void viewer.signIn(s)}
							>{tf('remote.signin_menu', 'Sign in with the saved details')}</MenuItem
						>
					{/if}
					<MenuDivider />
					<MenuItem
						type="checkbox"
						checked={s?.toggles.blockInput ?? false}
						onselect={() => viewer.setToggle('blockInput', !(s?.toggles.blockInput ?? false))}
						>{tf('remote.block_input', 'Block user input')}</MenuItem
					>
					<MenuItem icon="settings_backup_restore" onselect={() => viewer.resetKeyboard()}
						>{tf('remote.reset_keyboard', 'Reset keyboard')}</MenuItem
					>
					{#if winSessions.sessions.length >= 2}
						<MenuDivider />
						<div role="group" aria-labelledby="{uid}-winsess">
							<div class="menu-label" id="{uid}-winsess">
								{tf('remote.windows_sessions', 'Windows sessions')}
							</div>
							{#each winSessions.sessions as ws (ws.sid)}
								<MenuItem
									type="radio"
									checked={ws.sid === winSessions.currentSid}
									onselect={() => viewer.selectWindowsSession(ws.sid)}>{ws.name}</MenuItem
								>
							{/each}
						</div>
					{/if}
				</ToolMenu>
				{#if typeOpen}
					<div
						bind:this={typePanel}
						class="type-panel"
						id="{uid}-type"
						role="dialog"
						aria-labelledby="{uid}-type-label"
						tabindex="-1"
						onkeydown={(e) => {
							if (e.key === 'Escape') {
								e.preventDefault();
								e.stopPropagation();
								closeType(true);
							}
						}}
					>
						<label class="field-label" id="{uid}-type-label" for="{uid}-type-input"
							>{tf('remote.type_text', 'Type text')}</label
						>
						<textarea
							bind:this={typeInput}
							id="{uid}-type-input"
							class="type-input"
							rows="3"
							autocomplete="off"
							spellcheck="false"
							aria-describedby="{uid}-type-hint"
							bind:value={typeText}
							onkeydown={(e) => {
								if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
									e.preventDefault();
									sendTyped();
								}
							}}></textarea>
						<p class="form-hint" id="{uid}-type-hint">
							{tf('remote.type_text_placeholder', 'Text is typed as keystrokes on the remote device.')}
						</p>
						<Button size="sm" class="type-send" onclick={sendTyped}
							>{tf('remote.type_text_send', 'Send')}</Button
						>
					</div>
				{/if}
			</span>
		{/if}

		<ToolMenu label={tf('remote_viewer.view', 'View')} icon="tune" menuClass="display-menu">
			<div class="display-grid">
				<div role="group" aria-labelledby="{uid}-q">
					<div class="menu-label" id="{uid}-q">{tf('remote.image_quality', 'Image quality')}</div>
					{#each QUALITIES as q (q)}
						<MenuChoice checked={s?.prefs.quality === q} onselect={() => viewer.setPref('quality', q)}
							>{qualityLabel(q)}</MenuChoice
						>
					{/each}
				</div>
				<div role="group" aria-labelledby="{uid}-scale">
					<div class="menu-label" id="{uid}-scale">{tf('remote.scale_mode', 'Scaling')}</div>
					{#each SCALES as m (m)}
						<MenuChoice checked={s?.prefs.scale === m} onselect={() => viewer.setPref('scale', m)}
							>{scaleLabel(m)}</MenuChoice
						>
					{/each}
				</div>
				<div role="group" aria-labelledby="{uid}-opt">
					<div class="menu-label" id="{uid}-opt">{tf('remote_viewer.display_options', 'Options')}</div>
					<MenuChoice
						kind="checkbox"
						checked={s?.toggles.showCursor ?? true}
						onselect={() => viewer.setToggle('showCursor', !(s?.toggles.showCursor ?? true))}
						>{tf('remote.show_cursor', 'Show remote cursor')}</MenuChoice
					>
					<MenuChoice kind="checkbox" checked={s?.audioMuted ?? false} onselect={() => viewer.toggleAudio()}
						>{tf('remote_viewer.mute', 'Mute sound')}</MenuChoice
					>
					<MenuChoice kind="checkbox" checked={advanced} onselect={toggleAdvanced}
						>{tf('remote_viewer.advanced_settings', 'Advanced settings')}</MenuChoice
					>
				</div>
				{#if advanced}
					<div role="group" aria-labelledby="{uid}-fps">
						<div class="menu-label" id="{uid}-fps">{tf('remote_viewer.frame_rate', 'Frame rate')}</div>
						{#each FPS_MODES as m (m)}
							<MenuChoice
								checked={(s?.prefs.fpsMode ?? 'adaptive') === m}
								onselect={() => viewer.setPref('fpsMode', m)}
								>{#if m === 'adaptive'}{tf('remote_viewer.fps_adaptive', 'Adaptive')}{:else}<span
										translate="no">{m} fps</span
									>{/if}</MenuChoice
							>
						{/each}
					</div>
					<div role="group" aria-labelledby="{uid}-kb">
						<div class="menu-label" id="{uid}-kb">{tf('remote.keyboard_mode', 'Keyboard mode')}</div>
						{#each KEYBOARD_MODES as m (m)}
							<MenuChoice
								checked={(s?.prefs.keyboardMode ?? 'Auto') === m}
								onselect={() => viewer.setPref('keyboardMode', m)}>{keyboardLabel(m)}</MenuChoice
							>
						{/each}
						{#if mac}
							<MenuChoice
								kind="checkbox"
								checked={s?.prefs.cmdAsCtrl ?? true}
								onselect={() => viewer.setPref('cmdAsCtrl', !(s?.prefs.cmdAsCtrl ?? true))}
								>{tf('remote.cmd_as_ctrl', 'Send Cmd as Ctrl for common shortcuts')}</MenuChoice
							>
						{/if}
					</div>
					<div role="group" aria-labelledby="{uid}-codec">
						<div class="menu-label" id="{uid}-codec">{tf('remote.codec', 'Codec')}</div>
						{#each viewer.codecChoices as c (c)}
							<MenuChoice checked={s?.prefs.codec === c} onselect={() => viewer.setPref('codec', c)}
								>{#if c === 'Auto'}{tf('remote.codec_auto', 'Auto')}{:else}<span translate="no">{c}</span
									>{/if}</MenuChoice
							>
						{/each}
					</div>
				{/if}
			</div>
		</ToolMenu>
	</div>

	<div class="group">
		{#if !watchOnly && !narrow}
			<Button
				class="toolbar-btn is-icon"
				icon="chat"
				id={SHEET_BUTTONS.chat}
				title={tf('remote.chat', 'Chat')}
				aria-pressed={viewer.sheet === 'chat'}
				aria-controls="rd-sheet-host"
				onclick={() => viewer.toggleSheet('chat')}
				><span class="sr-only">{tf('remote.chat', 'Chat')}</span></Button
			>
			<Button
				class="toolbar-btn is-icon"
				icon="folder_open"
				id={SHEET_BUTTONS.files}
				title={tf('remote.file_transfer', 'File transfer')}
				aria-pressed={viewer.sheet === 'files'}
				aria-controls="rd-sheet-host"
				disabled={!(s?.allowed.file ?? true)}
				onclick={() => viewer.toggleSheet('files')}
				><span class="sr-only">{tf('remote.file_transfer', 'File transfer')}</span></Button
			>
		{/if}
		<!-- More carries the Session info sheet's button id, so closing the sheet focuses it -->
		<ToolMenu
			label={tf('remote_viewer.more', 'More')}
			icon="more_horiz"
			iconOnly
			buttonId={SHEET_BUTTONS.info}
		>
			{#if !watchOnly && narrow}
				<MenuItem icon="chat" onselect={() => viewer.toggleSheet('chat')}
					>{tf('remote.chat', 'Chat')}</MenuItem
				>
				<MenuItem
					icon="folder_open"
					disabled={!(s?.allowed.file ?? true)}
					onselect={() => viewer.toggleSheet('files')}>{tf('remote.file_transfer', 'File transfer')}</MenuItem
				>
				<MenuDivider />
			{/if}
			<MenuItem type="checkbox" checked={recording} onselect={() => viewer.toggleRecording()}
				>{tf('remote_viewer.record_session', 'Record the session')}</MenuItem
			>
			{#if !watchOnly}
				<MenuItem
					type="checkbox"
					checked={s?.viewOnly ?? false}
					disabled={!(s?.allowed.keyboard ?? true)}
					onselect={() => viewer.toggleViewOnly()}>{tf('remote.view_only', 'View only')}</MenuItem
				>
			{/if}
			<MenuItem
				type="checkbox"
				checked={s?.toggles.privacy ?? false}
				onselect={() => viewer.setToggle('privacy', !(s?.toggles.privacy ?? false))}
				>{tf('remote.privacy_mode', 'Privacy mode')}</MenuItem
			>
			<MenuItem
				type="checkbox"
				checked={s?.toggles.disableClipboard ?? false}
				onselect={() => viewer.setToggle('disableClipboard', !(s?.toggles.disableClipboard ?? false))}
				>{tf('remote.disable_clipboard', 'Disable clipboard')}</MenuItem
			>
			<MenuItem
				type="checkbox"
				checked={s?.toggles.lockAfter ?? false}
				onselect={() => viewer.setToggle('lockAfter', !(s?.toggles.lockAfter ?? false))}
				>{tf('remote.lock_after_session', 'Lock after session')}</MenuItem
			>
			<MenuDivider />
			<MenuItem icon="info" onselect={() => viewer.toggleSheet('info')}
				>{tf('remote_viewer.session_info', 'Session info')}</MenuItem
			>
			{#if hasPackage && viewer.data.workReports}
				<MenuItem icon="payments" onselect={() => viewer.markReportIntent()}
					>{tf('commercialization.report.toolbar', 'Work report')}</MenuItem
				>
			{/if}
			<MenuItem icon="refresh" onselect={() => viewer.refreshScreen()}
				>{tf('remote.refresh_screen', 'Refresh screen')}</MenuItem
			>
			{#if onopennewtab}
				<MenuItem icon="open_in_new" onselect={onopennewtab}
					>{tf('remote_viewer.open_in_new_tab', 'Open in new tab')}</MenuItem
				>
			{/if}
			{#if !watchOnly && (s?.allowed.restart ?? true)}
				<MenuDivider />
				<MenuItem icon="restart_alt" danger onselect={() => void viewer.restartRemote()}
					>{tf('remote.restart_remote', 'Restart remote device')}</MenuItem
				>
			{/if}
		</ToolMenu>
	</div>

	<div class="group">
		{#if ontogglefullscreen}
			{@const fsLabel = viewer.fullscreen
				? tf('remote_viewer.exit_fullscreen', 'Exit full screen')
				: tf('remote.fullscreen', 'Full screen')}
			<Button
				class="toolbar-btn is-icon"
				icon={viewer.fullscreen ? 'fullscreen_exit' : 'fullscreen'}
				title={`${fsLabel} (F11)`}
				onclick={ontogglefullscreen}><span class="sr-only">{fsLabel}</span></Button
			>
		{/if}
		<Button
			variant="danger-quiet"
			class="toolbar-btn"
			icon="link_off"
			title={tf('remote.disconnect', 'Disconnect')}
			onclick={() => viewer.disconnect()}
			><span class="st-label">{tf('remote.disconnect', 'Disconnect')}</span></Button
		>
	</div>
</div>

<style>
	.controls {
		display: flex;
		flex: none;
		align-items: center;
		gap: var(--space-2);
		min-width: 0;
		color: var(--text);
	}
	.group {
		display: flex;
		align-items: center;
		gap: var(--space-1);
	}
	.group + .group {
		padding-inline-start: var(--space-2);
		border-inline-start: 1px solid var(--rule);
	}
	.package-badge {
		padding-inline-end: var(--space-1);
		color: var(--text-muted);
		font-size: var(--text-sm);
		white-space: nowrap;
	}
	.controls :global(.toolbar-btn) {
		flex-shrink: 0;
		gap: var(--space-1);
	}
	.controls :global(.toolbar-btn.is-icon) {
		width: var(--control-h);
		padding: 0;
	}
	.controls :global(.toolbar-btn > .material-icons:not(.btn-caret)) {
		color: var(--text-muted);
	}
	.controls :global(.toolbar-btn:hover > .material-icons),
	.controls :global(.toolbar-btn[aria-pressed='true'] > .material-icons) {
		color: var(--text);
	}
	/* Switched-on settings, open sheets and open menus: the selected look of the control set */
	.controls :global(.toolbar-btn[aria-pressed='true']),
	.controls :global(.toolbar-btn[aria-expanded='true']) {
		border-color: var(--accent);
		background: var(--accent-tint);
		color: var(--text);
	}
	.controls :global(.toolbar-btn.recording),
	.controls :global(.toolbar-btn.recording > .material-icons) {
		border-color: var(--danger);
		color: var(--danger);
	}
	.controls :global(.btn-danger-quiet > .material-icons) {
		color: var(--danger);
	}
	.tool-anchor {
		position: relative;
		display: inline-flex;
	}
	.type-panel {
		position: absolute;
		top: calc(100% + var(--space-1));
		inset-inline-end: 0;
		z-index: var(--layer-menu);
		display: flex;
		flex-direction: column;
		gap: var(--space-2);
		width: min(360px, calc(100vw - 2 * var(--space-4)));
		padding: var(--space-3);
		border: 1px solid var(--rule);
		border-radius: var(--radius);
		background: var(--bg-float);
		box-shadow: var(--shadow-float);
	}
	.field-label {
		font-weight: 600;
	}
	.type-input {
		box-sizing: border-box;
		width: 100%;
		min-height: 72px;
		padding: var(--space-2) var(--space-3);
		border: 1px solid var(--border);
		border-radius: var(--control-radius);
		background: var(--bg);
		color: var(--text);
		font: inherit;
		resize: vertical;
	}
	.type-input:focus-visible {
		outline: var(--focus-ring);
		outline-offset: 1px;
	}
	.form-hint {
		margin: 0;
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	.type-panel :global(.type-send) {
		align-self: flex-end;
	}
	/* Group labels in the menus, in sentence case */
	.menu-label {
		padding: var(--space-2) var(--space-3) var(--space-1);
		color: var(--text-muted);
		font-size: var(--text-xs);
		font-weight: 600;
	}
	:global(.display-menu) {
		width: min(600px, calc(100vw - 2 * var(--space-4)));
	}
	.display-grid {
		display: grid;
		grid-template-columns: repeat(3, minmax(0, 1fr));
		gap: var(--space-1) var(--space-2);
	}
	@media (max-width: 720px) {
		.display-grid {
			grid-template-columns: repeat(2, minmax(0, 1fr));
		}
	}
	/* Narrower viewers: the words move into the tooltip and the accessible
	   name (visually hidden, not removed); the menus' words go first */
	@container viewer (max-width: 1240px) {
		.controls :global(.tb-label) {
			position: absolute;
			width: 1px;
			height: 1px;
			overflow: hidden;
			clip: rect(0 0 0 0);
			white-space: nowrap;
		}
		.controls :global(.toolbar-btn:has(.tb-label)) {
			min-width: var(--control-h);
			padding-inline: var(--space-2);
		}
	}
	@container viewer (max-width: 1000px) {
		.st-label {
			position: absolute;
			width: 1px;
			height: 1px;
			overflow: hidden;
			clip: rect(0 0 0 0);
			white-space: nowrap;
		}
		.controls :global(.toolbar-btn:has(.st-label)) {
			width: var(--control-h);
			padding: 0;
		}
		.package-badge {
			display: none;
		}
	}
</style>
