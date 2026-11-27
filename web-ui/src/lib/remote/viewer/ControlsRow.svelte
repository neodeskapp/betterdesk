<!--
  The session controls: a row under the session bar while Controls is
  pressed (or F8) or Keep open is on. Groups: keyboard and display (Actions,
  Type text, Display, screens), side sheets (Chat, File transfer, Session
  info), session (sound, recording, view only, work report), and Keep open
  and Disconnect at the end. Below 1360 px the words move into tooltips
  and accessible names.
-->
<script lang="ts">
	import { untrack } from 'svelte';
	import { tf } from '#lib/i18n/i18n.svelte.ts';
	import { FPS_MODES, KEYBOARD_MODES, QUALITIES, SCALES } from '#lib/remote/prefs.ts';
	import { SHEET_BUTTONS, type Viewer } from '#lib/remote/viewer.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import Icon from '#lib/ui/Icon.svelte';
	import MenuDivider from '#lib/ui/MenuDivider.svelte';
	import MenuItem from '#lib/ui/MenuItem.svelte';
	import { openPopup } from '#lib/ui/popups.svelte.ts';
	import DisplayMenu from './DisplayMenu.svelte';
	import MenuChoice from './MenuChoice.svelte';
	import { isMacPlatform } from './clipboard-keys.ts';
	import ToolMenu from './ToolMenu.svelte';

	let { viewer, id }: { viewer: Viewer; id: string } = $props();

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

	/* ── Type text: a small panel under its button ── */

	let typeOpen = $state(false);
	let typeText = $state('');
	let typeButton = $state<HTMLElement | null>(null);
	let typePanel = $state<HTMLElement | null>(null);
	let typeInput = $state<HTMLTextAreaElement | null>(null);

	function closeType(returnFocus: boolean) {
		if (!typeOpen) return;
		typeOpen = false;
		if (returnFocus) typeButton?.focus();
	}

	$effect(() => {
		if (!typeOpen) return;
		return untrack(() => {
			const release = openPopup(() => closeType(false));
			queueMicrotask(() => typeInput?.focus());
			const outside = (e: Event) => {
				const t = e.target as Node | null;
				if (t && (typePanel?.contains(t) || typeButton?.contains(t))) return;
				closeType(false);
			};
			document.addEventListener('pointerdown', outside, true);
			return () => {
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
	{#if hasPackage && packageText}
		<span class="package-badge" title={packageTitle}>{packageText}</span>
	{/if}
	<div class="controls-groups">
		<div class="group">
			{#if !watchOnly}
				<ToolMenu
					label={tf('remote.actions', 'Actions')}
					icon="keyboard"
					onopen={() => s && viewer.refreshMonitors(s)}
				>
					<MenuItem icon="keyboard" onselect={() => viewer.ctrlAltDel()}
						><span translate="no">Ctrl+Alt+Del</span></MenuItem
					>
					<MenuItem icon="lock" onselect={() => viewer.lockScreen()}
						>{tf('remote.lock_screen', 'Lock screen')}</MenuItem
					>
					{#if s?.signinOffer.kind === 'button'}
						<MenuItem icon="login" onselect={() => s && void viewer.signIn(s)}
							>{tf('remote.signin_menu', 'Sign in with the saved details')}</MenuItem
						>
					{/if}
					{#if s?.allowed.restart ?? true}
						<MenuItem icon="restart_alt" onselect={() => void viewer.restartRemote()}
							>{tf('remote.restart_remote', 'Restart remote device')}</MenuItem
						>
					{/if}
					<MenuDivider />
					<MenuItem icon="refresh" onselect={() => viewer.refreshScreen()}
						>{tf('remote.refresh_screen', 'Refresh screen')}</MenuItem
					>
					<MenuItem icon="content_paste" onselect={() => void viewer.pasteClipboard()}
						>{tf('remote.clipboard_paste', 'Paste clipboard')}</MenuItem
					>
					<MenuItem icon="settings_backup_restore" onselect={() => viewer.resetKeyboard()}
						>{tf('remote.reset_keyboard', 'Reset keyboard')}</MenuItem
					>
					<!-- Keys the browser keeps for itself (all of them in Firefox and Safari, which have no Keyboard Lock) -->
					<MenuItem icon="keyboard" onselect={() => viewer.sendSpecialKey('Meta')}
						>{tf('remote.send_windows_key', 'Windows key')}</MenuItem
					>
					<MenuItem icon="keyboard" onselect={() => viewer.sendSpecialKey('Alt+Tab')}
						><span translate="no">Alt+Tab</span></MenuItem
					>
					<MenuDivider />
					<MenuItem
						type="checkbox"
						checked={s?.toggles.blockInput ?? false}
						onselect={() => viewer.setToggle('blockInput', !(s?.toggles.blockInput ?? false))}
						>{tf('remote.block_input', 'Block user input')}</MenuItem
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

				<span class="tool-anchor">
					<Button
						size="sm"
						class="toolbar-btn"
						icon="text_fields"
						bind:element={typeButton}
						title={tf('remote.type_text', 'Type text')}
						aria-expanded={typeOpen}
						aria-controls={typeOpen ? `${uid}-type` : undefined}
						onclick={() => (typeOpen ? closeType(false) : (typeOpen = true))}
						><span class="tb-label">{tf('remote.type_text', 'Type text')}</span><Icon
							name="expand_more"
							class="btn-caret"
						/></Button
					>
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

			<ToolMenu label={tf('remote.display_settings', 'Display')} icon="fit_screen" menuClass="display-menu">
				<div class="display-grid">
					<div role="group" aria-labelledby="{uid}-q">
						<div class="menu-label" id="{uid}-q">{tf('remote.image_quality', 'Image quality')}</div>
						{#each QUALITIES as q (q)}
							<MenuChoice checked={s?.prefs.quality === q} onselect={() => viewer.setPref('quality', q)}
								>{qualityLabel(q)}</MenuChoice
							>
						{/each}
					</div>
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
					<div role="group" aria-labelledby="{uid}-scale">
						<div class="menu-label" id="{uid}-scale">{tf('remote.scale_mode', 'Scaling')}</div>
						{#each SCALES as m (m)}
							<MenuChoice checked={s?.prefs.scale === m} onselect={() => viewer.setPref('scale', m)}
								>{scaleLabel(m)}</MenuChoice
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
					<div role="group" aria-labelledby="{uid}-opt">
						<div class="menu-label" id="{uid}-opt">{tf('remote_viewer.display_options', 'Options')}</div>
						<MenuChoice
							kind="checkbox"
							checked={s?.toggles.showCursor ?? true}
							onselect={() => viewer.setToggle('showCursor', !(s?.toggles.showCursor ?? true))}
							>{tf('remote.show_cursor', 'Show remote cursor')}</MenuChoice
						>
						<MenuChoice
							kind="checkbox"
							checked={s?.toggles.lockAfter ?? false}
							onselect={() => viewer.setToggle('lockAfter', !(s?.toggles.lockAfter ?? false))}
							>{tf('remote.lock_after_session', 'Lock after session')}</MenuChoice
						>
						<MenuChoice
							kind="checkbox"
							checked={s?.toggles.privacy ?? false}
							onselect={() => viewer.setToggle('privacy', !(s?.toggles.privacy ?? false))}
							>{tf('remote.privacy_mode', 'Privacy mode')}</MenuChoice
						>
						<MenuChoice
							kind="checkbox"
							checked={s?.toggles.disableClipboard ?? false}
							onselect={() => viewer.setToggle('disableClipboard', !(s?.toggles.disableClipboard ?? false))}
							>{tf('remote.disable_clipboard', 'Disable clipboard')}</MenuChoice
						>
					</div>
				</div>
			</ToolMenu>

			{#if displaysMenu && s}
				<DisplayMenu {viewer} session={s} />
			{/if}
		</div>

		<div class="group">
			{#if !watchOnly}
				<Button
					size="sm"
					class="toolbar-btn"
					icon="chat"
					id={SHEET_BUTTONS.chat}
					title={tf('remote.chat', 'Chat')}
					aria-pressed={viewer.sheet === 'chat'}
					aria-controls="rd-sheet-host"
					onclick={() => viewer.toggleSheet('chat')}
					><span class="tb-label">{tf('remote.chat', 'Chat')}</span></Button
				>
				<Button
					size="sm"
					class="toolbar-btn"
					icon="folder_open"
					id={SHEET_BUTTONS.files}
					title={tf('remote.file_transfer', 'File transfer')}
					aria-pressed={viewer.sheet === 'files'}
					aria-controls="rd-sheet-host"
					disabled={!(s?.allowed.file ?? true)}
					onclick={() => viewer.toggleSheet('files')}
					><span class="tb-label">{tf('remote.file_transfer', 'File transfer')}</span></Button
				>
			{/if}
			<Button
				size="sm"
				class="toolbar-btn"
				icon="info"
				id={SHEET_BUTTONS.info}
				title={tf('remote_viewer.session_info', 'Session info')}
				aria-pressed={viewer.sheet === 'info'}
				aria-controls="rd-sheet-host"
				onclick={() => viewer.toggleSheet('info')}
				><span class="tb-label">{tf('remote_viewer.session_info', 'Session info')}</span></Button
			>
		</div>

		<div class="group">
			<Button
				size="sm"
				class="toolbar-btn"
				icon={s?.audioMuted ? 'volume_off' : 'volume_up'}
				title={s?.audioMuted
					? tf('remote_viewer.unmute', 'Turn sound on')
					: tf('remote_viewer.mute', 'Mute sound')}
				onclick={() => viewer.toggleAudio()}
				><span class="tb-label"
					>{s?.audioMuted
						? tf('remote_viewer.unmute', 'Turn sound on')
						: tf('remote_viewer.mute', 'Mute sound')}</span
				></Button
			>
			<Button
				size="sm"
				class={['toolbar-btn', (s?.recording || s?.meshServerRecord) && 'recording']
					.filter(Boolean)
					.join(' ')}
				icon={s?.recording || s?.meshServerRecord ? 'stop_circle' : 'fiber_manual_record'}
				title={s?.recording || s?.meshServerRecord
					? tf('remote_viewer.stop_recording', 'Stop recording')
					: tf('remote.record', 'Record')}
				onclick={() => viewer.toggleRecording()}
				><span class="tb-label"
					>{s?.recording || s?.meshServerRecord
						? tf('remote_viewer.stop_recording', 'Stop recording')
						: tf('remote.record', 'Record')}</span
				></Button
			>
			{#if !watchOnly}
				<Button
					size="sm"
					class="toolbar-btn"
					icon="visibility"
					title={tf('remote.view_only', 'View only')}
					aria-pressed={s?.viewOnly ?? false}
					disabled={!(s?.allowed.keyboard ?? true)}
					onclick={() => viewer.toggleViewOnly()}
					><span class="tb-label">{tf('remote.view_only', 'View only')}</span></Button
				>
			{/if}
			{#if hasPackage && viewer.data.workReports}
				<Button
					size="sm"
					class="toolbar-btn"
					icon="payments"
					title={tf('commercialization.report.toolbar', 'Work report')}
					onclick={() => viewer.markReportIntent()}
					><span class="tb-label">{tf('commercialization.report.toolbar', 'Work report')}</span></Button
				>
			{/if}
		</div>

		<div class="group group-end">
			<Button
				size="sm"
				class="toolbar-btn"
				title={tf('remote_viewer.keep_open_hint', 'Keep the controls open while you work')}
				aria-pressed={viewer.keepOpen}
				onclick={() => {
					viewer.keepOpen = !viewer.keepOpen;
					if (viewer.keepOpen) viewer.controlsOpen = true;
				}}>{tf('remote.pin_toolbar', 'Keep open')}</Button
			>
			<Button
				size="sm"
				variant="danger-quiet"
				class="toolbar-btn"
				icon="link_off"
				title={tf('remote.disconnect', 'Disconnect')}
				onclick={() => viewer.disconnect()}
				><span class="tb-label">{tf('remote.disconnect', 'Disconnect')}</span></Button
			>
		</div>
	</div>
</div>

<style>
	.controls {
		position: relative;
		z-index: 210;
		display: flex;
		flex: none;
		align-items: center;
		gap: var(--space-2);
		min-height: calc(var(--control-h-sm) + 2 * var(--space-1));
		padding: var(--space-1) var(--space-2);
		border-bottom: 1px solid var(--rule);
		background: var(--bg-subtle);
		color: var(--text);
	}
	.package-badge {
		color: var(--text-muted);
		font-size: var(--text-sm);
		white-space: nowrap;
	}
	.controls-groups {
		display: flex;
		flex: 1;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-1) var(--space-4);
		min-width: 0;
	}
	.group {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-1);
	}
	.group + .group {
		padding-inline-start: var(--space-4);
		border-inline-start: 1px solid var(--rule);
	}
	.group-end {
		margin-inline-start: auto;
	}
	.controls :global(.toolbar-btn) {
		gap: var(--space-1);
	}
	.controls :global(.toolbar-btn > .material-icons:not(.btn-caret)) {
		color: var(--text-muted);
	}
	.controls :global(.toolbar-btn:hover > .material-icons),
	.controls :global(.toolbar-btn[aria-pressed='true'] > .material-icons) {
		color: var(--text);
	}
	/* Pressed toggles, open sheets and open menus: the selected look of the control set */
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
	.tool-anchor {
		position: relative;
		display: inline-flex;
	}
	.tool-anchor :global(.btn-caret) {
		margin-inline-start: -2px;
		color: var(--text-muted);
		font-size: 18px;
	}
	.type-panel {
		position: absolute;
		top: calc(100% + var(--space-1));
		inset-inline-start: 0;
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
	/* Menu labels and the Display menu's grid */
	.menu-label {
		padding: var(--space-2) var(--space-3) var(--space-1);
		color: var(--text-muted);
		font-size: var(--text-xs);
		font-weight: 600;
		text-transform: uppercase;
		letter-spacing: 0.04em;
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
	/* Narrower windows: the row keeps its icons, the words move into the
	   tooltip and the accessible name (visually hidden, not removed) */
	@media (max-width: 1360px) {
		.controls :global(.tb-label) {
			position: absolute;
			width: 1px;
			height: 1px;
			overflow: hidden;
			clip: rect(0 0 0 0);
			white-space: nowrap;
		}
		.controls :global(.toolbar-btn:has(.tb-label)) {
			min-width: var(--control-h-sm);
			padding-inline: var(--space-1);
		}
		.group + .group {
			padding-inline-start: var(--space-2);
		}
		.controls-groups {
			column-gap: var(--space-2);
		}
	}
</style>
