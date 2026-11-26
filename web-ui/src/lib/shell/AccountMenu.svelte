<!--
  Account menu (views/partials/user-menu.ejs, public/js/app.js): the
  signed-in person with picture or initials and their status dot, then My
  status (online, away, busy), Your account (/account: profile, password,
  two-factor sign-in, signed-in devices, preferences), Signed-in devices
  (the account's sessions on other browsers, A239), API tokens (admins,
  their part of Your account), Preferences (the theme of this browser as a
  choice, Language, which opens the language list, Accessibility, which
  opens its panel) and Sign out.

  The button is the avatar at the end of the top bar, at every width.
-->
<script lang="ts">
	import { t, tf, has } from '#lib/i18n/i18n.svelte.ts';
	import { i18n } from '#lib/i18n/i18n.svelte.ts';
	import { link } from '#lib/nav.ts';
	import SessionsSheet from '#lib/people/SessionsSheet.svelte';
	import { mayMakeTokens } from '#lib/people/apiTokens.ts';
	import { session } from '#lib/state/session.svelte.ts';
	import { setTheme, theme, type ThemeChoice } from '#lib/state/theme.svelte.ts';
	import { myPresence, setPresence, PRESENCE_VALUES } from '#lib/state/presence.svelte.ts';
	import Avatar from '#lib/ui/Avatar.svelte';
	import LangFlag from '#lib/ui/LangFlag.svelte';
	import Menu from '#lib/ui/Menu.svelte';
	import MenuDivider from '#lib/ui/MenuDivider.svelte';
	import MenuItem from '#lib/ui/MenuItem.svelte';
	import Presence from '#lib/ui/Presence.svelte';
	import { a11y } from './a11y.svelte.ts';
	import { chooseLanguage, currentLanguageName } from './language.ts';
	import { signOut } from './sign-out.ts';

	const uid = $props.id();
	let open = $state(false);
	let devices = $state(false);
	/** The language list, opened from Language */
	let languages = $state(false);
	let button = $state<HTMLButtonElement | null>(null);
	/** ArrowUp opens on the last item, everything else on the first (APG menu button) */
	let initialFocus = $state<'first' | 'last'>('first');

	const user = $derived(session.user);
	const person = $derived(
		user
			? {
					id: user.id,
					username: user.username,
					display_name: user.displayName,
					avatar_version: user.avatarVersion ?? null
				}
			: null
	);
	/** The display name: first and last name, else the username */
	const name = $derived(user?.displayName || user?.username || '');
	const role = $derived.by(() => {
		const r = user?.role ?? '';
		if (!r) return '';
		const key = `users.role_${r}`;
		if (has(key)) return t(key);
		const words = r.replace(/_/g, ' ');
		return words.charAt(0).toUpperCase() + words.slice(1);
	});
	const statusLabel = (s: string) => t(`chat.presence_${s}`);
	const THEMES: { value: ThemeChoice; key: string }[] = [
		{ value: 'default', key: 'theme.console_default' },
		{ value: 'light', key: 'theme.light' },
		{ value: 'dark', key: 'theme.dark' }
	];

	function toggle() {
		initialFocus = 'first';
		open = !open;
	}

	function onkeydown(e: KeyboardEvent) {
		if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
			e.preventDefault();
			initialFocus = e.key === 'ArrowUp' ? 'last' : 'first';
			open = true;
		}
	}

	function onclose(returnFocus: boolean) {
		if (returnFocus) button?.focus();
	}

	/** The account menu has closed and given focus back: the language list opens from the same button */
	function openLanguages() {
		queueMicrotask(() => (languages = true));
	}
</script>

