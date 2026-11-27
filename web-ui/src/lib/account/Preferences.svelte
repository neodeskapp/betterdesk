<!--
  Your account › Preferences (#preferences): the theme of this browser
  (Console default follows Settings › Appearance; Light and Dark are this
  browser's own), the console language of the account, its time zone
  (the security mails give times in it), and the accessibility panel.
  Settings rows: name and help left, the control right on a wide page;
  the theme's three choices side by side, one click each. The account
  menu has the same three under
  Preferences. Settings › Appearance › Theme is the console's default for
  everyone, which is something else.
-->
<script lang="ts">
	import { untrack } from 'svelte';
	import { getOwnTimeZone, saveOwnTimeZone } from '#lib/api/users.ts';
	import { i18n, t, tf } from '#lib/i18n/i18n.svelte.ts';
	import { link } from '#lib/nav.ts';
	import { browserTimeZone, timeZoneOptions } from '#lib/people/time-zones.ts';
	import { a11y, isDefault } from '#lib/shell/a11y.svelte.ts';
	import { chooseLanguage } from '#lib/shell/language.ts';
	import { can } from '#lib/state/session.svelte.ts';
	import { setTheme, theme, type ThemeChoice } from '#lib/state/theme.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import Select from '#lib/ui/Select.svelte';
	import ChoiceSegments from '#lib/ui/settings/ChoiceSegments.svelte';
	import SettingRow from '#lib/ui/settings/SettingRow.svelte';
	import SettingsGroup from '#lib/ui/settings/SettingsGroup.svelte';
	import { toast } from '#lib/ui/toasts.svelte.ts';

	const themes = $derived<{ value: ThemeChoice; label: string }[]>([
		{ value: 'default', label: t('theme.console_default') },
		{ value: 'light', label: t('theme.light') },
		{ value: 'dark', label: t('theme.dark') }
	]);
	const languages = $derived(
		i18n.available.map((l) => ({ value: l.code, label: l.native || l.name || l.code }))
	);
	const a11yOn = $derived(!isDefault(a11y.settings));

	/** The console default (Settings › Appearance) in words */
	const consoleTheme = $derived(
		theme.consoleMode === 'dark'
			? tf('theme.dark', 'Dark')
			: theme.consoleMode === 'light'
				? tf('theme.light', 'Light')
				: tf('account.theme_system', 'System (follows this device)')
	);
	const themeSource = $derived(
		theme.choice === 'default'
			? tf('account.theme_now_default', 'Now: {theme}, the console default.', { theme: consoleTheme })
			: tf('account.theme_now_own', 'Set in this browser. The console default is {theme}.', {
					theme: consoleTheme
				})
	);
	const mayOpenAppearance = $derived(can('server.config'));
	/** The account's time zone; null until it is read, 'error' when that failed */
	let zone = $state<string | null | 'error'>(null);
	const zones = $derived(
		timeZoneOptions(t('users.time_zone_none'), typeof zone === 'string' && zone !== 'error' ? zone : '')
	);
	const browserZone = browserTimeZone();

	$effect(() => {
		untrack(() => {
			getOwnTimeZone().then(
				(z) => (zone = z),
				() => (zone = 'error')
			);
		});
	});

	async function pickZone(next: string) {
		const before = zone;
		zone = next;
		try {
			await saveOwnTimeZone(next);
			toast.success(t('account.time_zone_saved'));
		} catch (err) {
			zone = before;
			toast.error(err instanceof Error && err.message ? err.message : t('account.time_zone_failed'));
		}
	}
</script>

<SettingsGroup
	id="preferences"
	title={t('account.preferences')}
	description={tf('account.prefs_description', 'How the console looks and reads for you.')}
	consequence={`${tf('settings_ui.model_instant', 'Changes here apply at once, only for you.')} ${tf(
		'account.prefs_where',
		'Theme and accessibility stay in this browser. The language goes with your account to every device.'
	)}`}
>
	<!-- What applies now comes from Settings › Appearance unless this browser has its own: said under the theme, with the link -->
	<SettingRow
		id="preferences-theme"
		label={t('account.theme')}
		help={t('account.theme_note')}
		group
		source={{
			kind: theme.choice === 'default' ? 'inherited' : 'here',
			text: themeSource,
			href: mayOpenAppearance ? link('/settings?tab=branding') : undefined,
			linkLabel: tf('account.theme_source_link', 'Settings › Appearance'),
			visible: true
		}}
	>
		{#snippet control(f)}
			<ChoiceSegments
				id={f.id}
				labelledby={f.labelledby}
				describedby={f.describedby}
				options={themes}
				value={theme.choice}
				onchange={(v) => setTheme(v as ThemeChoice)}
			/>
		{/snippet}
	</SettingRow>
	<SettingRow id="preferences-language" label={t('settings.language')} help={t('account.language_note')}>
		{#snippet control(f)}
			<Select
				id={f.id}
				describedby={f.describedby}
				options={languages}
				value={i18n.lang}
				onchange={(e) => void chooseLanguage(e.currentTarget.value)}
			/>
		{/snippet}
	</SettingRow>
	<SettingRow
		id="preferences-time-zone"
		label={t('users.time_zone')}
		help={zone === 'error'
			? t('account.time_zone_load_failed')
			: browserZone && zone !== browserZone
				? t('account.time_zone_note_browser', { zone: browserZone })
				: t('account.time_zone_note')}
	>
		{#snippet control(f)}
			{#if zone !== 'error'}
				<Select
					id={f.id}
					describedby={f.describedby}
					options={zones}
					value={zone ?? ''}
					disabled={zone === null}
					onchange={(e) => void pickZone(e.currentTarget.value)}
				/>
			{/if}
		{/snippet}
	</SettingRow>
	<SettingRow
		id="preferences-accessibility"
		label={t('accessibility.title')}
		help={a11yOn ? t('account.accessibility_on') : t('account.accessibility_note')}
	>
		{#snippet control(f)}
			<!-- Its own words name it, not the row's label pointing at it -->
			<Button
				id={f.id}
				aria-label={t('account.accessibility_open')}
				aria-describedby={f.describedby}
				icon="accessibility_new"
				aria-haspopup="dialog"
				onclick={() => (a11y.open = true)}>{t('account.accessibility_open')}</Button
			>
		{/snippet}
	</SettingRow>
</SettingsGroup>
