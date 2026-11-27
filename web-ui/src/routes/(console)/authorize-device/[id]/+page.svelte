<!--
  /authorize-device/<request id>: Sign in with NeoDesk. The desktop app
  opens this page in the browser; someone not signed in to the console
  signs in first (the server sends them to /login and back here). The
  page shows which computer asks (its name, system, NeoDesk ID, the
  address the request came from and how long ago) before Allow and Deny,
  so a link someone else sent can't sign their computer in unnoticed.
  Allow signs the computer in as this account; the app picks the sign-in
  up within seconds. A request lives five minutes and is answered once.

  Data: GET /api/device-login/<id>, POST .../approve and .../deny
  (lib/api/deviceLogin.ts).

  Screen readers: when the step changes, focus goes to the heading, so
  the new step is read from its start.
-->
<script lang="ts">
	import { onMount, tick, untrack } from 'svelte';
	import { page } from '$app/state';
	import { ApiError } from '#lib/api/client.ts';
	import { decideDeviceLogin, getDeviceLogin, type DeviceLogin } from '#lib/api/deviceLogin.ts';
	import { has, t } from '#lib/i18n/i18n.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import Facts from '#lib/ui/Facts.svelte';
	import FormStatus from '#lib/ui/FormStatus.svelte';
	import Notice from '#lib/ui/Notice.svelte';
	import States from '#lib/ui/States.svelte';
	import Time from '#lib/ui/Time.svelte';

	type Phase = 'loading' | 'failed' | 'pending' | 'allowed' | 'declined' | 'ended';

	const id = $derived(page.params.id ?? '');
	let phase = $state<Phase>('loading');
	let request = $state.raw<DeviceLogin | null>(null);
	let busy = $state<'allow' | 'deny' | null>(null);
	let error = $state('');
	let heading = $state<HTMLElement | null>(null);

	/* A new step: focus on its heading, so screen readers read it from the start */
	$effect(() => {
		const p = phase;
		if (p === 'loading' || p === 'pending') return;
		untrack(() => void tick().then(() => heading?.focus()));
	});

	/* The request ends five minutes after it started, also while the page is open */
	$effect(() => {
		if (phase !== 'pending' || !request?.expires_at) return;
		const left = Date.parse(request.expires_at) - Date.now();
		if (!Number.isFinite(left)) return;
		const timer = setTimeout(() => (phase = 'ended'), Math.max(0, left));
		return () => clearTimeout(timer);
	});

	async function load() {
		phase = 'loading';
		try {
			request = await getDeviceLogin(id);
			phase = request.state === 'pending' ? 'pending' : 'ended';
		} catch {
			phase = 'failed';
		}
	}

	onMount(() => void load());

	/** The sentence for a refused Allow or Deny, from its code */
	function refusal(err: unknown): string {
		const code = err instanceof ApiError ? err.code : null;
		if (code && has(`device_authorize.errors.${code}`)) return t(`device_authorize.errors.${code}`);
		return t('device_authorize.errors.server_error');
	}

	async function decide(allow: boolean) {
		busy = allow ? 'allow' : 'deny';
		error = '';
		try {
			request = await decideDeviceLogin(id, allow);
			phase = allow ? 'allowed' : 'declined';
		} catch (err) {
			if (err instanceof ApiError && (err.status === 404 || err.code === 'already_decided')) phase = 'ended';
			else error = refusal(err);
		} finally {
			busy = null;
		}
	}

	const device = $derived(request?.device_name || t('device_authorize.unnamed'));
	const title = $derived(
		phase === 'allowed'
			? t('device_authorize.allowed_title')
			: phase === 'declined'
				? t('device_authorize.declined_title')
				: phase === 'ended'
					? t('device_authorize.ended_title')
					: t('device_authorize.title')
	);
</script>

<svelte:head>
	<title>{t('device_authorize.title')}</title>
</svelte:head>

<div class="page authorize-page">
	<div class="page-head">
		<h1 bind:this={heading} tabindex="-1">{title}</h1>
	</div>

	{#if phase === 'loading'}
		<States kind="loading" />
	{:else if phase === 'failed'}
		<States kind="error" message={t('device_authorize.load_failed')} onretry={() => void load()} />
	{:else if phase === 'ended'}
		<Notice tone="warning" message={t('device_authorize.ended_text')} />
	{:else if phase === 'declined'}
		<Notice message={t('device_authorize.declined_text')} />
	{:else if phase === 'allowed' && request}
		<Notice tone="ok" message={t('device_authorize.allowed_text', { device, account: request.account })} />
	{:else if phase === 'pending' && request}
		<p class="authorize-ask">{t('device_authorize.description', { account: request.account })}</p>
		{#snippet asked()}<Time value={request?.created_at} />{/snippet}
		<Facts
			label={t('device_authorize.facts_label')}
			items={[
				{ label: t('device_authorize.device_name'), value: device },
				{ label: t('device_authorize.device_os'), value: request.device_os },
				{ label: t('device_authorize.device_id'), value: request.device_id },
				{ label: t('device_authorize.ip'), value: request.ip },
				{ label: t('device_authorize.asked'), value: asked }
			]}
		/>
		<Notice tone="warning" message={t('device_authorize.check')} />
		<div class="authorize-actions">
			<Button
				variant="primary"
				busy={busy === 'allow'}
				disabled={busy !== null}
				onclick={() => void decide(true)}>{t('device_authorize.allow')}</Button
			>
			<Button busy={busy === 'deny'} disabled={busy !== null} onclick={() => void decide(false)}
				>{t('device_authorize.deny')}</Button
			>
		</div>
		<FormStatus message={error} tone="error" />
	{/if}
</div>

<style>
	.authorize-page {
		display: flex;
		flex-direction: column;
		gap: var(--space-4);
		max-width: var(--measure);
	}
	.authorize-page .page-head h1 {
		margin: 0;
	}
	/* Focus lands here on a new step to read it, not to act on it */
	.authorize-page h1:focus {
		outline: none;
	}
	.authorize-ask {
		margin: 0;
	}
	.authorize-actions {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
	}
</style>
