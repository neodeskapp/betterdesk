<!--
  /confirm-email: where the link of an email address change leads. The
  link carries its token after #, which the browser never sends to the
  server; the page reads it, keeps it for this tab (so a reload keeps it)
  and takes it out of the address bar.

  The server says which account and address the link is for, or that it
  cannot be used (unknown, used, expired and cancelled look the same). A
  usable one gets one button: Confirm address, so a mail scanner that
  opens the link changes nothing. Confirming sets the address and signs
  the account out on its other devices.

  Data: GET /api/auth/login-page (CSRF token, the console's name), POST
  /api/auth/email-change/lookup, POST /api/auth/email-change/confirm.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { ApiError, setCsrfToken } from '#lib/api/client.ts';
	import { linkToken } from '#lib/api/passwordlinks.ts';
	import { getLoginPage } from '#lib/api/signin.ts';
	import { confirmEmailChange, lookupEmailChange, type EmailChangeLink } from '#lib/api/users.ts';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import { link } from '#lib/nav.ts';
	import { dropEarlySession } from '#lib/public/boot.svelte.ts';
	import DownloadFrame from '#lib/public/DownloadFrame.svelte';
	import Button from '#lib/ui/Button.svelte';
	import FormStatus from '#lib/ui/FormStatus.svelte';
	import Notice from '#lib/ui/Notice.svelte';
	import States from '#lib/ui/States.svelte';

	/** The token survives a reload of this tab; it is gone with the tab */
	const KEY = 'nd.email-change';

	type Phase = 'loading' | 'none' | 'form' | 'invalid' | 'failed' | 'done';
	let phase = $state<Phase>('loading');
	let token = '';
	let found = $state.raw<EmailChangeLink | null>(null);
	let appName = $state('');
	let busy = $state(false);
	let error = $state('');

	function remember(value: string) {
		try {
			if (value) sessionStorage.setItem(KEY, value);
			else sessionStorage.removeItem(KEY);
		} catch {
			/* without storage the token lives only in this page */
		}
	}

	function stored(): string {
		try {
			return sessionStorage.getItem(KEY) ?? '';
		} catch {
			return '';
		}
	}

	async function check(value: string) {
		token = value;
		phase = 'loading';
		try {
			found = await lookupEmailChange(token);
			phase = 'form';
		} catch (err) {
			if (err instanceof ApiError && err.status === 404) {
				remember('');
				phase = 'invalid';
			} else phase = 'failed';
		}
	}

	onMount(() => {
		dropEarlySession();
		const fromLink = linkToken(location.hash);
		if (location.hash) history.replaceState(history.state, '', location.pathname + location.search);
		if (fromLink) remember(fromLink);
		const value = fromLink || stored();
		void (async () => {
			try {
				const page = await getLoginPage();
				setCsrfToken(page.csrfToken);
				appName = page.appName;
			} catch {
				phase = 'failed';
				return;
			}
			if (value) await check(value);
			else phase = 'none';
		})();
	});

	async function confirmIt() {
		busy = true;
		error = '';
		try {
			found = await confirmEmailChange(token);
			remember('');
			phase = 'done';
		} catch (err) {
			if (err instanceof ApiError && err.status === 404) {
				remember('');
				phase = 'invalid';
			} else if (err instanceof ApiError && err.status === 429) error = err.message;
			else error = t('password_link.check_failed');
		} finally {
			busy = false;
		}
	}

	const title = $derived(
		phase === 'done'
			? t('email_change.done_title')
			: phase === 'invalid'
				? t('email_change.invalid_title')
				: t('email_change.page_title')
	);
</script>

<svelte:head>
	<title>{title}{appName ? ` - ${appName}` : ''}</title>
</svelte:head>

<DownloadFrame>
	<header class="link-head">
		<h1>{title}</h1>
	</header>

	<div class="link-body" aria-live="polite">
		{#if phase === 'loading'}
			<States kind="loading" />
		{:else if phase === 'failed'}
			<States
				kind="error"
				message={t('password_link.check_failed')}
				onretry={() => (token ? void check(token) : location.reload())}
			/>
		{:else if phase === 'none'}
			<p>{t('email_change.no_link')}</p>
		{:else if phase === 'invalid'}
			<Notice tone="warning" class="link-invalid" message={t('email_change.unusable')} />
		{:else if phase === 'form' && found}
			<p class="link-for">{t('email_change.page_intro', { email: found.email, username: found.username })}</p>
			<FormStatus message={error} tone="error" />
			<div>
				<Button variant="primary" {busy} onclick={() => void confirmIt()}
					>{t('email_change.page_confirm')}</Button
				>
			</div>
		{:else if phase === 'done' && found}
			<p class="link-done">{t('email_change.done_text', { email: found.email, username: found.username })}</p>
			<div>
				<Button variant="primary" href={link('/login')}>{t('password_link.sign_in')}</Button>
			</div>
		{/if}
	</div>
</DownloadFrame>

<style>
	.link-head h1 {
		margin: 0;
	}
	.link-body {
		display: flex;
		flex-direction: column;
		gap: var(--space-4);
		max-width: var(--measure);
	}
	.link-body p {
		margin: 0;
		overflow-wrap: anywhere;
	}
</style>
