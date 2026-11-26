<!--
  /set-password: where the link of an invite or a password reset mail
  leads, and where "Forgot password?" on the sign-in page goes. The link carries
  its token after #, which the browser never sends to the server; the page
  reads it, keeps it for this tab (so a reload keeps it) and takes it out
  of the address bar.

  The server says which account the link is for, or that it cannot be used
  (unknown, used and expired look the same). A usable one gets a form:
  password, password again. Saving sets the password, spends the link and
  signs the account out everywhere; the page then sends the person to the
  sign-in. Without a token the page asks for a username or email address
  and requests a reset link for it, when the console can send mail; else
  it says to open the link from the mail.

  Data: GET /api/auth/login-page (CSRF token, the console's name), POST
  /api/auth/password-link/lookup, POST /api/auth/password-link/redeem, GET
  /api/auth/password-reset/available, POST /api/auth/password-reset/request.

  Screen readers: when the step changes, focus goes to the heading, so the
  new step is read from its start (the steps with a field focus the field
  instead). Only the status sentences are live, not the whole form.
-->
<script lang="ts">
	import { onMount, tick, untrack } from 'svelte';
	import { ApiError, setCsrfToken } from '#lib/api/client.ts';
	import {
		linkToken,
		lookupPasswordLink,
		passwordResetAvailable,
		redeemPasswordLink,
		requestPasswordReset,
		type PasswordLink
	} from '#lib/api/passwordlinks.ts';
	import { getLoginPage } from '#lib/api/signin.ts';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import { link } from '#lib/nav.ts';
	import PasswordField from '#lib/people/PasswordField.svelte';
	import { passwordProblem } from '#lib/people/users.ts';
	import { dropEarlySession } from '#lib/public/boot.svelte.ts';
	import DownloadFrame from '#lib/public/DownloadFrame.svelte';
	import Button from '#lib/ui/Button.svelte';
	import Field from '#lib/ui/Field.svelte';
	import FormStatus from '#lib/ui/FormStatus.svelte';
	import Input from '#lib/ui/Input.svelte';
	import Notice from '#lib/ui/Notice.svelte';
	import States from '#lib/ui/States.svelte';
	import { createForm, FormError } from '#lib/ui/form.svelte.ts';

	/** The token survives a reload of this tab; it is gone with the tab */
	const KEY = 'nd.password-link';

	type Phase = 'loading' | 'none' | 'request' | 'requested' | 'form' | 'invalid' | 'failed' | 'done';
	let phase = $state<Phase>('loading');
	let heading = $state<HTMLElement | null>(null);

	/* A new step: focus on its heading, unless the step starts in its field */
	$effect(() => {
		const p = phase;
		if (p === 'loading' || p === 'form' || p === 'request') return;
		untrack(() => void tick().then(() => heading?.focus()));
	});
	let token = '';
	let found = $state.raw<PasswordLink | null>(null);
	let doneFor = $state('');
	let appName = $state('');

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
			found = await lookupPasswordLink(token);
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
			else phase = (await passwordResetAvailable().catch(() => false)) ? 'request' : 'none';
		})();
	});

	const ask = createForm({
		initial: { login: '' },
		validate: (v) => ({ login: v.login.trim() ? undefined : t('auth.fill_all_fields') }),
		submit: async (v) => {
			try {
				await requestPasswordReset(v.login.trim());
			} catch (err) {
				if (err instanceof ApiError && err.status === 429) throw new FormError(err.message);
				throw new FormError(t('password_link.check_failed'));
			}
			phase = 'requested';
		}
	});

	const form = createForm({
		initial: { password: '', again: '' },
		validate: (v) => {
			const problem = passwordProblem(v.password, true);
			return {
				password: problem ? t(problem) : undefined,
				again: v.again === v.password ? undefined : t('password_link.passwords_differ')
			};
		},
		submit: async (v) => {
			try {
				doneFor = (await redeemPasswordLink({ token, password: v.password })).username;
			} catch (err) {
				if (err instanceof ApiError && err.status === 404) {
					remember('');
					phase = 'invalid';
					return;
				}
				if (err instanceof ApiError && (err.status === 400 || err.status === 429))
					throw new FormError(err.message);
				throw new FormError(t('password_link.check_failed'));
			}
			remember('');
			phase = 'done';
		}
	});

	const reset = $derived(found?.purpose === 'reset');
	const title = $derived(
		phase === 'done'
			? t('password_link.done_title')
			: phase === 'invalid'
				? t('password_link.invalid_title')
				: phase === 'request' || phase === 'requested'
					? t('auth.forgot_title')
					: reset
						? t('password_link.page_title_reset')
						: t('password_link.page_title_invite')
	);
