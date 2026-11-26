<!--
  Settings › Appearance (?tab=branding): how the console looks for
  everyone: the default theme and custom colours, fonts, the console
  background, and custom CSS behind Advanced. One save model (PATTERN.md
  rule 1): changes collect in the save bar and go out together with Save
  (POST /api/settings/branding, the whole appearance, shared with
  Settings › Branding through AppearanceForm). The preview beside them
  shows the choices before they are saved.

  Actions that are no settings run at once and say so: applying a built-in
  theme or a saved appearance, importing, downloading or uploading fonts
  to the server, and Reset in the danger zone. Each asks first when there
  are unsaved changes, as it replaces them.

  Everyone may look; branding.edit may change (the rows are read-only
  otherwise, and the parts that only change things are left out). Element
  ids are the old page's, for links and the settings search.
-->
<script lang="ts">
	import {
		applyProfile,
		applyTheme as applyBuiltinTheme,
		createProfile,
		deleteFont,
		deleteProfile,
		duplicateProfile,
		exportBranding,
		importBranding,
		listBackgrounds,
		listLocalFonts,
		listProfiles,
		listThemes,
		resetBranding,
		updateProfile,
		uploadFont,
		type BackgroundImage,
		type BrandingProfile,
		type BuiltinTheme
	} from '#lib/api/settings.ts';
	import { t, tf } from '#lib/i18n/i18n.svelte.ts';
	import { can } from '#lib/state/session.svelte.ts';
	import { theme } from '#lib/state/theme.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import ChoiceList from '#lib/ui/ChoiceList.svelte';
	import ColorField from '#lib/ui/ColorField.svelte';
	import CountLine from '#lib/ui/CountLine.svelte';
	import DangerZone from '#lib/ui/DangerZone.svelte';
	import Disclosure from '#lib/ui/Disclosure.svelte';
	import Field from '#lib/ui/Field.svelte';
	import FilePicker from '#lib/ui/FilePicker.svelte';
	import FormStatus from '#lib/ui/FormStatus.svelte';
	import Input from '#lib/ui/Input.svelte';
	import ListRow from '#lib/ui/ListRow.svelte';
	import MenuButton from '#lib/ui/MenuButton.svelte';
	import MenuDivider from '#lib/ui/MenuDivider.svelte';
	import MenuItem from '#lib/ui/MenuItem.svelte';
	import Notice from '#lib/ui/Notice.svelte';
	import ReadOnlyHint from '#lib/ui/ReadOnlyHint.svelte';
	import RowList from '#lib/ui/RowList.svelte';
	import SegmentedFilter from '#lib/ui/SegmentedFilter.svelte';
	import Select from '#lib/ui/Select.svelte';
	import ShowWhen from '#lib/ui/ShowWhen.svelte';
	import States from '#lib/ui/States.svelte';
	import Textarea from '#lib/ui/Textarea.svelte';
	import { confirm, prompt } from '#lib/ui/dialogs.svelte.ts';
	import { toast } from '#lib/ui/toasts.svelte.ts';
	import ChangesBar from '#lib/ui/settings/ChangesBar.svelte';
	import SettingRow from '#lib/ui/settings/SettingRow.svelte';
	import SettingsBody from '#lib/ui/settings/SettingsBody.svelte';
	import SettingsGroup from '#lib/ui/settings/SettingsGroup.svelte';
	import { SettingsFilter } from '#lib/ui/settings/filter.svelte.ts';
	import { AppearanceForm, applyToConsole } from '../appearance-form.svelte.ts';
	import {
		COLOR_GROUPS,
		brandingFromServer,
		brandingPayload,
		isHex,
		paletteColors,
		type ThemeMode
	} from '../appearance.ts';
	import BackgroundFields from '../BackgroundFields.svelte';
	import FontPicker from '../FontPicker.svelte';
	import RangeField from '../RangeField.svelte';
	import ThemePreview from '../ThemePreview.svelte';
	import { readabilityText } from '../readability.ts';

	const canEdit = can('branding.edit');
	const form = new AppearanceForm();
	const draft = form.draft;
	$effect(() => draft.track());
	const filter = new SettingsFilter();

	const v = $derived(form.values);
	const errText = (err: unknown, fallback = t('errors.server_error')) =>
		err instanceof Error && err.message ? err.message : fallback;
	type Line = { message: string; tone: 'neutral' | 'ok' | 'error' };
	const NO_LINE: Line = { message: '', tone: 'neutral' };
	const undo = $derived(tf('settings.appearance.undo_change', 'Undo change'));
	/** A row's "Changed" mark and its Undo */
	const edit = (field: string) => ({
		changed: draft.isChanged(field),
		onreset: draft.isChanged(field) ? () => draft.reset(field) : undefined,
		resetLabel: undo
	});
	const values = $derived(draft.values as Record<string, unknown>);
	const setValue = (field: string, value: unknown) =>
		((draft.values as Record<string, unknown>)[field] = value);

	/** An action that replaces the appearance: unsaved changes go, so ask first */
	async function mayDrop(): Promise<boolean> {
		if (!draft.dirty) return true;
		return confirm({
			title: tf('settings.page.discard_title', 'Discard unsaved changes?'),
			message: tf('settings.page.discard_message', 'The changes on this page are not saved yet.'),
			confirmLabel: tf('settings.page.discard_confirm', 'Discard changes'),
			danger: true
		});
	}

	/* ── Theme and colours ── */

	/** The palette the console shows for a mode (system follows this device) */
	function lookPalette(mode: ThemeMode | 'light' | 'dark'): Record<string, string> {
		const dark =
			mode === 'dark' ||
			(mode !== 'light' &&
				typeof window.matchMedia === 'function' &&
				window.matchMedia('(prefers-color-scheme: dark)').matches);
		return (dark ? theme.palettes.dark : theme.palettes.light) ?? {};
	}

	function usePalette(palette: Record<string, string>) {
		for (const [k, c] of Object.entries(paletteColors(palette))) setValue(`colors.${k}`, c);
	}

	function onMode(mode: ThemeMode) {
		const prev = v.themeMode;
		setValue('themeMode', mode);
		// Start the custom colours from what the console looks like now
		if (mode === 'custom' && prev !== 'custom' && form.saved.themeMode !== 'custom')
			usePalette(lookPalette(prev));
	}

	const previewPalette = $derived.by(() => {
		if (v.themeMode !== 'custom') return lookPalette(v.themeMode);
		const out: Record<string, string> = { ...lookPalette('light') };
		for (const [k, c] of Object.entries(v.colors)) if (isHex(c)) out[k] = c.trim();
		return out;
	});

	// Built-in themes: picking one changes nothing; Apply saves it at once
	let themes = $state.raw<BuiltinTheme[]>([]);
	let themesFailed = $state(false);
	let themePick = $state('');
	let themeBusy = $state(false);
	let themeSelect = $state<HTMLSelectElement | null>(null);

	async function loadThemes() {
		try {
			themes = await listThemes();
		} catch {
			themesFailed = true;
		}
	}

	async function applyPickedTheme() {
		if (!canEdit) return;
		const picked = themes.find((x) => x.id === themePick);
		if (!picked) {
			toast.error(t('settings.appearance.pick_theme_first'));
			themeSelect?.focus();
			return;
		}
		if (!(await mayDrop())) return;
		const ok = await confirm({
			title: t('settings.tab_branding'),
			message: t('branding.theme_apply_confirm'),
			confirmLabel: t('settings.appearance.apply_theme')
		});
		if (!ok) return;
		themeBusy = true;
		try {
			await applyBuiltinTheme(picked.id);
			await form.reloadFromServer();
			toast.success(t('branding.theme_applied'));
		} catch (err) {
			toast.error(errText(err));
		} finally {
			themeBusy = false;
		}
	}

	/* ── Fonts ── */

	let fontSource = $state<'google' | 'local' | 'upload'>('google');
	let fontCategory = $state('all');
	let localFonts = $state.raw<{ family: string }[] | null>(null);
	let localError = $state('');
	let fontUploadName = $state('');
	let fontFiles = $state<FileList | null>(null);
	let fontPicker = $state<HTMLInputElement | null>(null);

	async function loadLocalFonts() {
		try {
			localFonts = await listLocalFonts();
			localError = '';
		} catch (err) {
			localError = errText(err);
		}
	}

	async function removeFont(family: string) {
		try {
			await deleteFont(family);
		} catch (err) {
			toast.error(errText(err));
		}
		void loadLocalFonts();
	}

	async function onFontFile() {
		const file = fontFiles?.[0];
		if (!file || !canEdit) return;
		const family = fontUploadName.trim() || file.name.replace(/\.[^.]+$/, '');
		try {
			await uploadFont(file, family);
			toast.success(t('branding.font_upload_success'));
			void loadLocalFonts();
		} catch (err) {
			toast.error(errText(err));
		} finally {
			if (fontPicker) fontPicker.value = '';
		}
	}

	const FONT_CATEGORIES = $derived([
		{ value: 'all', label: t('branding.font_cat_all') },
		{ value: 'sans-serif', label: tf('settings.appearance.font_cat_sans', 'Sans-serif') },
		{ value: 'serif', label: tf('settings.appearance.font_cat_serif', 'Serif') },
		{ value: 'monospace', label: tf('settings.appearance.font_cat_mono', 'Monospace') },
		{ value: 'display', label: tf('settings.appearance.font_cat_display', 'Display') }
	]);

	/* ── Background pictures uploaded before ── */

	let library = $state.raw<BackgroundImage[] | null>(null);
	let libraryError = $state('');

	async function loadLibrary() {
		try {
			library = await listBackgrounds();
			libraryError = '';
		} catch (err) {
			libraryError = errText(err);
		}
	}
	// A picture uploaded here joins the list
	let uploadsBefore = 0;
	$effect(() => {
		const now = form.uploading;
		if (now < uploadsBefore) void loadLibrary();
		uploadsBefore = now;
	});

	function pickLibrary(url: string) {
		setValue('bgImageUrl', url);
		setValue('bgType', 'image');
	}

	const fileName = (url: string) => url.split('/').pop() || url;

	/* ── Custom CSS, behind Advanced ── */

	let advancedOpen = $state(false);
	// Open for custom CSS that is set, a refusal of it, and a filter that finds it;
	// never closed here, so a link or search that opened it keeps it open
	$effect(() => {
		if (
			String(values.customCss ?? '').trim() ||
			form.refusedAt('customCss') ||
			(filter.active && filter.shown.get('custom-css'))
		)
			advancedOpen = true;
	});

	/* ── Saved appearances ── */

	let profiles = $state.raw<BrandingProfile[]>([]);
	let profileId = $state('');
	let profileStatus = $state<Line>(NO_LINE);
	let profileBusy = $state(false);

	async function loadProfiles(selected = '') {
		try {
			profiles = await listProfiles();
			const active = profiles.find((p) => p.is_active);
			profileId = selected || (active ? String(active.id) : '');
		} catch (err) {
			profileStatus = { message: errText(err), tone: 'error' };
		}
	}

	async function runProfile(fn: () => Promise<void>, ok: string) {
		profileStatus = NO_LINE;
		profileBusy = true;
		try {
			await fn();
			profileStatus = { message: ok, tone: 'ok' };
		} catch (err) {
			profileStatus = { message: errText(err), tone: 'error' };
		} finally {
			profileBusy = false;
		}
	}

	function needProfile(): boolean {
		if (profileId) return true;
		profileStatus = { message: t('branding.profile_select_first'), tone: 'error' };
		return false;
	}

	async function applySelected() {
		if (!needProfile() || !(await mayDrop())) return;
		void runProfile(async () => {
			const r = await applyProfile(profileId);
			if (r.data) {
				const next = brandingFromServer(r.data);
				form.fill(next);
				applyToConsole(brandingPayload(next), r.revision);
			} else {
				await form.reloadFromServer(r.revision);
			}
			await loadProfiles(profileId);
		}, t('branding.profile_applied'));
	}

	const current = () => brandingPayload($state.snapshot(form.values));

	async function newProfile() {
		const name = await prompt({
			title: t('settings.tab_branding'),
			label: t('branding.profile_name_prompt'),
			confirmLabel: t('actions.save')
		});
		if (!name?.trim()) return;
		void runProfile(async () => {
			await createProfile(name.trim(), current());
			await loadProfiles();
		}, t('branding.profile_created'));
	}

	function saveIntoProfile() {
		if (!needProfile()) return;
		void runProfile(
			() => updateProfile(profileId, current()).then(() => undefined),
			t('branding.profile_updated')
		);
	}

	function duplicateSelected() {
		if (!needProfile()) return;
		void runProfile(async () => {
			const p = profiles.find((x) => String(x.id) === profileId);
			const copyName = p ? `${p.name ?? ''} (${t('branding.profile_duplicate')})` : '';
			const id = await duplicateProfile(profileId, copyName);
			await loadProfiles(id ?? '');
		}, t('branding.profile_duplicated'));
	}

	async function deleteSelected() {
		if (!needProfile()) return;
		const ok = await confirm({
			title: t('settings.tab_branding'),
			message: t('branding.profile_delete_confirm'),
			confirmLabel: t('branding.profile_delete'),
			danger: true
		});
		if (!ok) return;
		void runProfile(async () => {
			await deleteProfile(profileId);
			await loadProfiles();
		}, t('branding.profile_deleted'));
	}

	const profileOptions = $derived([
		{ value: '', label: t('branding.profile_none') },
		...profiles.map((p) => ({
			value: String(p.id),
			label: p.is_active ? `${p.name ?? ''} (${t('branding.profile_active')})` : (p.name ?? '')
		}))
	]);

	/* ── Import and export ── */

	let ieStatus = $state<Line>(NO_LINE);
	let importFiles = $state<FileList | null>(null);
	let importPicker = $state<HTMLInputElement | null>(null);

	async function exportNow() {
		try {
			const preset = await exportBranding();
			const blob = new Blob([JSON.stringify(preset, null, 2)], { type: 'application/json' });
			const url = URL.createObjectURL(blob);
			const a = document.createElement('a');
			a.href = url;
			a.download = 'neodesk-theme.json';
			a.click();
			setTimeout(() => URL.revokeObjectURL(url), 1000);
			ieStatus = { message: t('branding.exported'), tone: 'ok' };
		} catch (err) {
			ieStatus = { message: errText(err), tone: 'error' };
		}
	}

	async function importNow() {
		const file = importFiles?.[0];
		if (!file) return;
		try {
			if (!(await mayDrop())) return;
			let preset: unknown;
			try {
				preset = JSON.parse(await file.text());
			} catch {
				// Not the parser's own words: a file that is no JSON is no appearance file
				throw new Error(t('settings.appearance.refused_theme_file'));
			}
			await importBranding(preset);
			await form.reloadFromServer();
			ieStatus = { message: t('branding.imported'), tone: 'ok' };
		} catch (err) {
			ieStatus = { message: errText(err, t('branding.import_error')), tone: 'error' };
		} finally {
			if (importPicker) importPicker.value = '';
		}
	}

	/* ── Reset ── */

	let resetting = $state(false);

	async function resetAll() {
		const ok = await confirm({
			title: tf('settings.appearance.reset_confirm_title', 'Reset the appearance and branding?'),
			message: tf(
				'settings.appearance.reset_confirm',
				'Everyone gets the built-in look and name again. Your theme and colours, fonts, backgrounds, custom CSS, name and logo, footer, sign-in page and download page settings are removed, and unsaved changes here too. This cannot be undone. Saved appearances, uploaded pictures and fonts and the client app branding stay.'
			),
			confirmLabel: tf('settings.appearance.reset_confirm_button', 'Reset for everyone'),
			danger: true
		});
		if (!ok) return;
		resetting = true;
		try {
			await resetBranding();
			await form.reloadFromServer();
			toast.success(t('branding.reset_success'));
		} catch (err) {
			toast.error(errText(err));
		} finally {
			resetting = false;
		}
	}

	/* ── Readability ── */

	const readIssues = $derived(form.readability?.issues ?? []);
	const readText = $derived(readabilityText(readIssues));

	/* ── Start ── */

	$effect(() => {
		void form.load();
		void loadThemes();
		void loadLocalFonts();
		void loadLibrary();
		if (canEdit) void loadProfiles();
	});

	const sections = $derived([
		{ id: 'branding-theme', label: t('theme.title') },
		...(v.themeMode === 'custom'
			? [{ id: 'branding-custom-colors', label: t('settings.appearance.custom_colors') }]
			: []),
		{ id: 'branding-fonts', label: t('branding.typography_title') },
		{ id: 'branding-background', label: t('settings.appearance.console_background') },
		{ id: 'branding-css', label: tf('settings.appearance.advanced', 'Advanced') },
		...(canEdit
			? [
					{ id: 'branding-profiles', label: t('settings.appearance.profiles') },
					{ id: 'branding-import-export', label: t('branding.import_export_title') }
				]
			: [])
	]);
