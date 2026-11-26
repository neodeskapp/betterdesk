<!--
  Your account (/account), from the account menu, for every account: the
  signed-in person's own things, which sat in Settings and on other pages
  before.
  - Profile: picture, name, role and My status (#profile)
  - Email address (#email): a new one waits for its confirmation link
  - Password (#password) and Two-factor sign-in (#two-factor), from
    Settings › Sign-in
  - Signed-in devices (#sessions), Audit log › Your sessions before
  - Recent sign-ins and security events (#security-events): sign-ins,
    password and two-factor changes, and when an admin viewed NeoDesk as
    the person
  - API tokens (#api-tokens) for the roles that may make them, /api-tokens
    before
  - Preferences (#preferences): theme of this browser, language,
    accessibility, from the top bar
  The old addresses lead here (lib/redirects.ts). A #<id> scrolls to that
  part or field and marks it, as on the Settings page (SettingsPageFrame);
  the settings search finds the fields (search-index.ts, place "account").
-->
<script lang="ts">
	import ApiTokens from '#lib/account/ApiTokens.svelte';
	import EmailAddress from '#lib/account/EmailAddress.svelte';
	import PasswordAndTwoFactor from '#lib/account/PasswordAndTwoFactor.svelte';
	import Preferences from '#lib/account/Preferences.svelte';
	import Profile from '#lib/account/Profile.svelte';
	import SecurityEvents from '#lib/account/SecurityEvents.svelte';
	import Sessions from '#lib/account/Sessions.svelte';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import { mayMakeTokens } from '#lib/people/apiTokens.ts';
	import SettingsPageFrame from '#lib/settings/SettingsPageFrame.svelte';
	import { session } from '#lib/state/session.svelte.ts';
</script>

<SettingsPageFrame id="account" need="" title={t('account.title')} description={t('account.description')}>
	<div class="account-parts">
		<Profile />
		<EmailAddress />
		<PasswordAndTwoFactor />
		<Sessions />
		<SecurityEvents />
		{#if mayMakeTokens(session.user?.role)}<ApiTokens />{/if}
		<Preferences />
	</div>
</SettingsPageFrame>

<style>
	.account-parts {
		display: flex;
		flex-direction: column;
		gap: var(--space-7);
	}
</style>
