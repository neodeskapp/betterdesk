<!--
  A remote session inside the console (/remote/session/<device id>): the
  viewer the shell holds (ConsoleViewer.svelte) fills the main area while
  this page shows; this page opens the device's session there, or picks it
  when it is open, and says what went wrong when the server refuses.
  Sessions the console can't hold (guest links, RDP and VNC targets,
  another transport) go to the standalone viewer instead. Leaving the page
  keeps the sessions connected; the strip of open sessions leads back.
-->
<script lang="ts">
	import { onMount, untrack } from 'svelte';
	import { afterNavigate, goto } from '$app/navigation';
	import { page } from '$app/state';
	import { tf } from '#lib/i18n/i18n.svelte.ts';
	import { appPath, link } from '#lib/nav.ts';
	import { consoleSessions, standalonePath } from '#lib/remote/console-sessions.svelte.ts';
	import { setPageTitle } from '#lib/shell/page.svelte.ts';
	import { goLogin } from '#lib/shell/session-guard.svelte.ts';
	import Button from '#lib/ui/Button.svelte';

	const cs = consoleSessions;
	const deviceId = $derived(page.params.deviceId ?? '');
	const label = $derived(
		cs.viewer?.sessions.find((s) => s.deviceId === deviceId)?.label ?? cs.viewer?.active?.label ?? deviceId
	);

	async function openHere(id: string) {
		if (!/^[A-Za-z0-9_-]{1,64}$/.test(id)) {
			// eslint-disable-next-line svelte/no-navigation-without-resolve -- link() adds the base
			void goto(link('/devices'), { replaceState: true });
			return;
		}
		const result = await cs.open(id, location.search);
		if (result === 'standalone') location.replace(link(standalonePath(id, location.search)));
		else if (result === 'signin') goLogin();
	}

	$effect(() => {
		const id = deviceId;
		untrack(() => void openHere(id));
	});

	$effect(() =>
		setPageTitle(label ? tf('remote.session_title', 'Remote session: {name}', { name: label }) : null)
	);

	afterNavigate(({ from }) => {
		// Close on the last session goes back to the page the person came from
		if (from && !appPath(from.url.pathname).startsWith('/remote/session/'))
			cs.returnTo = appPath(from.url.pathname) + from.url.search;
	});

	onMount(() => {
		cs.navigate = (path) => {
			// eslint-disable-next-line svelte/no-navigation-without-resolve -- link() adds the base
			void goto(link(path));
		};
		cs.setShown(true);
		return () => cs.setShown(false);
	});
</script>

<h1 class="sr-only">{tf('remote.session_title', 'Remote session: {name}', { name: label })}</h1>

{#if cs.problem && cs.problem.deviceId === deviceId}
	<div class="session-problem" role="alert">
		<h2>{cs.problem.title}</h2>
		<p>{cs.problem.message}</p>
		<div class="actions">
			<Button onclick={() => void openHere(deviceId)}>{tf('errors.try_again', 'Try again')}</Button>
			<Button variant="ghost" href={link('/devices')}
				>{tf('remote.back_to_devices', 'Back to devices')}</Button
			>
		</div>
	</div>
{:else if cs.opening === deviceId && !cs.count}
	<div class="session-loading" aria-busy="true"></div>
{/if}

<style>
	.session-problem {
		display: flex;
		flex-direction: column;
		gap: var(--space-3);
		max-width: var(--measure);
		padding: var(--page-pad);
	}
	.session-problem h2 {
		margin: 0;
		font-size: var(--text-xl);
	}
	.session-problem p {
		margin: 0;
		color: var(--text-muted);
	}
	.actions {
		display: flex;
		gap: var(--space-2);
	}
	.session-loading {
		flex: 1;
		background: #000000;
	}
</style>