<span class="account">
	<button
		bind:this={button}
		type="button"
		class="account-icon-btn"
		aria-haspopup="menu"
		aria-expanded={open}
		aria-controls={open ? `${uid}-menu` : undefined}
		title={name}
		onclick={toggle}
		{onkeydown}
	>
		<Avatar user={person} presence={myPresence.status} />
		<!-- The name, not the initials on the picture, names the button -->
		<span class="sr-only">{name}, {statusLabel(myPresence.status)}</span>
	</button>
	<Menu
		bind:open
		anchor={button}
		align="end"
		{initialFocus}
		id={`${uid}-menu`}
		label={tf('user_menu.label', 'Account')}
		class="account-menu"
		{onclose}
	>
		<!-- The name is on the button already; this repeats it for the eye -->
		<div class="account-person" aria-hidden="true">
			<Avatar user={person} size="md" presence={myPresence.status} />
			<span class="account-text">
				<span class="account-name">{name}</span>
				{#if role}<span class="account-role">{role}</span>{/if}
			</span>
		</div>
		<div role="group" aria-labelledby={`${uid}-status`}>
			<div class="menu-label" id={`${uid}-status`}>{t('chat.my_presence')}</div>
			{#each PRESENCE_VALUES as s (s)}
				<MenuItem type="radio" checked={myPresence.status === s} onselect={() => setPresence(s)}>
					{#snippet lead()}<span class="status-dot"><Presence status={s} /></span>{/snippet}
					{statusLabel(s)}
				</MenuItem>
			{/each}
		</div>
		<MenuDivider />
		<MenuItem icon="person" href={link('/account')}>{t('account.title')}</MenuItem>
		<MenuItem icon="devices" onselect={() => (devices = true)}>{t('sessions.title')}</MenuItem>
		{#if mayMakeTokens(user?.role)}
			<MenuItem icon="api" href={link('/account#api-tokens')} class="act-api-tokens"
				>{t('api_tokens.title')}</MenuItem
			>
		{/if}
		<MenuDivider />
		<div role="group" aria-labelledby={`${uid}-theme`}>
			<div class="menu-label" id={`${uid}-theme`}>{t('theme.title')}</div>
			{#each THEMES as th (th.value)}
				<MenuItem type="radio" checked={theme.choice === th.value} onselect={() => setTheme(th.value)}
					>{t(th.key)}</MenuItem
				>
			{/each}
		</div>
		<MenuItem icon="language" haspopup="menu" onselect={openLanguages}
			>{t('settings.language')}: {currentLanguageName()}</MenuItem
		>
		<MenuItem icon="accessibility_new" haspopup="dialog" onselect={() => (a11y.open = true)}
			>{t('accessibility.title')}</MenuItem
		>
		<MenuDivider />
		<MenuItem icon="logout" onselect={signOut}>{t('user_menu.sign_out')}</MenuItem>
	</Menu>
	{#if devices}
		<SessionsSheet onclose={() => (devices = false)} />
	{/if}
	<Menu
		bind:open={languages}
		anchor={button}
		align="end"
		id={`${uid}-languages`}
		label={t('settings.language')}
		initialFocus="checked"
		class="lang-menu"
		{onclose}
	>
		{#each i18n.available as l (l.code)}
			<MenuItem
				type="radio"
				checked={l.code === i18n.lang}
				lang={l.code}
				onselect={() => chooseLanguage(l.code)}
			>
				{#snippet lead()}<LangFlag code={l.code} />{/snippet}
				{l.native || l.name || l.code}
			</MenuItem>
		{/each}
	</Menu>
</span>

<style>
	.account {
		position: relative;
		display: inline-flex;
	}
	/* The dot on the avatar is ringed in the surface it sits on */
	.account-icon-btn {
		--presence-ring: var(--bg);
		display: inline-flex;
		align-items: center;
		justify-content: center;
		width: var(--control-h);
		height: var(--control-h);
		padding: 0;
		border: 0;
		border-radius: var(--radius);
		background: transparent;
		cursor: pointer;
	}
	.account-icon-btn:hover,
	.account-icon-btn[aria-expanded='true'] {
		--presence-ring: var(--hover);
		background: var(--hover);
	}
	.account-text {
		display: flex;
		flex: 1;
		flex-direction: column;
		min-width: 0;
		line-height: 1.3;
	}
	.account-name {
		overflow: hidden;
		font-weight: 600;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.account-role {
		overflow: hidden;
		color: var(--text-muted);
		font-size: var(--text-sm);
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.account :global(.menu.account-menu) {
		--presence-ring: var(--bg-float);
		width: max-content;
		min-width: 220px;
		max-width: min(320px, calc(100vw - 2 * var(--space-3)));
	}
	.account :global(.menu.lang-menu) {
		min-width: 200px;
		max-height: min(70vh, 480px);
	}
	.account-person {
		display: flex;
		align-items: center;
		gap: var(--space-2);
		margin-bottom: var(--space-1);
		padding: var(--space-2) var(--space-3);
		border-bottom: 1px solid var(--rule);
	}
	.menu-label {
		padding: var(--space-2) var(--space-3) var(--space-1);
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	/* The status dot where menu icons go (20 px box) */
	.status-dot {
		display: inline-flex;
		flex-shrink: 0;
		align-items: center;
		justify-content: center;
		width: 20px;
	}
	@media (pointer: coarse) {
		.account-icon-btn {
			width: 44px;
			height: 44px;
		}
	}
</style>
