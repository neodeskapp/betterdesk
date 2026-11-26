<!--
  One background (console, sign-in page, download page, web remote
  client) as setting rows of the appearance draft: the kind (none, same as
  the console, colour, gradient, picture) and the rows of the chosen kind.
  A picked picture uploads at once and becomes the picture address, saved
  with the rest when Save is pressed (AppearanceForm.uploadBackground).
  Ids follow the old page: <prefix>-type, <prefix>-color, <prefix>-gradient,
  <prefix>-image-file, <prefix>-image-url, <prefix>-size, and the panels
  <prefix>-<kind>-panel the kinds show. children: the rows shown for
  colour, gradient and picture (blur, darken); their ids go in `effects`,
  so the choices point at them.
-->
<script lang="ts">
	import type { Snippet } from 'svelte';
	import { t, tf } from '#lib/i18n/i18n.svelte.ts';
	import ColorField from '#lib/ui/ColorField.svelte';
	import FilePicker from '#lib/ui/FilePicker.svelte';
	import FormStatus from '#lib/ui/FormStatus.svelte';
	import Input from '#lib/ui/Input.svelte';
	import Meter from '#lib/ui/Meter.svelte';
	import Radio from '#lib/ui/Radio.svelte';
	import Select from '#lib/ui/Select.svelte';
	import ShowWhen from '#lib/ui/ShowWhen.svelte';
	import SettingRow from '#lib/ui/settings/SettingRow.svelte';
	import type { AppearanceForm } from './appearance-form.svelte.ts';
	import { BG_MAX, BG_TYPES, fileProblem, type BgType } from './appearance.ts';

	interface Props {
		form: AppearanceForm;
		prefix: string;
		/** The draft's fields: "bg" for bgType, bgColor, bgGradient, bgImageUrl, bgSize */
		field: 'bg' | 'loginBg' | 'agentBg' | 'rdclientBg';
		types: readonly BgType[];
		showSize?: boolean;
		/** What the kind choice is for, under its name */
		typeHelp: string;
		/** Picked from the uploaded pictures (console background) */
		library?: Snippet;
		effects?: readonly string[];
		disabled?: boolean;
		children?: Snippet;
	}

	let {
		form,
		prefix,
		field,
		types,
		showSize = false,
		typeHelp,
		library,
		effects = [],
		disabled = false,
		children
	}: Props = $props();

	const values = $derived(form.draft.values as Record<string, unknown>);
	const k = $derived({
		type: `${field}Type`,
		color: `${field}Color`,
		gradient: `${field}Gradient`,
		imageUrl: `${field}ImageUrl`,
		size: `${field}Size`
	});
	const get = (key: string) => String(values[key] ?? '');
	const set = (key: string, v: string) => ((form.draft.values as Record<string, unknown>)[key] = v);
	const type = $derived(get(k.type) as BgType);
	const changed = (key: string) => form.draft.isChanged(key);
	const reset = (key: string) => () => form.draft.reset(key);
	const undo = $derived(tf('settings.appearance.undo_change', 'Undo change'));

	const LABEL: Record<BgType, string> = {
		none: 'branding.bg_type_none',
		inherit: 'branding.login_bg_inherit',
		color: 'branding.bg_type_color',
		gradient: 'branding.bg_type_gradient',
		image: 'branding.bg_type_image'
	};

	const panel = (kind: BgType) => `${prefix}-${kind}-panel`;
	const controls = (kind: BgType) =>
		[
			kind === 'color' || kind === 'gradient' || kind === 'image' ? panel(kind) : '',
			...(kind === 'none' || kind === 'inherit' ? [] : effects)
		]
			.filter(Boolean)
			.join(' ') || undefined;

	let files = $state<FileList | null>(null);
	let picker = $state<HTMLInputElement | null>(null);
	let progress = $state<{ state: 'idle' | 'uploading' | 'ok' | 'error'; message: string; percent: number }>({
		state: 'idle',
		message: '',
		percent: 0
	});

	async function onFile() {
		const file = files?.[0];
		if (!file) return;
		const problem = fileProblem(file, BG_TYPES, BG_MAX);
		if (problem) {
			progress = {
				state: 'error',
				message: t(problem === 'size' ? 'branding.bg_image_too_large' : 'branding.bg_image_invalid_type'),
				percent: 0
			};
			if (picker) picker.value = '';
			return;
		}
		progress = { state: 'uploading', message: `${file.name}: ${t('branding.status_saving')}`, percent: 1 };
		try {
			await form.uploadBackground(file, k.imageUrl, k.type, (percent) => {
				progress = { state: 'uploading', message: `${file.name}: ${percent} %`, percent };
			});
			progress = {
				state: 'ok',
				message: tf('settings.appearance.bg_uploaded', '{name} is uploaded. It is used once you save.', {
					name: file.name
				}),
				percent: 100
			};
		} catch (err) {
			progress = {
				state: 'error',
				message: err instanceof Error && err.message ? err.message : t('errors.server_error'),
				percent: 0
			};
		} finally {
			if (picker) picker.value = '';
		}
	}
</script>

<SettingRow
	id="{prefix}-type"
	label={t('settings.appearance.background_type')}
	help={typeHelp}
	group
	changed={changed(k.type)}
	onreset={changed(k.type) ? reset(k.type) : undefined}
	resetLabel={undo}
