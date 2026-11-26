<!--
  Your account › Preferences (#preferences): the theme of this browser
  (Console default follows Settings › Appearance; Light and Dark are this
  browser's own), the console language of the account, its time zone
  (the security mails give times in it), and the accessibility panel. The account menu has the same three under
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
	import ChoiceList from '#lib/ui/ChoiceList.svelte';
	import Field from '#lib/ui/Field.svelte';
	import Select from '#lib/ui/Select.svelte';
	import SettingsGroup from '#lib/ui/settings/SettingsGroup.svelte';
	import { toast } from '#lib/ui/toasts.svelte.ts';

	const themes = $derived<{ value: ThemeChoice; title: string }[]>([
		{ value: 'default', title: t('theme.console_default') },
		{ value: 'light', title: t('theme.light') },
		{ value: 'dark', title: t('theme.dark') }
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
	<div class="prefs">
		<div class="pref" id="preferences-theme">
			<h3 id="preferences-theme-title">{t('account.theme')}</h3>
			<p class="form-hint" id="preferences-theme-note">{t('account.theme_note')}</p>
			<ChoiceList
				choices={themes}
				name="preferences-theme"
				labelledby="preferences-theme-title"
				describedby="preferences-theme-note preferences-theme-source"
				value={theme.choice}
				onchange={(v, checked) => {
					if (checked) setTheme(v);
				}}
			/>
			<p class="source" id="preferences-theme-source">
				{themeSource}
				{#if mayOpenAppearance}<a href={link('/settings?tab=branding')}
						>{tf('account.theme_source_link', 'Settings › Appearance')}</a
					>{/if}
			</p>
		</div>
		<div class="pref">
			<Field id="preferences-language" label={t('settings.language')} hint={t('account.language_note')}>
				{#snippet children(f)}
					<Select
						{...f}
						width="medium"
						options={languages}
						value={i18n.lang}
						onchange={(e) => void chooseLanguage(e.currentTarget.value)}
					/>
				{/snippet}
			</Field>
		</div>
		<div class="pref" id="preferences-time-zone">
			{#if zone === 'error'}
				<p class="form-hint">{t('account.time_zone_load_failed')}</p>
			{:else}
				<Field
					id="preferences-time-zone-field"
					label={t('users.time_zone')}
					hint={browserZone && zone !== browserZone
						? t('account.time_zone_note_browser', { zone: browserZone })
						: t('account.time_zone_note')}
				>
					{#snippet children(f)}
						<Select
							{...f}
							width="medium"
							options={zones}
							value={zone ?? ''}
							disabled={zone === null}
							onchange={(e) => void pickZone(e.currentTarget.value)}
						/>
					{/snippet}
				</Field>
			{/if}
		</div>
		<div class="pref" id="preferences-accessibility">
			<h3>{t('accessibility.title')}</h3>
			<p class="form-hint">{a11yOn ? t('account.accessibility_on') : t('account.accessibility_note')}</p>
			<p class="line">
				<Button icon="accessibility_new" aria-haspopup="dialog" onclick={() => (a11y.open = true)}
					>{t('account.accessibility_open')}</Button
				>
			</p>
		</div>
	</div>
</SettingsGroup>

<style>
	.prefs {
		padding: var(--space-3) var(--row-pad-x) 0;
		display: flex;
		flex-direction: column;
		gap: var(--space-5);
		max-width: var(--measure);
	}
	.pref {
		display: flex;
		flex-direction: column;
		gap: var(--space-2);
	}
	.pref :global(.form-group) {
		margin-bottom: 0;
	}
	h3 {
		margin: 0;
		font-size: var(--text-md);
	}
	.form-hint,
	.line {
		margin: 0;
	}
	.form-hint,
	.source {
		margin: 0;
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
</style>
