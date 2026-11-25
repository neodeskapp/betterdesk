<!--
  Remote sessions (/remote/sessions), the sidebar's entry: the sessions
  open in this console, each one back to its page or closed, and a new one
  by ID or from the address book (connected, recent, folders), opened
  inside the console. The standalone /remote stays for guests and for a
  session moved out with Open in new tab.
-->
<script lang="ts">
	import { goto } from '$app/navigation';
	import { tf } from '#lib/i18n/i18n.svelte.ts';
	import { link } from '#lib/nav.ts';
	import AddressBook from '#lib/remote/AddressBook.svelte';
	import QuickConnect from '#lib/remote/QuickConnect.svelte';
	import { isPhone } from '#lib/remote/capabilities.ts';
	import { consoleSessions, sessionPath } from '#lib/remote/console-sessions.svelte.ts';
	import { tabStatus } from '#lib/remote/states.ts';
	import { can } from '#lib/state/session.svelte.ts';
	import IconButton from '#lib/ui/IconButton.svelte';
	import { appLink, type DesktopApp } from '#lib/util/app-links.ts';
	import { readPref, writePref } from '#lib/util/storage.ts';

	const VIEW_KEY = 'rd-desk-view';

	const cs = consoleSessions;
	const sessions = $derived(cs.viewer?.sessions ?? []);
	let view = $state<'list' | 'grid'>(readPref(VIEW_KEY) === 'grid' ? 'grid' : 'list');
	const phone = typeof window !== 'undefined' && isPhone();

	function open(id: string) {
		// eslint-disable-next-line svelte/no-navigation-without-resolve -- link() adds the base
		void goto(link(sessionPath(id)));
	}

	function openInApp(id: string, app: DesktopApp = 'rustdesk') {
		location.href = appLink(id, app);
	}
</script>

<div class="page page-wide remote-sessions">
	<div class="page-head">
		<h1>{tf('nav.remote', 'Remote sessions')}</h1>
		<p class="page-description">
			{tf('remote_dashboard.page_description', 'Connect to a device in this browser.')}
		</p>
	</div>

	{#if !can('device.connect')}
		<p>{tf('errors.forbidden_message', 'You do not have permission to access this page.')}</p>
	{:else}
		<section class="open-sessions" aria-labelledby="open-sessions-title">
			<h2 id="open-sessions-title">{tf('remote_viewer.open_sessions', 'Open sessions')}</h2>
			{#if sessions.length}
				<ul class="session-list">
					{#each sessions as s (s.deviceId)}
						{@const st = tabStatus(s.view)}
						<li class="session-row">
							<a class="session-link" href={link(sessionPath(s.deviceId))}>
								<span class="tab-dot status is-{st.tone}" aria-hidden="true"></span>
								<span class="session-name">{s.label}</span>
								{#if s.label !== s.deviceId}<span class="session-id mono">{s.deviceId}</span>{/if}
								<span class="session-state">{st.label}</span>
							</a>
							<IconButton
								icon="close"
								label={tf('remote_viewer.close_tab_named', 'Close {name}', { name: s.label })}
								onclick={() => cs.close(s.deviceId)}
							/>
						</li>
					{/each}
				</ul>
			{:else}
				<p class="empty">
					{tf('remote_viewer.no_open_sessions', 'No session is open. Connect by ID, or pick a device below.')}
				</p>
			{/if}
		</section>

		<section class="quick" aria-label={tf('remote_dashboard.quick_connect_title', 'Connect by ID')}>
			<QuickConnect
				{phone}
				hint={tf('remote_dashboard.quick_connect_hint', 'The ID shown in the RustDesk app on the device.')}
				onconnect={(id) => open(id)}
				onapp={openInApp}
			/>
		</section>

		<AddressBook
			bind:view
			{phone}
			connected={sessions.map((s) => s.deviceId)}
			onconnect={(id) => open(id)}
			onviewchange={(v) => writePref(VIEW_KEY, v)}
		/>
	{/if}
</div>

<style>
	.remote-sessions {
		display: flex;
		flex-direction: column;
		gap: var(--space-5);
	}
	.open-sessions h2 {
		margin: 0 0 var(--space-2);
		font-size: var(--text-md);
	}
	.session-list {
		display: flex;
		flex-direction: column;
		margin: 0;
		padding: 0;
		border: 1px solid var(--rule);
		border-radius: var(--radius);
		list-style: none;
	}
	.session-row {
		display: flex;
		align-items: center;
		gap: var(--space-2);
		padding: 0 var(--space-2) 0 0;
	}
	.session-row + .session-row {
		border-top: 1px solid var(--rule);
	}
	.session-link {
		display: flex;
		flex: 1;
		align-items: center;
		gap: var(--space-2);
		min-width: 0;
		min-height: var(--control-h);
		padding: var(--space-2) var(--space-3);
		color: var(--text);
		text-decoration: none;
	}
	.session-link:hover {
		background: var(--hover);
	}
	.session-link:focus-visible {
		outline: var(--focus-ring);
		outline-offset: -2px;
	}
	.session-name {
		min-width: 0;
		overflow: hidden;
		font-weight: 600;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.session-id,
	.session-state {
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	.session-state {
		margin-inline-start: auto;
	}
	.empty {
		margin: 0;
		color: var(--text-muted);
	}
	.quick {
		max-width: 36rem;
	}
	/* The state mark of the viewer's tabs */
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
