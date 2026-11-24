<!--
  The open remote sessions on the console's other pages: one link per
  device with its state, back to the session page, and a close button.
  A middle click on a session closes it, as on a browser tab. The
  sessions stay connected while the person works on other pages.
-->
<script lang="ts">
	import { tf } from '#lib/i18n/i18n.svelte.ts';
	import { link } from '#lib/nav.ts';
	import Icon from '#lib/ui/Icon.svelte';
	import { consoleSessions, sessionPath } from '../console-sessions.svelte.ts';
	import { tabStatus } from '../states.ts';

	const cs = consoleSessions;
	const sessions = $derived(cs.viewer?.sessions ?? []);

	function onauxclick(e: MouseEvent, id: string) {
		if (e.button !== 1) return;
		e.preventDefault();
		cs.close(id);
	}
</script>

{#if sessions.length}
	<nav class="session-strip" aria-label={tf('remote_viewer.open_sessions', 'Open sessions')}>
		{#each sessions as s (s.deviceId)}
			{@const st = tabStatus(s.view)}
			<div class="strip-item">
				<a
					class="strip-link"
					href={link(sessionPath(s.deviceId))}
					title="{s.label}{s.label !== s.deviceId ? ` (${s.deviceId})` : ''}, {st.label}"
					onauxclick={(e) => onauxclick(e, s.deviceId)}
					onmousedown={(e) => {
						// A middle press would start the browser's scrolling
						if (e.button === 1) e.preventDefault();
					}}
				>
					<span class="tab-dot status is-{st.tone}" aria-hidden="true"></span>
					<span class="strip-label">{s.label}</span>
					<span class="sr-only">, {st.label}</span>
				</a>
				<button
					type="button"
					class="strip-close"
					aria-label={tf('remote_viewer.close_tab_named', 'Close {name}', { name: s.label })}
					title={tf('remote_viewer.close_tab_named', 'Close {name}', { name: s.label })}
					onclick={() => cs.close(s.deviceId)}><Icon name="close" /></button
				>
			</div>
		{/each}
	</nav>
{/if}

<style>
	.session-strip {
		display: flex;
		flex: none;
		gap: var(--space-1);
		min-height: calc(var(--control-h) + 2px);
		padding: 0 var(--page-pad);
		overflow-x: auto;
		border-bottom: 1px solid var(--rule);
		background: var(--bg-subtle);
		scrollbar-width: none;
	}
	.strip-item {
		display: flex;
		flex-shrink: 0;
		align-items: center;
		max-width: 240px;
		color: var(--text-muted);
	}
	.strip-item:hover {
		color: var(--text);
	}
	.strip-link {
		display: inline-flex;
		align-items: center;
		gap: var(--space-2);
		min-width: 0;
		height: var(--control-h);
		padding: 0 var(--space-1) 0 var(--space-2);
		border-radius: var(--control-radius);
		color: inherit;
		font-weight: var(--control-weight);
		text-decoration: none;
	}
	.strip-link:focus-visible {
		outline: var(--focus-ring);
		outline-offset: -2px;
	}
	.strip-label {
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.strip-close {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		width: 28px;
		height: 28px;
		padding: 0;
		border: 0;
		border-radius: var(--control-radius);
		background: transparent;
		color: var(--text-muted);
		cursor: pointer;
	}
	.strip-close :global(.material-icons) {
		font-size: 18px;
	}
	.strip-close:hover {
		background: var(--hover);
		color: var(--text);
	}
	.strip-close:focus-visible {
		outline: var(--focus-ring);
		outline-offset: -2px;
	}
	/* The state mark of the viewer's tabs (RemoteViewer.svelte) */
	.tab-dot {
		display: inline-flex;
		flex-shrink: 0;
	}
	.tab-dot::before {
		content: '';
		box-sizing: border-box;
		width: 8px;
		height: 8px;
		border: 1.5px solid var(--text-muted);
		border-radius: 50%;
		forced-color-adjust: none;
	}
	.tab-dot.is-good::before {
		border-color: var(--success);
		background: var(--success);
	}
	.tab-dot.is-waiting::before {
		width: 8px;
		height: 10px;
		border: 0;
		border-radius: 0;
		background: var(--accent);
		clip-path: polygon(0 0, 100% 0, 62% 50%, 100% 100%, 0 100%, 38% 50%);
	}
	.tab-dot.is-danger::before {
		border-color: var(--danger);
		border-radius: 1px;
		background: var(--danger);
	}
</style>
