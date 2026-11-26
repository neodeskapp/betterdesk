<!--
  Settings › Branding (?tab=brand): your name and logo, the footer, the
  sign-in page with its slides and background, the download page, the
  remote viewer's background, and the client app (ClientAppBranding). One
  save model (PATTERN.md rule 1): every change, the client app's too,
  collects in the save bar and goes out with Save. The appearance part is
  saved whole with the Appearance tab's settings (AppearanceForm), the
  client app's to its own address. The preview shows name and logo before
  they are saved.

  Everyone may look; branding.edit may change (the rows are read-only
  otherwise, and the client app is left out). Element ids are the old
  page's, for links and the settings search.
-->
<script lang="ts">
	import { t, tf } from '#lib/i18n/i18n.svelte.ts';
	import { can } from '#lib/state/session.svelte.ts';
	import { theme } from '#lib/state/theme.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import Field from '#lib/ui/Field.svelte';
	import FilePicker from '#lib/ui/FilePicker.svelte';
	import Input from '#lib/ui/Input.svelte';
	import Notice from '#lib/ui/Notice.svelte';
	import Radio from '#lib/ui/Radio.svelte';
	import ReadOnlyHint from '#lib/ui/ReadOnlyHint.svelte';
	import Select from '#lib/ui/Select.svelte';
	import ShowWhen from '#lib/ui/ShowWhen.svelte';
	import States from '#lib/ui/States.svelte';
	import Textarea from '#lib/ui/Textarea.svelte';
	import { toast } from '#lib/ui/toasts.svelte.ts';
	import ChangesBar from '#lib/ui/settings/ChangesBar.svelte';
	import SettingRow from '#lib/ui/settings/SettingRow.svelte';
	import SettingsBody from '#lib/ui/settings/SettingsBody.svelte';
	import SettingsGroup from '#lib/ui/settings/SettingsGroup.svelte';
	import { SettingsFilter } from '#lib/ui/settings/filter.svelte.ts';
	import { AppearanceForm } from '../appearance-form.svelte.ts';
	import {
		DEFAULT_SLIDES,
		LOGO_ICONS,
		LOGO_MAX,
		LOGO_TYPES,
		MAX_SLIDES,
		fileProblem,
		isHex,
		moveSlide,
		type LogoType,
		type PromoMode
	} from '../appearance.ts';
	import BackgroundFields from '../BackgroundFields.svelte';
	import ClientAppBranding from '../ClientAppBranding.svelte';
	import RangeField from '../RangeField.svelte';
	import ThemePreview from '../ThemePreview.svelte';
	import { readabilityText } from '../readability.ts';

	const canEdit = can('branding.edit');
	const form = new AppearanceForm();
	const draft = form.draft;
	$effect(() => draft.track());
	const filter = new SettingsFilter();

	const v = $derived(form.values);
	const values = $derived(draft.values as Record<string, unknown>);
	const setValue = (field: string, value: unknown) =>
		((draft.values as Record<string, unknown>)[field] = value);
	const text = (field: string) => String(values[field] ?? '');
	const undo = $derived(tf('settings.appearance.undo_change', 'Undo change'));
	const edit = (field: string) => ({
		changed: draft.isChanged(field),
		onreset: draft.isChanged(field) ? () => draft.reset(field) : undefined,
		resetLabel: undo
	});

	/* ── Logo ── */

	const LOGO_TYPES_LIST: readonly LogoType[] = ['default', 'text', 'icon', 'svg', 'image'];
	const LOGO_LABEL: Record<LogoType, string> = {
		default: 'branding.logo_type_default',
		text: 'branding.logo_type_text',
		icon: 'branding.logo_type_icon',
		svg: 'branding.logo_type_svg',
		image: 'branding.logo_type_image'
	};

	let logoFiles = $state<FileList | null>(null);
	let logoPicker = $state<HTMLInputElement | null>(null);
	let logoProblem = $state('');

	// Picking a file only remembers it; Save uploads it, so the current logo stays until then
	function onLogoFile() {
		const file = logoFiles?.[0];
		logoProblem = '';
		if (!file) {
			form.pickLogo(null);
			return;
		}
		const problem = fileProblem(file, LOGO_TYPES, LOGO_MAX);
		if (problem) {
			logoProblem = t(
				problem === 'size' ? 'branding.logo_image_too_large' : 'branding.logo_image_invalid_type'
			);
			form.pickLogo(null);
			if (logoPicker) logoPicker.value = '';
			return;
		}
		form.pickLogo(file);
	}

	// Discard or a save took the waiting file away: so does the picker
	$effect(() => {
		if (!form.pendingLogo && logoPicker) logoPicker.value = '';
	});

	let pendingLogoUrl = $state('');
	$effect(() => {
		const file = form.pendingLogo;
		if (!file || !file.type.startsWith('image/')) {
			pendingLogoUrl = '';
			return;
		}
		const url = URL.createObjectURL(file);
		pendingLogoUrl = url;
		return () => URL.revokeObjectURL(url);
	});

	/* ── Sign-in page slides ── */

	const slides = $derived(draft.values.loginSlides);

	function addSlide() {
		if (slides.length >= MAX_SLIDES) return;
		draft.values.loginSlides = [
			...slides,
			{ id: `slide-${Date.now().toString(36)}`, slideKey: '', imageUrl: '', title: '', body: '', overlay: 45 }
		];
	}

	function restoreSlides() {
		draft.values.loginSlides = DEFAULT_SLIDES.map((x) => ({ ...x }));
	}

	function move(i: number, by: -1 | 1) {
		draft.values.loginSlides = moveSlide($state.snapshot(draft.values.loginSlides), i, by);
		const to = i + by;
		queueMicrotask(() => {
			const btn = document.getElementById(
				`login-slide-${to}-${by < 0 ? 'up' : 'down'}`
			) as HTMLButtonElement | null;
			const other = document.getElementById(`login-slide-${to}-${by < 0 ? 'down' : 'up'}`);
			(btn && !btn.disabled ? btn : other)?.focus();
		});
	}

	function removeSlide(i: number) {
		if (slides.length <= 1) return;
		draft.values.loginSlides = slides.filter((_x, j) => j !== i);
	}

	async function onSlideFile(i: number, input: HTMLInputElement) {
		const file = input.files?.[0];
		if (!file) return;
		try {
			await form.uploadSlide(file, i);
		} catch (err) {
			toast.error(err instanceof Error && err.message ? err.message : t('errors.server_error'));
		} finally {
			input.value = '';
		}
	}

	/** What a slide shows when its title or text is empty: the built-in slide's words */
	const builtIn = (key: string, part: 'title' | 'body') => {
		const k = `login_promo.slides.${key}.${part}`;
		const word = key ? t(k) : '';
		return word && word !== k ? word : '';
	};

	const PROMO: { value: PromoMode; label: string }[] = [
		{ value: 'classic', label: 'branding.login_promo_classic' },
		{ value: 'single', label: 'branding.login_promo_single' },
		{ value: 'slides', label: 'branding.login_promo_slides' }
	];

	/* ── Preview ── */

	const previewPalette = $derived.by(() => {
		const dark =
			v.themeMode === 'dark' ||
			(v.themeMode !== 'light' &&
				typeof window.matchMedia === 'function' &&
				window.matchMedia('(prefers-color-scheme: dark)').matches);
		const out: Record<string, string> = { ...((dark ? theme.palettes.dark : theme.palettes.light) ?? {}) };
		if (v.themeMode === 'custom')
			for (const [k, c] of Object.entries(v.colors)) if (isHex(c)) out[k] = c.trim();
		return out;
	});

	const readIssues = $derived(form.readability?.issues ?? []);
	const readText = $derived(readabilityText(readIssues));

	$effect(() => {
		void form.load();
	});

	const sections = $derived([
		{ id: 'branding-identity', label: t('settings.appearance.identity') },
		{ id: 'branding-footer', label: t('branding.footer_title') },
		{ id: 'branding-login', label: t('branding.login_title') },
		{ id: 'branding-agent', label: t('branding.agent_title') },
		{ id: 'branding-rdclient', label: t('settings.appearance.rdclient') },
		...(canEdit ? [{ id: 'client-app', label: t('settings.branding.client_app') }] : [])
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
		logoSrc={pendingLogoUrl || v.logoUrl}
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

<SettingsBody
	{filter}
	filterLabel={tf('settings.brand.filter', 'Filter branding settings')}
	{sections}
	summary={preview}
	summaryLabel={t('settings.side.preview_title')}
	summaryLast
	{top}
	{bar}
	class="branding-tab"
>
	<fieldset class="branding-form" disabled={!canEdit} aria-busy={!form.loaded}>
		<legend class="sr-only">{t('settings.tab_brand')}</legend>

		<SettingsGroup
			id="branding-identity"
			title={t('settings.appearance.identity')}
			description={t('branding.identity_desc')}
		>
			<SettingRow
				id="brand-name"
				label={t('branding.app_name')}
				help={tf(
					'settings.brand.name_help',
					'Shown in the sidebar, the browser tab and on the sign-in page. Empty uses NeoDesk.'
				)}
				{...edit('appName')}
			>
				{#snippet control(f)}<Input
						id={f.id}
						describedby={f.describedby}
						maxlength={50}
						bind:value={() => text('appName'), (x) => setValue('appName', x)}
					/>{/snippet}
			</SettingRow>
			<SettingRow
				id="logo-type"
				label={t('branding.logo_title')}
				help={t('branding.logo_desc')}
				group
				{...edit('logoType')}
			>
				{#snippet control(f)}
					<div
						class="radio-row"
						role="radiogroup"
						id={f.id}
						aria-labelledby={f.labelledby}
						aria-describedby={f.describedby}
					>
						{#each LOGO_TYPES_LIST as kind (kind)}
							<Radio
								name="logo-type"
								value={kind}
								group={v.logoType}
								onchange={() => setValue('logoType', kind)}
								label={t(LOGO_LABEL[kind])}
								aria-controls={kind === 'default' ? undefined : `logo-${kind}-panel`}
							/>
						{/each}
					</div>
				{/snippet}
			</SettingRow>
			<ShowWhen id="logo-text-panel" when={v.logoType === 'text'}>
				<SettingRow
					id="logo-text-input"
					label={t('branding.logo_text')}
					help={t('branding.logo_text_hint')}
					error={form.refusedAt('logoText')}
					{...edit('logoText')}
				>
					{#snippet control(f)}<Input
							id={f.id}
							describedby={f.describedby}
							invalid={f.invalid}
							maxlength={30}
							bind:value={() => text('logoText'), (x) => setValue('logoText', x)}
						/>{/snippet}
				</SettingRow>
				<SettingRow
					id="logo-text-accent"
					label={t('branding.logo_text_accent')}
					help={t('branding.logo_text_accent_hint')}
					{...edit('logoTextAccent')}
				>
					{#snippet control(f)}<Input
							id={f.id}
							describedby={f.describedby}
							maxlength={30}
							bind:value={() => text('logoTextAccent'), (x) => setValue('logoTextAccent', x)}
						/>{/snippet}
				</SettingRow>
			</ShowWhen>
			<ShowWhen id="logo-icon-panel" when={v.logoType === 'icon'}>
				<SettingRow
					id="logo-icon-name"
					label={t('branding.logo_icon_name')}
					help={t('branding.logo_icon_hint')}
					error={form.refusedAt('logoIcon')}
					{...edit('logoIcon')}
				>
					{#snippet control(f)}<Select
							id={f.id}
							describedby={f.describedby}
							invalid={f.invalid}
							bind:value={() => text('logoIcon'), (x) => setValue('logoIcon', x)}
							options={[
								// A stored name the icon font lacks stays visible, so the refusal can name it
								...(LOGO_ICONS.includes(v.logoIcon) ? [] : [{ value: v.logoIcon, label: v.logoIcon }]),
								...LOGO_ICONS.map((name) => ({ value: name, label: name }))
							]}
						/>{/snippet}
				</SettingRow>
			</ShowWhen>
			<ShowWhen id="logo-svg-panel" when={v.logoType === 'svg'}>
				<SettingRow
					id="logo-svg-input"
					label={t('branding.logo_svg_code')}
					help={t('branding.logo_svg_hint')}
					error={form.refusedAt('logoSvg')}
					{...edit('logoSvg')}
				>
					{#snippet control(f)}<Textarea
							id={f.id}
							describedby={f.describedby}
							invalid={f.invalid}
							class="mono"
							rows={6}
							bind:value={() => text('logoSvg'), (x) => setValue('logoSvg', x)}
						/>{/snippet}
				</SettingRow>
			</ShowWhen>
			<ShowWhen id="logo-image-panel" when={v.logoType === 'image'}>
				<SettingRow
					id="logo-image-file"
					label={t('branding.logo_image_upload')}
					help="{t('branding.logo_image_hint')} {t('settings.appearance.logo_upload_effect')}"
					error={logoProblem || undefined}
					{...edit('logoUpload')}
				>
					{#snippet control(f)}
						<FilePicker
							id={f.id}
							accept="image/png,image/jpeg,image/gif,image/webp,image/svg+xml"
							bind:files={logoFiles}
							bind:element={logoPicker}
							describedby={f.describedby}
							onchange={onLogoFile}
						/>
						{#if form.pendingLogo}<p class="row-line" id="logo-file-name">
								{t('settings.appearance.logo_pending', { name: form.pendingLogo.name })}
							</p>{/if}
					{/snippet}
				</SettingRow>
				<SettingRow
					id="logo-image-url"
					label={t('branding.logo_image_url')}
					help={t('branding.logo_image_url_hint')}
					error={form.refusedAt('logoUrl')}
					{...edit('logoUrl')}
				>
					{#snippet control(f)}<Input
							id={f.id}
							describedby={f.describedby}
							invalid={f.invalid}
							bind:value={() => text('logoUrl'), (x) => setValue('logoUrl', x)}
						/>{/snippet}
				</SettingRow>
			</ShowWhen>
		</SettingsGroup>

		<SettingsGroup
			id="branding-footer"
			title={t('branding.footer_title')}
			description={t('branding.footer_desc')}
		>
			<SettingRow
				id="footer-text"
				label={t('branding.footer_text')}
				help={tf(
					'settings.brand.footer_help',
					'For example a copyright notice or your company name. Empty shows no text.'
				)}
				{...edit('footerText')}
			>
				{#snippet control(f)}<Input
						id={f.id}
						describedby={f.describedby}
						maxlength={200}
						bind:value={() => text('footerText'), (x) => setValue('footerText', x)}
					/>{/snippet}
			</SettingRow>
			<SettingRow
				id="show-powered"
				label={t('branding.show_powered')}
				help={t('branding.show_powered_hint')}
				checked={v.showPoweredBy}
				onchange={(on) => setValue('showPoweredBy', on)}
				disabled={!canEdit}
				{...edit('showPoweredBy')}
			/>
		</SettingsGroup>

		<SettingsGroup
			id="branding-login"
			title={t('branding.login_title')}
			description={t('branding.login_desc')}
		>
			<SettingRow
				id="login-title"
				label={t('branding.login_heading')}
				help={tf(
					'settings.brand.login_title_help',
					'The big line on the sign-in page. Empty shows "{text}".',
					{
						text: t('auth.login_title')
					}
				)}
				{...edit('loginTitle')}
			>
				{#snippet control(f)}<Input
						id={f.id}
						describedby={f.describedby}
						maxlength={80}
						bind:value={() => text('loginTitle'), (x) => setValue('loginTitle', x)}
					/>{/snippet}
			</SettingRow>
			<SettingRow
				id="login-subtitle"
				label={t('branding.login_subheading')}
				help={tf('settings.brand.login_subtitle_help', 'A line under the heading. Empty shows none.')}
				{...edit('loginSubtitle')}
			>
				{#snippet control(f)}<Input
						id={f.id}
						describedby={f.describedby}
						maxlength={160}
						bind:value={() => text('loginSubtitle'), (x) => setValue('loginSubtitle', x)}
					/>{/snippet}
			</SettingRow>
			<SettingRow
				id="login-promo-mode-selector"
				label={t('branding.login_promo_mode')}
				help={t('branding.login_promo_mode_hint')}
				group
				{...edit('loginPromoMode')}
			>
				{#snippet control(f)}
					<div
						class="radio-row"
						role="radiogroup"
						id={f.id}
						aria-labelledby={f.labelledby}
						aria-describedby={f.describedby}
					>
						{#each PROMO as mode (mode.value)}
							<Radio
								name="login-promo-mode"
								value={mode.value}
								group={v.loginPromoMode}
								onchange={() => setValue('loginPromoMode', mode.value)}
								label={t(mode.label)}
								aria-controls={mode.value === 'slides' ? 'login-slides-panel' : 'login-bg-config'}
							/>
						{/each}
					</div>
				{/snippet}
			</SettingRow>
			<ShowWhen id="login-slides-panel" when={v.loginPromoMode === 'slides'}>
				<SettingRow
					id="login-slides-list"
					label={t('branding.login_slides_title')}
					help="{t('branding.login_slides_desc')} {t('branding.login_slides_max')}"
					group
					{...edit('loginSlides')}
				>
					{#snippet control(f)}
						<ol
							class="slides"
							role="list"
							id={f.id}
							aria-labelledby={f.labelledby}
							aria-describedby={f.describedby}
						>
							{#each slides as slide, i (slide.id)}
								{@const p = `login-slide-${i}`}
								{@const shownTitle = builtIn(slide.slideKey, 'title')}
								{@const shownBody = builtIn(slide.slideKey, 'body')}
								<li class="slide">
									<div class="slide-head">
										<h4>{t('settings.appearance.slide_n', { n: i + 1 })}</h4>
										<div class="inline-controls">
											<Button size="sm" id="{p}-up" disabled={i === 0} onclick={() => move(i, -1)}
												>{t('branding.login_slide_move_up')}</Button
											>
											<Button
												size="sm"
												id="{p}-down"
												disabled={i >= slides.length - 1}
												onclick={() => move(i, 1)}>{t('branding.login_slide_move_down')}</Button
											>
											<Button
												size="sm"
												variant="danger-quiet"
												disabled={slides.length <= 1}
												onclick={() => removeSlide(i)}>{t('branding.login_slides_remove')}</Button
											>
										</div>
									</div>
									<Field
										id="{p}-file"
										label={t('branding.login_slide_image')}
										hint={tf(
											'settings.brand.slide_upload_help',
											'Uploaded when you pick it and used once you save.'
										)}
									>
										{#snippet children(ff)}<FilePicker
												id={ff.id}
												describedby={ff.describedby}
												accept="image/png,image/jpeg,image/webp,image/gif"
												onchange={(e) => void onSlideFile(i, e.currentTarget)}
											/>{/snippet}
									</Field>
									<Field
										id="{p}-url"
										label={t('branding.bg_image_url')}
										hint={tf(
											'settings.brand.slide_url_help',
											'Filled in when you upload a picture. An address on this server, for example /img/login-slides/free.jpg.'
										)}
										error={form.refusedAt(`loginSlides.${i}.imageUrl`)}
									>
										{#snippet children(ff)}<Input {...ff} bind:value={slide.imageUrl} />{/snippet}
									</Field>
									<Field
										id="{p}-title"
										label={t('branding.login_slide_title')}
										hint={shownTitle
											? tf('settings.brand.slide_empty_shows', 'Empty shows "{text}".', { text: shownTitle })
											: undefined}
										error={form.refusedAt(`loginSlides.${i}.title`)}
									>
										{#snippet children(ff)}<Input
												{...ff}
												maxlength={120}
												bind:value={slide.title}
											/>{/snippet}
									</Field>
									<Field
										id="{p}-body"
										label={t('branding.login_slide_body')}
										hint={shownBody
											? tf('settings.brand.slide_empty_shows', 'Empty shows "{text}".', { text: shownBody })
											: undefined}
										error={form.refusedAt(`loginSlides.${i}.body`)}
									>
										{#snippet children(ff)}<Textarea
												{...ff}
												rows={2}
												maxlength={400}
												bind:value={slide.body}
											/>{/snippet}
									</Field>
									<div class="form-group">
										<label class="form-label" for="{p}-overlay">{t('branding.bg_overlay')}</label>
										<RangeField
											id="{p}-overlay"
											unit="%"
											min={0}
											max={90}
											step={5}
											bind:value={slide.overlay}
										/>
									</div>
								</li>
							{/each}
						</ol>
						<div class="inline-controls">
							<Button
								icon="add"
								id="login-slides-add"
								disabled={slides.length >= MAX_SLIDES}
								onclick={addSlide}>{t('branding.login_slides_add')}</Button
							>
							<Button id="login-slides-restore" onclick={restoreSlides}
								>{t('branding.login_slides_restore')}</Button
							>
						</div>
					{/snippet}
				</SettingRow>
			</ShowWhen>
			<ShowWhen id="login-bg-config" when={v.loginPromoMode !== 'slides'}>
				<BackgroundFields
					{form}
					prefix="login-bg"
					field="loginBg"
					types={['inherit', 'color', 'gradient', 'image']}
					typeHelp={tf('settings.brand.login_bg_help', 'What shows behind the sign-in form.')}
					effects={['login-bg-overlay-group']}
					disabled={!canEdit}
				>
					<ShowWhen
						id="login-bg-overlay-group"
						when={v.loginBgType !== 'inherit' && v.loginBgType !== 'none'}
					>
						<SettingRow
							id="login-bg-overlay"
							label={t('branding.bg_overlay')}
							help={t('branding.bg_overlay_hint')}
							{...edit('loginBgOverlay')}
						>
							{#snippet control(f)}<RangeField
									{...f}
									unit="%"
									min={0}
									max={90}
									step={5}
									disabled={!canEdit}
									bind:value={() => Number(values.loginBgOverlay ?? 0), (n) => setValue('loginBgOverlay', n)}
								/>{/snippet}
						</SettingRow>
					</ShowWhen>
				</BackgroundFields>
			</ShowWhen>
		</SettingsGroup>

		<SettingsGroup
			id="branding-agent"
			title={t('branding.agent_title')}
			description={t('branding.agent_desc')}
		>
			<BackgroundFields
				{form}
				prefix="agent-bg"
				field="agentBg"
				types={['none', 'color', 'gradient', 'image']}
				typeHelp={tf('settings.brand.agent_bg_help', 'What shows behind the download page.')}
				disabled={!canEdit}
			/>
			<SettingRow
				id="agent-show-powered"
				label={t('branding.agent_show_powered')}
				help={t('branding.agent_show_powered_hint')}
				checked={v.agentShowPoweredBy}
				onchange={(on) => setValue('agentShowPoweredBy', on)}
				disabled={!canEdit}
				{...edit('agentShowPoweredBy')}
			/>
		</SettingsGroup>

		<SettingsGroup
			id="branding-rdclient"
			title={t('settings.appearance.rdclient')}
			description={t('settings.appearance.rdclient_note')}
		>
			<BackgroundFields
				{form}
				prefix="rdclient-bg"
				field="rdclientBg"
				types={['inherit', 'none', 'color', 'gradient', 'image']}
				typeHelp={tf(
					'settings.brand.rdclient_bg_help',
					'What shows behind the device list. "{inherit}" uses the console background.',
					{ inherit: t('branding.login_bg_inherit') }
				)}
				showSize
				effects={['rdclient-bg-overlay-group']}
				disabled={!canEdit}
			>
				<ShowWhen
					id="rdclient-bg-overlay-group"
					when={v.rdclientBgType !== 'inherit' && v.rdclientBgType !== 'none'}
				>
					<SettingRow
						id="rdclient-bg-overlay"
						label={t('branding.bg_overlay')}
						help={t('branding.bg_overlay_hint')}
						{...edit('rdclientBgOverlay')}
					>
						{#snippet control(f)}<RangeField
								{...f}
								unit="%"
								min={0}
								max={90}
								step={5}
								disabled={!canEdit}
								bind:value={
									() => Number(values.rdclientBgOverlay ?? 0), (n) => setValue('rdclientBgOverlay', n)
								}
							/>{/snippet}
					</SettingRow>
				</ShowWhen>
			</BackgroundFields>
		</SettingsGroup>
	</fieldset>

	<ClientAppBranding {form} />
</SettingsBody>

<style>
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
	.row-line {
		margin: 0;
		max-width: var(--measure);
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	.row-line {
		margin-top: var(--space-1);
	}
	.preview-title {
		margin: 0 0 var(--space-1);
		font-size: var(--text-md);
	}
	.preview-note {
		margin-bottom: var(--space-3);
	}
	.radio-row {
		display: flex;
		flex-wrap: wrap;
		gap: 0 var(--space-5);
	}
	.inline-controls {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-2);
	}
	h4 {
		margin: 0;
		margin-inline-end: auto;
		font-size: var(--text-base, 1rem);
	}
	.slides {
		margin: 0 0 var(--space-3);
		padding: 0;
		list-style: none;
		border-top: 1px solid var(--rule);
	}
	.slide {
		display: flex;
		flex-direction: column;
		gap: var(--space-3);
		padding: var(--space-4) 0;
		border-bottom: 1px solid var(--rule);
	}
	.slide > :global(.form-group) {
		margin: 0;
	}
	.slide-head {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-2) var(--space-3);
	}
	.form-group {
		display: flex;
		flex-direction: column;
		gap: var(--space-1);
	}
	.form-label {
		font-weight: 600;
	}
</style>
