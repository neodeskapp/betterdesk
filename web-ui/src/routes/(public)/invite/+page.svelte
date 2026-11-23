<!--
  /invite: where an organization invitation link leads, signed in to the
  console or not. The link carries the invitation's code after #, which
  the browser never sends to the server; the page reads it, keeps it for
  this tab (so a reload keeps it) and takes it out of the address bar.

  The server says which organization the code is for, or that it cannot
  be used (unknown, used, expired or revoked look the same). A usable one
  gets a form: username, password, password again. Redeeming makes a
  sign-in for the NeoDesk client that exists only in that organization:
  the client then shows the organization's shared address book; it cannot
  sign in to the console. Someone signed in to the console is told that
  the new sign-in is separate from their console account. Without a code
  the page asks for the link.

  Data: GET /api/auth/login-page (CSRF token, the console's name), POST
  /api/invitations/lookup, POST /api/invitations/redeem.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { ApiError, setCsrfToken } from '#lib/api/client.ts';
	import {
		lookupInvitation,
		redeemInvitation,
		type InvitationLookup,
		type Redeemed
	} from '#lib/api/invitations.ts';
	import { getLoginPage } from '#lib/api/signin.ts';
	import { formatDate } from '#lib/i18n/format.ts';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import { invitationCode } from '#lib/people/orgs.ts';
	import { dropEarlySession } from '#lib/public/boot.svelte.ts';
	import DownloadFrame from '#lib/public/DownloadFrame.svelte';
	import Button from '#lib/ui/Button.svelte';
	import Field from '#lib/ui/Field.svelte';
	import FormStatus from '#lib/ui/FormStatus.svelte';
	import Input from '#lib/ui/Input.svelte';
	import Notice from '#lib/ui/Notice.svelte';
	import States from '#lib/ui/States.svelte';
	import { createForm, FormError } from '#lib/ui/form.svelte.ts';

	/** The code survives a reload of this tab; it is gone with the tab */
	const KEY = 'nd.invitation';
	const USERNAME = /^[A-Za-z0-9._@-]{3,64}$/;

	type Phase = 'loading' | 'ask' | 'form' | 'invalid' | 'failed' | 'done';
	let phase = $state<Phase>('loading');
	let code = '';
	let invitation = $state.raw<InvitationLookup | null>(null);
	let redeemed = $state.raw<Redeemed | null>(null);
	let appName = $state('');

	function remember(value: string) {
		try {
			if (value) sessionStorage.setItem(KEY, value);
			else sessionStorage.removeItem(KEY);
		} catch {
			/* without storage the code lives only in this page */
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
		code = value;
		phase = 'loading';
		try {
			invitation = await lookupInvitation(code);
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
		const fromLink = invitationCode(location.hash);
		if (location.hash) history.replaceState(history.state, '', location.pathname + location.search);
		if (fromLink) remember(fromLink);
		const found = fromLink || stored();
		void (async () => {
			try {
				const page = await getLoginPage();
				setCsrfToken(page.csrfToken);
				appName = page.appName;
			} catch {
				phase = 'failed';
				return;
			}
			if (found) await check(found);
			else phase = 'ask';
		})();
	});

	const ask = createForm({
		initial: { link: '' },
		validate: (v) => ({ link: invitationCode(v.link) ? undefined : t('organizations.invite_code_bad') }),
		submit: async (v) => {
			const value = invitationCode(v.link);
			remember(value);
			await check(value);
		}
	});

	const form = createForm({
		initial: { username: '', password: '', again: '' },
		validate: (v) => ({
			username: USERNAME.test(v.username.trim()) ? undefined : t('users.invalid_username'),
			password: v.password.length >= 8 ? undefined : t('users.password_too_short'),
			again: v.again === v.password ? undefined : t('settings.passwords_not_match')
		}),
		submit: async (v) => {
			try {
				redeemed = await redeemInvitation({ token: code, username: v.username.trim(), password: v.password });
			} catch (err) {
				if (err instanceof ApiError && err.status === 404) {
					remember('');
					phase = 'invalid';
					return;
				}
				if (err instanceof ApiError && err.status === 409)
					throw new FormError(t('organizations.invite_username_taken'), 'username');
				if (err instanceof ApiError && (err.status === 400 || err.status === 429))
					throw new FormError(err.message);
				throw new FormError(t('organizations.invite_redeem_failed'));
			}
			remember('');
			phase = 'done';
		}
	});

	const expires = $derived(invitation?.expires_at ? formatDate(invitation.expires_at, {}, '') : '');
</script>

<svelte:head>
	<title>{t('organizations.invite_title')}{appName ? ` - ${appName}` : ''}</title>
</svelte:head>

<DownloadFrame>
	<header class="invite-head">
		<h1>{phase === 'done' ? t('organizations.invite_done_title') : t('organizations.invite_title')}</h1>
	</header>

	<div class="invite-body" aria-live="polite">
		{#if phase === 'loading'}
			<States kind="loading" />
		{:else if phase === 'failed'}
			<States
				kind="error"
				message={t('organizations.invite_load_failed')}
				onretry={() => (code ? void check(code) : location.reload())}
			/>
		{:else if phase === 'invalid'}
			<Notice tone="warning" class="invite-invalid" message={t('organizations.invite_invalid')} />
		{:else if phase === 'ask'}
			<form id={ask.id} class="invite-form" onsubmit={ask.onsubmit} novalidate>
				<Field label={t('organizations.invite_code_label')} error={ask.errors.link}>
					{#snippet children(f)}
						<Input {...f} width="host" autocomplete="off" spellcheck="false" bind:value={ask.values.link} />
					{/snippet}
				</Field>
				<div>
					<Button type="submit" variant="primary" busy={ask.busy}
						>{t('organizations.invite_code_submit')}</Button
					>
				</div>
			</form>
		{:else if phase === 'form' && invitation}
			<p class="invite-for">{t('organizations.invite_for', { org: invitation.organization.name })}</p>
			<p class="invite-note">
				{t('organizations.client_signin_hint')}
				{#if expires}{t('organizations.invite_expires', { date: expires })}{/if}
			</p>
			{#if invitation.account}
				<Notice
					class="invite-signed-in"
					message={t('organizations.invite_signed_in', { name: invitation.account.username })}
				/>
			{/if}
			<form id={form.id} class="invite-form" onsubmit={form.onsubmit} novalidate>
				<Field label={t('organizations.username')} error={form.errors.username}>
					{#snippet children(f)}
						<Input
							{...f}
							width="medium"
							autocomplete="username"
							spellcheck="false"
							bind:value={form.values.username}
						/>
					{/snippet}
				</Field>
				<Field
					label={t('organizations.password')}
					hint={t('settings.req_length')}
					error={form.errors.password}
				>
					{#snippet children(f)}
						<Input
							{...f}
							width="medium"
							type="password"
							autocomplete="new-password"
							bind:value={form.values.password}
						/>
					{/snippet}
				</Field>
				<Field label={t('organizations.invite_password_again')} error={form.errors.again}>
					{#snippet children(f)}
						<Input
							{...f}
							width="medium"
							type="password"
							autocomplete="new-password"
							bind:value={form.values.again}
						/>
					{/snippet}
				</Field>
				<FormStatus status={form.status} />
				<div>
					<Button type="submit" variant="primary" busy={form.busy}>{t('organizations.invite_submit')}</Button>
				</div>
			</form>
		{:else if phase === 'done' && redeemed}
			<p class="invite-done">
				{t('organizations.invite_done_text', {
					slug: redeemed.organization.slug,
					username: redeemed.username
				})}
			</p>
		{/if}
	</div>
</DownloadFrame>

<style>
	.invite-head h1 {
		margin: 0;
	}
	.invite-body {
		display: flex;
		flex-direction: column;
		gap: var(--space-4);
		max-width: var(--measure);
	}
	.invite-body p {
		margin: 0;
	}
	.invite-for {
		font-weight: 600;
	}
	.invite-note {
		color: var(--text-muted);
	}
	.invite-form {
		display: flex;
		flex-direction: column;
		gap: var(--space-2);
	}
</style>