</script>

{#snippet preview()}
	<h2 class="preview-title" id="branding-preview-title">{t('settings.side.preview_title')}</h2>
	<p class="preview-note">{t('settings.side.preview_note')}</p>
	<ThemePreview
		palette={previewPalette}
		mode={v.themeMode}
		name={v.appName}
		logoType={v.logoType}
		logoSrc={v.logoUrl}
		fontHeading={v.fontHeading}
		fontBody={v.fontBody}
	/>
{/snippet}

{#snippet top()}
	<ReadOnlyHint writes={['branding.edit']} />
	{#if readText}
		<Notice
			tone={readIssues.some((i) => i.severity === 'error') ? 'danger' : 'warning'}
			id="branding-readability-alert"
			title={t('branding.readability_check')}
			message={readText}
		/>
	{/if}
	{#if form.loadError}<States kind="error" message={form.loadError} onretry={() => void form.load()} />{/if}
	<p class="applies">{t('settings.appearance.save_effect')}</p>
{/snippet}

{#snippet bar()}
	{#if canEdit}<ChangesBar {draft} onsave={() => form.save()} />{/if}
{/snippet}

{#snippet bgLibrary()}
	<SettingRow
		id="bg-image-library"
		label={t('desktop.wp_images')}
		help={tf(
			'settings.appearance.library_help',
			'Pictures uploaded to this server before. Pick one to use it.'
		)}
		group
	>
		{#snippet control(f)}
			<div
				class="background-library"
				role="group"
				id={f.id}
				aria-labelledby={f.labelledby}
				aria-describedby={f.describedby}
			>
				{#if libraryError}
					<p class="state is-error">{libraryError}</p>
				{:else if !library}
					<States kind="loading" />
				{:else if !library.length}
					<p class="state">{t('desktop.wp_unavailable')}</p>
				{:else}
					{#each library as item (item.url)}
						{@const url = item.url ?? ''}
						<button
							type="button"
							class={['library-item', v.bgImageUrl === url && 'active']}
							aria-pressed={v.bgImageUrl === url}
							disabled={!canEdit}
							onclick={() => pickLibrary(url)}
						>
							<img class="library-thumb" src={url} alt="" loading="lazy" />
							<span class="library-name">{item.name || fileName(url)}</span>
						</button>
					{/each}
				{/if}
			</div>
		{/snippet}
	</SettingRow>
{/snippet}

<SettingsBody
	{filter}
	filterLabel={tf('settings.appearance.filter', 'Filter appearance settings')}
	{sections}
	summary={preview}
	summaryLabel={t('settings.side.preview_title')}
	summaryLast
	{top}
	{bar}
	class="appearance-tab"
>
	<fieldset class="branding-form" disabled={!canEdit} aria-busy={!form.loaded}>
		<legend class="sr-only">{t('settings.tab_branding')}</legend>

		<SettingsGroup
			id="branding-theme"
			title={t('theme.title')}
			description={t('settings.appearance.theme_note')}
		>
			<SettingRow
				id="theme-mode-selector"
				label={tf('settings.appearance.theme_default', 'Default theme')}
				help={tf(
					'settings.appearance.theme_default_help',
					'What everyone sees, unless they pick light or dark for themselves in the top bar.'
				)}
				group
				{...edit('themeMode')}
			>
				{#snippet control(f)}
					<div id={f.id}>
						<ChoiceList
							name="theme-mode"
							labelledby={f.labelledby}
							describedby={f.describedby}
							disabled={!canEdit}
							bind:value={() => v.themeMode, (mode) => onMode(mode)}
							choices={[
								{
									value: 'system',
									title: t('theme.auto'),
									description: t('settings.appearance.mode_system')
								},
								{ value: 'light', title: t('theme.light'), description: t('settings.appearance.mode_light') },
								{ value: 'dark', title: t('theme.dark'), description: t('settings.appearance.mode_dark') },
								{
									value: 'custom',
									title: t('theme.custom'),
									description: t('settings.appearance.mode_custom'),
									controls: 'branding-custom-colors'
								}
							]}
						/>
					</div>
				{/snippet}
			</SettingRow>
			<SettingRow
				id="branding-theme-select"
				label={t('branding.builtin_themes')}
				help="{t('settings.appearance.builtin_hint')} {t('settings.appearance.builtin_effect')}"
			>
				{#snippet control(f)}
					<div class="inline-controls" id="branding-builtin-themes">
						<Select
							id={f.id}
							bind:element={themeSelect}
							bind:value={themePick}
							disabled={themesFailed || !canEdit}
							describedby={f.describedby}
							options={[
								{ value: '', label: t('settings.appearance.pick_theme') },
								...themes.map((x) => ({ value: x.id, label: x.name || x.id }))
							]}
						/>
						<Button id="branding-theme-apply" busy={themeBusy} onclick={() => void applyPickedTheme()}
							>{t('settings.appearance.apply_theme')}</Button
						>
					</div>
				{/snippet}
			</SettingRow>
		</SettingsGroup>

		<ShowWhen id="branding-custom-colors" when={v.themeMode === 'custom'}>
			<SettingsGroup
				id="branding-colors"
				title={t('settings.appearance.custom_colors')}
				description={t('settings.appearance.custom_colors_note')}
			>
				{#snippet actions()}
					{#if canEdit}
						<Button size="sm" onclick={() => usePalette(lookPalette('light'))}
							>{t('settings.appearance.start_light')}</Button
						>
						<Button size="sm" onclick={() => usePalette(lookPalette('dark'))}
							>{t('settings.appearance.start_dark')}</Button
						>
					{/if}
				{/snippet}
				{#each COLOR_GROUPS as group (group.legend)}
					{#if !filter.active}<h3 class="color-legend">{t(group.legend)}</h3>{/if}
					<div class="color-rows">
						{#each group.colors as [key, labelKey] (key)}
							{@const field = `colors.${key}`}
							<SettingRow
								id="color-{key}"
								label={t(`settings.appearance.${labelKey}`)}
								error={form.refusedAt(field)}
								{...edit(field)}
							>
								{#snippet control(f)}
									<ColorField
										id={f.id}
										describedby={f.describedby}
										invalid={f.invalid}
										disabled={!canEdit}
										label={t(`settings.appearance.${labelKey}`)}
										bind:value={() => String(values[field] ?? ''), (c) => setValue(field, c)}
									/>
								{/snippet}
							</SettingRow>
						{/each}
					</div>
				{/each}
			</SettingsGroup>
		</ShowWhen>

		<SettingsGroup
			id="branding-fonts"
			title={t('branding.typography_title')}
			description={t('branding.typography_desc')}
		>
			{#snippet note()}
				<div class="font-sources" data-search-skip>
					<div class="font-source" id="font-source">
						<span class="font-source-label" id="font-source-label"
							>{t('settings.appearance.font_source')}</span
						>
						<SegmentedFilter
							label={t('settings.appearance.font_source')}
							bind:value={fontSource}
							options={[
								{ value: 'google', label: t('branding.font_google') },
								{ value: 'local', label: t('branding.font_local') },
								{ value: 'upload', label: t('branding.font_upload') }
							]}
						/>
					</div>
					{#if fontSource === 'google'}
						<div id="font-source-google">
							<SegmentedFilter
								label={t('settings.appearance.font_category')}
								bind:value={fontCategory}
								options={FONT_CATEGORIES}
							/>
						</div>
					{:else if fontSource === 'local'}
						<div id="font-source-local">
							{#if localError}
								<p class="state is-error">{localError}</p>
							{:else if localFonts && !localFonts.length}
								<RowList><ListRow empty title={t('branding.font_local_empty')} /></RowList>
							{:else if localFonts}
								<RowList label={t('branding.font_local')}>
									{#each localFonts as font (font.family)}
										<ListRow title={font.family}>
											{#snippet actions()}
												{#if canEdit}
													<Button
														size="sm"
														variant="danger-quiet"
														onclick={() => void removeFont(font.family)}>{t('branding.font_delete')}</Button
													>
												{/if}
											{/snippet}
										</ListRow>
									{/each}
								</RowList>
							{/if}
						</div>
					{:else}
						<div id="font-source-upload" class="font-upload">
							<Field id="font-upload-name" label={t('branding.font_upload_name')}>
								{#snippet children(f)}<Input {...f} maxlength={80} bind:value={fontUploadName} />{/snippet}
							</Field>
							<div class="form-group">
								<label class="form-label" for="font-upload-file">{t('branding.font_upload_choose')}</label>
								<FilePicker
									id="font-upload-file"
									accept=".woff2,.ttf,font/woff2,font/ttf"
									bind:files={fontFiles}
									bind:element={fontPicker}
									describedby="font-upload-file-effect"
									onchange={() => void onFontFile()}
								/>
								<p class="form-hint" id="font-upload-file-effect">
									{t('settings.appearance.font_upload_effect')}
								</p>
							</div>
						</div>
					{/if}
					<CountLine class="font-local-info">
						<strong id="font-local-count">{localFonts?.length ?? 0}</strong>
						{t('branding.font_downloaded_count')}
					</CountLine>
				</div>
			{/snippet}
			<SettingRow
				id="font-heading-search"
				label={t('branding.font_heading')}
				help={t('branding.font_heading_hint')}
				{...edit('fontHeading')}
			>
				{#snippet control(f)}
					<FontPicker
						slot="heading"
						bare
						inputId={f.id}
						describedby={f.describedby}
						bind:value={() => String(values.fontHeading ?? ''), (font) => setValue('fontHeading', font)}
						source={fontSource === 'google' ? 'google' : 'local'}
						category={fontCategory === 'all' ? '' : fontCategory}
						{canEdit}
						disabled={!canEdit}
						ondownload={() => void loadLocalFonts()}
					/>
				{/snippet}
			</SettingRow>
			<SettingRow
				id="font-body-search"
				label={t('branding.font_body')}
				help={t('branding.font_body_hint')}
				{...edit('fontBody')}
			>
				{#snippet control(f)}
					<FontPicker
						slot="body"
						bare
						inputId={f.id}
						describedby={f.describedby}
						bind:value={() => String(values.fontBody ?? ''), (font) => setValue('fontBody', font)}
						source={fontSource === 'google' ? 'google' : 'local'}
						category={fontCategory === 'all' ? '' : fontCategory}
						{canEdit}
						disabled={!canEdit}
						ondownload={() => void loadLocalFonts()}
					/>
				{/snippet}
			</SettingRow>
		</SettingsGroup>

		<SettingsGroup
			id="branding-background"
			title={t('settings.appearance.console_background')}
			description={t('branding.background_desc')}
		>
			<BackgroundFields
				{form}
				prefix="bg"
				field="bg"
				types={['none', 'color', 'gradient', 'image']}
				typeHelp={tf('settings.appearance.bg_type_help', 'What shows behind the console pages.')}
				showSize
				library={bgLibrary}
				effects={['bg-effects-group', 'bg-overlay-group']}
				disabled={!canEdit}
			>
				<ShowWhen id="bg-effects-group" when={v.bgType !== 'none'}>
					<SettingRow
						id="bg-blur"
						label={t('branding.bg_blur')}
						help={tf('settings.appearance.blur_help', 'Blurs the background so the pages stand out.')}
						{...edit('bgBlur')}
					>
						{#snippet control(f)}<RangeField
								{...f}
								unit="px"
								min={0}
								max={30}
								disabled={!canEdit}
								bind:value={() => Number(values.bgBlur ?? 0), (n) => setValue('bgBlur', n)}
							/>{/snippet}
					</SettingRow>
				</ShowWhen>
				<ShowWhen id="bg-overlay-group" when={v.bgType !== 'none'}>
					<SettingRow
						id="bg-overlay"
						label={t('branding.bg_overlay')}
						help={t('branding.bg_overlay_hint')}
						{...edit('bgOverlay')}
					>
						{#snippet control(f)}<RangeField
								{...f}
								unit="%"
								min={0}
								max={90}
								step={5}
								disabled={!canEdit}
								bind:value={() => Number(values.bgOverlay ?? 0), (n) => setValue('bgOverlay', n)}
							/>{/snippet}
					</SettingRow>
				</ShowWhen>
			</BackgroundFields>
		</SettingsGroup>

		<div class="advanced" id="branding-css" hidden={filter.active && !filter.shown.get('custom-css')}>
			<Disclosure title={tf('settings.appearance.advanced', 'Advanced')} bind:open={advancedOpen}>
				<div class="advanced-body">
					<p class="advanced-note">
						{tf(
							'settings.appearance.advanced_note',
							'Settings most consoles never change. A wrong value can spoil how the console and the sign-in page look.'
						)}
					</p>
					<div class="advanced-rows">
						<SettingRow
							id="custom-css"
							label={t('branding.custom_css_title')}
							help="{t('branding.custom_css_desc')} {t('branding.custom_css_hint')}"
							error={form.refusedAt('customCss')}
							{...edit('customCss')}
						>
							{#snippet control(f)}
								<Textarea
									id={f.id}
									describedby={f.describedby}
									invalid={f.invalid}
									disabled={!canEdit}
									class="mono"
									rows={10}
									spellcheck={false}
									bind:value={() => String(values.customCss ?? ''), (css) => setValue('customCss', css)}
								/>
							{/snippet}
						</SettingRow>
					</div>
				</div>
			</Disclosure>
		</div>
	</fieldset>

	{#if canEdit}
		<!-- Actions, no settings: out of the way while the filter looks for settings -->
		<div class="actions-part" hidden={filter.active}>
			<SettingsGroup
				id="branding-profiles"
				title={t('settings.appearance.profiles')}
				description={t('settings.appearance.profiles_note')}
				consequence={t('settings.appearance.profile_effect')}
			>
				<div class="action-block">
					<label class="form-label" for="branding-profile-select">{t('branding.profiles_label')}</label>
					<div class="inline-controls">
						<Select id="branding-profile-select" bind:value={profileId} options={profileOptions} />
						<Button id="branding-profile-apply-btn" busy={profileBusy} onclick={() => void applySelected()}
							>{t('branding.profile_apply')}</Button
						>
						<MenuButton label={t('settings.appearance.profile_more')}>
							<MenuItem icon="add" onselect={() => void newProfile()}>{t('branding.profile_new')}</MenuItem>
							<MenuItem icon="save" onselect={saveIntoProfile}>{t('branding.profile_save')}</MenuItem>
							<MenuItem icon="duplicate" onselect={duplicateSelected}
								>{t('branding.profile_duplicate')}</MenuItem
							>
							<MenuDivider />
							<MenuItem icon="delete" danger onselect={() => void deleteSelected()}
								>{t('branding.profile_delete')}</MenuItem
							>
						</MenuButton>
					</div>
					<FormStatus message={profileStatus.message} tone={profileStatus.tone} class="line-status" />
				</div>
			</SettingsGroup>

			<SettingsGroup
				id="branding-import-export"
				title={t('branding.import_export_title')}
				description={t('branding.import_export_desc')}
				consequence={t('settings.appearance.import_effect')}
			>
				<div class="action-block">
					<p class="line">
						<Button icon="export" id="branding-export-btn" onclick={() => void exportNow()}
							>{t('branding.export_theme')}</Button
						>
					</p>
					<label class="form-label" for="branding-import-input">{t('branding.import_theme')}</label>
					<FilePicker
						id="branding-import-input"
						accept=".json"
						bind:files={importFiles}
						bind:element={importPicker}
						onchange={() => void importNow()}
					/>
					<FormStatus message={ieStatus.message} tone={ieStatus.tone} class="line-status" />
				</div>
			</SettingsGroup>

			<DangerZone
				title={tf('settings.appearance.danger_zone', 'Danger zone')}
				rows={[
					{
						title: tf('settings.appearance.reset_row', 'Reset appearance and branding'),
						consequence: tf(
							'settings.appearance.reset_removes',
							'Removes your theme and colours, fonts, backgrounds, custom CSS, name and logo, footer, sign-in page and download page settings for everyone. Saved appearances, uploaded pictures and fonts and the client app branding stay.'
						),
						action: t('settings.appearance.reset_button'),
						onclick: () => void resetAll(),
						busy: resetting
					}
				]}
			/>
		</div>
	{/if}
</SettingsBody>

<style>
	/* A changed row is tinted, and so is the picked theme: muted text would lose its contrast on both */
	:global(.appearance-tab .setting-row.is-changed .choice-desc) {
		color: var(--text);
	}
	.branding-form {
		display: flex;
		flex-direction: column;
		gap: var(--space-6);
		min-width: 0;
		margin: 0;
		padding: 0;
		border: 0;
	}
	.applies,
	.preview-note,
	.advanced-note {
		margin: 0;
		max-width: var(--measure);
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	.preview-title {
		margin: 0 0 var(--space-1);
		font-size: var(--text-md);
	}
	.preview-note {
		margin-bottom: var(--space-3);
	}
	.inline-controls {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-2);
	}
	.inline-controls > :global(.form-select) {
		flex: 1 1 220px;
		width: auto;
	}
	.color-legend {
		margin: var(--space-4) 0 0;
		padding: 0 var(--row-pad-x) var(--space-1);
		font-size: var(--text-sm);
		font-weight: 600;
		color: var(--text-muted);
	}
	.color-rows {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(min(100%, 260px), 1fr));
	}
	.font-sources {
		display: flex;
		flex-direction: column;
		gap: var(--space-2);
		margin-bottom: var(--space-2);
	}
	.font-source {
		display: flex;
		flex-direction: column;
		gap: var(--space-1);
	}
	.font-source-label,
	.form-label {
		font-weight: 600;
	}
	.font-upload {
		display: flex;
		flex-direction: column;
		gap: var(--space-3);
	}
	.form-group {
		display: flex;
		flex-direction: column;
		gap: var(--space-1);
		margin: 0;
	}
	.form-hint {
		margin: 0;
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	.background-library {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(120px, 1fr));
		gap: var(--space-2);
	}
	.library-item {
		display: flex;
		flex-direction: column;
		gap: var(--space-1);
		padding: var(--space-1);
		border: 1px solid var(--rule);
		border-radius: var(--radius);
		background: var(--bg);
		color: var(--text);
		font: inherit;
		font-size: var(--text-sm);
		text-align: start;
		cursor: pointer;
	}
	.library-item:hover {
		border-color: var(--border);
	}
	.library-item.active {
		border-color: var(--accent);
		outline: 1px solid var(--accent);
	}
	.library-item:focus-visible {
		outline: var(--focus-ring);
		outline-offset: var(--focus-offset);
	}
	.library-thumb {
		display: block;
		width: 100%;
		aspect-ratio: 16 / 10;
		border-radius: 2px;
		background: var(--bg-subtle);
		object-fit: cover;
	}
	.library-name {
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.advanced-body {
		display: flex;
		flex-direction: column;
		gap: var(--space-4);
		padding: var(--space-3) 0 var(--space-2);
	}
	.action-block {
		display: flex;
		flex-direction: column;
		gap: var(--space-2);
		padding: var(--space-3) var(--row-pad-x);
	}
	.actions-part {
		display: flex;
		flex-direction: column;
		gap: var(--space-6);
	}
	.actions-part[hidden],
	.advanced[hidden] {
		display: none;
	}
	.advanced-rows {
		margin: 0 calc(-1 * var(--row-pad-x));
		border-top: 1px solid var(--rule);
	}
	.line {
		margin: 0 0 var(--space-2);
	}
	.state {
		margin: 0;
		color: var(--text-muted);
	}
	.state.is-error {
		color: var(--danger);
	}
	.action-block :global(.line-status:empty) {
		display: none;
	}
</style>