</script>

<svelte:head>
	<title>{title}{appName ? ` - ${appName}` : ''}</title>
</svelte:head>

<DownloadFrame>
	<header class="link-head">
		<h1 bind:this={heading} tabindex="-1">{title}</h1>
	</header>

	<div class="link-body">
		{#if phase === 'loading'}
			<States kind="loading" />
		{:else if phase === 'failed'}
			<States
				kind="error"
				message={t('password_link.check_failed')}
				onretry={() => (token ? void check(token) : location.reload())}
			/>
		{:else if phase === 'none'}
			<p>{t('password_link.no_link')}</p>
		{:else if phase === 'request'}
			<p>{t('auth.forgot_intro')}</p>
			<form id={ask.id} class="link-form" onsubmit={ask.onsubmit} novalidate>
				<Field label={t('auth.forgot_field')} error={ask.errors.login}>
					{#snippet children(f)}
						<Input
							{...f}
							width="host"
							autocomplete="username"
							autocapitalize="none"
							spellcheck="false"
							data-autofocus
							bind:value={ask.values.login}
						/>
					{/snippet}
				</Field>
				<FormStatus status={ask.status} />
				<div class="link-actions">
					<Button type="submit" variant="primary" busy={ask.busy}>{t('auth.forgot_send')}</Button>
					<Button variant="ghost" href={link('/login')}>{t('auth.forgot_back')}</Button>
				</div>
			</form>
		{:else if phase === 'requested'}
			<p class="link-sent" role="status">{t('auth.forgot_sent')}</p>
			<div><Button href={link('/login')}>{t('auth.forgot_back')}</Button></div>
		{:else if phase === 'invalid'}
			<Notice tone="warning" class="link-invalid" message={t('password_link.unusable')} />
		{:else if phase === 'form' && found}
			<p class="link-for">
				{reset
					? t('password_link.page_intro_reset', { username: found.username })
					: t('password_link.page_intro_invite', { server: appName || 'NeoDesk', username: found.username })}
			</p>
			<form id={form.id} class="link-form" onsubmit={form.onsubmit} novalidate>
				<!-- The username for password managers, which file the new password under it -->
				<input
					class="sr-only"
					type="text"
					autocomplete="username"
					value={found.username}
					readonly
					tabindex="-1"
					aria-hidden="true"
				/>
				<PasswordField
					label={t('users.password')}
					hint={t('users.password_hint')}
					error={form.errors.password}
					autofocus
					bind:value={form.values.password}
				/>
				<Field label={t('password_link.repeat_password')} error={form.errors.again}>
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
					<Button type="submit" variant="primary" busy={form.busy}>{t('password_link.save')}</Button>
				</div>
			</form>
		{:else if phase === 'done'}
			<p class="link-done" role="status">
				{t(reset ? 'password_link.done_text_reset' : 'password_link.done_text', { username: doneFor })}
			</p>
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
	/* Focus lands here on a new step to read it, not to act on it */
	.link-head h1:focus {
		outline: none;
	}
	.link-body {
		display: flex;
		flex-direction: column;
		gap: var(--space-4);
		max-width: var(--measure);
	}
	.link-body p {
		margin: 0;
	}
	.link-actions {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
	}
	.link-form {
		display: flex;
		flex-direction: column;
		gap: var(--space-2);
	}
</style>
