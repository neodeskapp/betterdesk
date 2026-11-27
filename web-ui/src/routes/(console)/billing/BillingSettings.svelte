<!--
  Billing settings (#billing-settings-panel): a closed section in the side
  column, opened by ?settings=1 or ?tab=settings. The link to the server
  clock settings, whether email sending works, and the help request email
  options (the other three show while "Send email for new help requests"
  is on). Loads when it opens. People without billing.manage see the
  options switched off for editing and no save bar. The options show only
  once the stored values loaded, so a save never writes the start values.
  The settings pattern (PATTERN.md rules 1, 5, 6 and 11): a change waits
  in the save bar until Save, the toast names it, each option says what it
  does. That no mail goes out without email sending is said once on the
  group, with its link; nobody to mail is said under the main option.
-->
<script lang="ts">
	import { tick, untrack } from 'svelte';
	import { getEmailConfig, saveEmailConfig, smtpConfigured } from '#lib/api/billing.ts';
	import { t, tf } from '#lib/i18n/i18n.svelte.ts';
	import { link } from '#lib/nav.ts';
	import Disclosure from '#lib/ui/Disclosure.svelte';
	import States from '#lib/ui/States.svelte';
	import ChangesBar from '#lib/ui/settings/ChangesBar.svelte';
	import SettingRow from '#lib/ui/settings/SettingRow.svelte';
	import SettingsGroup from '#lib/ui/settings/SettingsGroup.svelte';
	import { SettingsDraft, saveAndConfirm } from '#lib/ui/settings/draft.svelte.ts';

	let { open = false, canManage }: { open?: boolean; canManage: boolean } = $props();

	// svelte-ignore state_referenced_locally
	let isOpen = $state(open);
	let loaded = $state(false);
	let loadError = $state(false);
	/** The stored values are in the draft */
	let ready = $state(false);
	let smtp = $state<boolean | null>(null);
	let host = $state<HTMLElement | null>(null);

	type Values = {
		help_requests_enabled: boolean;
		notify_assigned_operators: boolean;
		fallback_alert_email: boolean;
		include_folder_in_subject: boolean;
	};
	const KEYS = [
		'help_requests_enabled',
		'notify_assigned_operators',
		'fallback_alert_email',
		'include_folder_in_subject'
	] as const;
	const label = (k: (typeof KEYS)[number]) => t(`commercialization.notifications.${k}`);

	const draft = new SettingsDraft<Values>(
		{
			help_requests_enabled: true,
			notify_assigned_operators: true,
			fallback_alert_email: true,
			include_folder_in_subject: true
		},
		Object.fromEntries(KEYS.map((k) => [k, { label: label(k) }]))
	);
	$effect(() => draft.track());

	async function load() {
		loaded = true;
		smtpConfigured().then(
			(on) => (smtp = on),
			() => (smtp = false)
		);
		loadError = false;
		try {
			draft.load(await getEmailConfig());
			ready = true;
		} catch {
			loadError = true;
		}
	}

	$effect(() => {
		if (isOpen && !untrack(() => loaded)) void load();
	});

	// Opened from the URL: scroll it into view once it is drawn
	$effect(() => {
		if (!untrack(() => open)) return;
		void tick().then(() => host?.scrollIntoView({ block: 'start' }));
	});

	const HELP: Record<(typeof KEYS)[number], [string, string]> = {
		help_requests_enabled: [
			'commercialization.notifications.help_requests_hint',
			'A mail goes out when a device asks for help.'
		],
		notify_assigned_operators: [
			'commercialization.notifications.notify_assigned_hint',
			'The mail goes to the operators who have the device’s folder or group.'
		],
		fallback_alert_email: [
			'commercialization.notifications.fallback_hint',
			'When no operator gets the mail, it goes to the alert address in Settings › Email.'
		],
		include_folder_in_subject: [
			'commercialization.notifications.folder_subject_hint',
			'The subject names the device’s folder, so mails can be sorted by it.'
		]
	};
	const help = (k: (typeof KEYS)[number]) => tf(HELP[k][0], HELP[k][1]);

	const noRecipient = $derived(
		draft.values.help_requests_enabled &&
			!draft.values.notify_assigned_operators &&
			!draft.values.fallback_alert_email
	);
</script>

{#snippet row(k: (typeof KEYS)[number], noEffect?: string)}
	<SettingRow
		id="billing-notif-{k.replaceAll('_', '-')}"
		label={label(k)}
		help={help(k)}
		checked={draft.values[k]}
		disabled={!canManage}
		changed={draft.isChanged(k)}
		onchange={(on) => draft.set(k, on)}
		{noEffect}
	/>
{/snippet}

<div id="billing-settings-panel" class="settings" bind:this={host}>
	<Disclosure class="section" title={t('commercialization.ui.settings_title')} bind:open={isOpen}>
		<div class="body">
			<p class="section-note">
				{t('commercialization.ui.settings_note')}
				<a href={link('/settings?tab=time')}>{t('commercialization.clock.open_time_settings')}</a>
			</p>
			{#if loadError}
				<States kind="error" message={t('commercialization.ui.error_settings')} onretry={() => void load()} />
			{/if}
			{#if ready}
				<SettingsGroup
					id="billing-notifications"
					level={3}
					title={t('commercialization.notifications.title')}
					description={t('commercialization.notifications.desc')}
					consequence={t('commercialization.ui.applies_now')}
				>
					{#snippet note()}
						<!-- Whether any of these mails can go out holds for the whole group: said once, here, with the way to fix it -->
						<p class={['line', 'smtp', smtp === false && 'is-warning']}>
							{#if smtp === true}{t(
									'commercialization.notifications.smtp_configured'
								)}{:else if smtp === false}{tf(
									'commercialization.notifications.no_smtp_note',
									'Email sending is not set up, so none of these mails go out yet.'
								)}{/if}
							<a href={link('/settings?tab=email')}>{t('commercialization.notifications.smtp_link')}</a>
						</p>
					{/snippet}
					{@render row('help_requests_enabled')}
					{#if draft.values.help_requests_enabled}
						{#if noRecipient}
							<p class="warn" role="status">
								{tf(
									'commercialization.notifications.no_recipient',
									'Has no effect: nobody gets the mail while both options below are off.'
								)}
							</p>
						{/if}
						{@render row('notify_assigned_operators')}
						{@render row('fallback_alert_email')}
						{@render row('include_folder_in_subject')}
					{/if}
				</SettingsGroup>
			{/if}
		</div>
	</Disclosure>
	{#if canManage && ready}
		<ChangesBar
			{draft}
			saveLabel={t('commercialization.notifications.save')}
			onsave={() => saveAndConfirm(draft, () => saveEmailConfig(draft.values))}
		/>
	{/if}
</div>

<style>
	.settings {
		scroll-margin-top: calc(var(--topbar-h, 56px) + var(--space-4));
	}
	.body {
		display: flex;
		flex-direction: column;
		gap: var(--space-3);
		margin-top: var(--space-2);
	}
	.line {
		margin: 0;
	}
	.smtp.is-warning {
		color: var(--warning);
	}
	.smtp {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-1) var(--space-3);
		font-size: var(--text-sm);
	}
	.warn {
		margin: 0;
		padding: var(--space-2) var(--row-pad-x);
		border-bottom: 1px solid var(--rule);
		color: var(--warning);
		font-size: var(--text-sm);
	}
</style>