>
	{#snippet control(f)}
		<div
			class="radio-row"
			role="radiogroup"
			id={f.id}
			aria-labelledby={f.labelledby}
			aria-describedby={f.describedby}
		>
			{#each types as kind (kind)}
				<Radio
					name="{prefix}-type"
					value={kind}
					group={type}
					onchange={() => set(k.type, kind)}
					label={t(LABEL[kind])}
					{disabled}
					aria-controls={controls(kind)}
				/>
			{/each}
		</div>
	{/snippet}
</SettingRow>

{#if types.includes('color')}
	<ShowWhen id={panel('color')} when={type === 'color'}>
		<SettingRow
			id="{prefix}-color"
			label={t('branding.bg_color')}
			help={tf('settings.appearance.color_help', 'A colour as #rrggbb, for example #1e293b.')}
			error={form.refusedAt(k.color)}
			changed={changed(k.color)}
			onreset={changed(k.color) ? reset(k.color) : undefined}
			resetLabel={undo}
		>
			{#snippet control(f)}
				<ColorField
					id={f.id}
					describedby={f.describedby}
					invalid={f.invalid}
					{disabled}
					label={t('branding.bg_color')}
					bind:value={() => get(k.color), (v) => set(k.color, v)}
				/>
			{/snippet}
		</SettingRow>
	</ShowWhen>
{/if}

{#if types.includes('gradient')}
	<ShowWhen id={panel('gradient')} when={type === 'gradient'}>
		<SettingRow
			id="{prefix}-gradient"
			label={t('branding.bg_gradient')}
			help={t('branding.bg_gradient_hint')}
			error={form.refusedAt(k.gradient)}
			changed={changed(k.gradient)}
			onreset={changed(k.gradient) ? reset(k.gradient) : undefined}
			resetLabel={undo}
		>
			{#snippet control(f)}
				<Input
					id={f.id}
					describedby={f.describedby}
					invalid={f.invalid}
					{disabled}
					class="mono"
					maxlength={400}
					bind:value={() => get(k.gradient), (v) => set(k.gradient, v)}
				/>
			{/snippet}
		</SettingRow>
	</ShowWhen>
{/if}

{#if types.includes('image')}
	<ShowWhen id={panel('image')} when={type === 'image'}>
		{@render library?.()}
		<SettingRow
			id="{prefix}-image-file"
			label={t('branding.bg_image_upload')}
			help="{t('branding.bg_image_hint')} {tf(
				'settings.appearance.bg_upload_save',
				'It is uploaded when you pick it and used once you save.'
			)}"
		>
			{#snippet control(f)}
				<FilePicker
					id={f.id}
					accept="image/png,image/jpeg,image/webp,image/gif"
					bind:files
					bind:element={picker}
					describedby={f.describedby}
					{disabled}
					onchange={() => void onFile()}
				/>
				{#if progress.state !== 'idle'}
					<div class="upload-status" id="{prefix}-upload-status">
						<FormStatus
							message={progress.message}
							tone={progress.state === 'ok' ? 'ok' : progress.state === 'error' ? 'error' : 'neutral'}
						/>
						{#if progress.state === 'uploading'}<Meter
								value={progress.percent}
								text="{progress.percent} %"
							/>{/if}
					</div>
				{/if}
			{/snippet}
		</SettingRow>
		<SettingRow
			id="{prefix}-image-url"
			label={t('branding.bg_image_url')}
			help={tf(
				'settings.appearance.image_url_help',
				"Filled in when you upload or pick a picture. You can also enter an address on this server that starts with /, for example /uploads/wallpaper.jpg. Pictures from other websites don't load."
			)}
			error={form.refusedAt(k.imageUrl)}
			changed={changed(k.imageUrl)}
			onreset={changed(k.imageUrl) ? reset(k.imageUrl) : undefined}
			resetLabel={undo}
		>
			{#snippet control(f)}
				<Input
					id={f.id}
					describedby={f.describedby}
					invalid={f.invalid}
					{disabled}
					bind:value={() => get(k.imageUrl), (v) => set(k.imageUrl, v)}
				/>
			{/snippet}
		</SettingRow>
		{#if showSize}
			<SettingRow
				id="{prefix}-size"
				label={t('branding.bg_size')}
				help={tf('settings.appearance.size_help', 'How the picture fills the space behind the page.')}
				changed={changed(k.size)}
				onreset={changed(k.size) ? reset(k.size) : undefined}
				resetLabel={undo}
			>
				{#snippet control(f)}
					<Select
						id={f.id}
						describedby={f.describedby}
						{disabled}
						bind:value={() => get(k.size) || 'cover', (v) => set(k.size, v)}
						options={[
							{ value: 'cover', label: t('branding.bg_size_cover') },
							{ value: 'contain', label: t('branding.bg_size_contain') },
							{ value: 'auto', label: t('branding.bg_size_auto') }
						]}
					/>
				{/snippet}
			</SettingRow>
		{/if}
	</ShowWhen>
{/if}

{@render children?.()}

<style>
	.radio-row {
		display: flex;
		flex-wrap: wrap;
		gap: 0 var(--space-5);
	}
	.upload-status {
		display: flex;
		flex-direction: column;
		gap: var(--space-1);
		margin-top: var(--space-2);
	}
</style>
