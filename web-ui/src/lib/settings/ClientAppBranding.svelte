<!--
  Settings › Branding › Client app (#client-app; the page /client-branding
  before, which leads here): the name, contact details, accent colour and
  logo that NeoDesk clients and support agents show. Its fields are part
  of the Branding tab's draft ("client." fields of AppearanceForm), so the
  tab's one save bar lists and saves them with the rest
  (POST /api/client-branding for these). A preview (ClientAppPreview)
  follows the fields as people type: in the tab's side column from 1200
  px, at the end of the group below that. Clients pick changes up when they
  next check in.

  Needs branding.edit (both calls): without it the group is left out.
-->
<script module lang="ts">
	import type { BrandingLogo } from '#lib/api/clientBranding.ts';

	export const CLIENT = 'client.';

	export interface ClientValues {
		'client.company_name': string;
		'client.support_contact': string;
		'client.phone': string;
		'client.email': string;
		'client.website': string;
		'client.accent_color': string;
		'client.logo': Partial<BrandingLogo> | null;
	}
</script>

<script lang="ts">
	import { getClientBranding, saveClientBranding, type ClientBranding } from '#lib/api/clientBranding.ts';
	import {
		DEFAULT_ACCENT,
		brandingPayload,
		checkLogo,
		isHex,
		logoFromDataUrl,
		logoSrc,
		savedAt
	} from '#lib/clients/branding.ts';
	import { around } from '#lib/clients/text.ts';
	import { t, tf } from '#lib/i18n/i18n.svelte.ts';
	import { can } from '#lib/state/session.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import ColorField from '#lib/ui/ColorField.svelte';
	import FilePicker from '#lib/ui/FilePicker.svelte';
	import Input from '#lib/ui/Input.svelte';
	import States from '#lib/ui/States.svelte';
	import Time from '#lib/ui/Time.svelte';
	import SettingRow from '#lib/ui/settings/SettingRow.svelte';
	import SettingsGroup from '#lib/ui/settings/SettingsGroup.svelte';
	import { inWords } from '#lib/ui/settings/draft.svelte.ts';
	import type { AppearanceForm } from './appearance-form.svelte.ts';
	import ClientAppPreview from './ClientAppPreview.svelte';

	let { form }: { form: AppearanceForm } = $props();

	const allowed = can('branding.edit');
	const draft = $derived(form.draft);
	const values = $derived(form.draft.values as unknown as ClientValues);
	const setValue = (field: keyof ClientValues, value: unknown) =>
		((form.draft.values as Record<string, unknown>)[field] = value);

	function valuesOf(b: ClientBranding | null): ClientValues {
		return {
			'client.company_name': b?.company_name ?? '',
			'client.support_contact': b?.support_contact ?? '',
			'client.phone': b?.phone ?? '',
			'client.email': b?.email ?? '',
			'client.website': b?.website ?? '',
			'client.accent_color': b?.accent_color || DEFAULT_ACCENT,
			'client.logo': b?.logo && (b.logo.data_base64 || b.logo.url) ? { ...b.logo } : null
		};
	}

	let revision = $state('');
	let loaded = $state(false);
	let loadError = $state<string | null>(null);
	let files = $state<FileList | null>(null);
	let fileInput = $state<HTMLInputElement | null>(null);
	let logoError = $state('');

	const of = (what: string) => `${t('settings.branding.client_app')}: ${what}`;
	const pictureWords = (v: unknown) => {
		const logo = v as Partial<BrandingLogo> | null;
		if (!logo) return inWords(null);
		const kb = Math.max(1, Math.round(((logo.data_base64?.length ?? 0) * 3) / 4 / 1024));
		return logo.data_base64
			? tf('settings.brand.picture_kb', 'Picture, {kb} KB', { kb })
			: (logo.url ?? inWords(null));
	};

	function register(b: ClientBranding | null) {
		form.addPart(
			{
				prefix: CLIENT,
				check: (v) =>
					isHex(String(v['client.accent_color'] ?? '')) ? null : t('client_branding.accent_invalid'),
				save: async (v, before) => {
					const now = v as unknown as ClientValues;
					const saved = await saveClientBranding(
						brandingPayload({
							company_name: now['client.company_name'],
							support_contact: now['client.support_contact'],
							phone: now['client.phone'],
							email: now['client.email'],
							website: now['client.website'],
							accent_color: now['client.accent_color'],
							logo: now['client.logo'],
							clearLogo: !now['client.logo'] && !!before['client.logo']
						})
					).catch((err: unknown) => {
						throw new Error(
							`${t('client_branding.save_failed')} ${err instanceof Error ? err.message : ''}`.trim()
						);
					});
					revision = saved.revision;
					if (fileInput) fileInput.value = '';
				}
			},
			{ ...valuesOf(b) },
			{
				'client.company_name': { label: of(t('client_branding.company_name')) },
				'client.support_contact': { label: of(t('client_branding.support_contact')) },
				'client.phone': { label: of(t('client_branding.phone')) },
				'client.email': { label: of(t('client_branding.email')) },
				'client.website': { label: of(t('client_branding.website')) },
				'client.accent_color': { label: of(t('client_branding.accent_color')) },
				'client.logo': { label: of(t('client_branding.logo')), format: pictureWords }
			}
		);
	}

	async function load(): Promise<void> {
		if (!allowed) return;
		try {
			const b = await getClientBranding();
			revision = b.revision;
			register(b);
			if (fileInput) fileInput.value = '';
			logoError = '';
			loadError = null;
		} catch (err) {
			loadError = `${t('client_branding.load_failed')} ${err instanceof Error ? err.message : ''}`.trim();
		} finally {
			loaded = true;
		}
	}

	$effect(() => {
		void load();
	});

	/* ── Logo ── */

	function onLogoFile() {
		const file = files?.[0];
		if (!file) return;
		const problem = checkLogo(file);
		if (problem) {
			logoError = t(
				problem === 'too_large' ? 'client_branding.logo_too_large' : 'client_branding.logo_invalid'
			);
			if (fileInput) fileInput.value = '';
			return;
		}
		const reader = new FileReader();
		reader.onload = () => {
			setValue('client.logo', logoFromDataUrl(String(reader.result ?? ''), file.type));
			logoError = '';
		};
		reader.onerror = () => (logoError = t('client_branding.logo_invalid'));
		reader.readAsDataURL(file);
	}

	function clearLogo() {
		setValue('client.logo', null);
		if (fileInput) fileInput.value = '';
	}

	const undo = $derived(tf('settings.appearance.undo_change', 'Undo change'));
	const edit = (field: keyof ClientValues) => ({
		changed: draft.isChanged(field),
		onreset: draft.isChanged(field) ? () => draft.reset(field) : undefined,
		resetLabel: undo
	});
	const text = (field: keyof ClientValues) => String(values[field] ?? '');

	const accent = $derived(text('client.accent_color'));
	const accentError = $derived(
		draft.isChanged('client.accent_color') && !isHex(accent) ? t('client_branding.accent_invalid') : undefined
	);
	const logo = $derived(logoSrc(values['client.logo']));
	const saved = $derived(savedAt(revision));
</script>

{#if allowed}
	<SettingsGroup
		id="client-app"
		title={t('settings.branding.client_app')}
		description={t('client_branding.page_description')}
		consequence={t('client_branding.effect')}
		class="client-branding-page"
	>
		{#snippet note()}
			{#if loadError}
				<States kind="error" message={loadError} onretry={() => void load()} />
			{/if}
			<p class="cb-revision">
				{#if saved}{@const [pre, post] = around(t('client_branding.last_saved'), '{date}')}{pre}<Time
						value={saved}
					/>{post}{:else if loaded && !loadError}{tf(
						'client_branding.defaults_in_use',
						'Nothing saved here yet: clients show the values below until you save your own.'
					)}{/if}
			</p>
		{/snippet}
		<h3 class="cb-sub" id="cb-contact-title">{t('client_branding.contact_title')}</h3>
		<SettingRow
			id="client-app-company"
			label={t('client_branding.company_name')}
			help={t('client_branding.company_hint')}
			{...edit('client.company_name')}
		>
			{#snippet control(f)}<Input
					id={f.id}
					describedby={f.describedby}
					maxlength={256}
					class="wide-44"
					bind:value={() => text('client.company_name'), (x) => setValue('client.company_name', x)}
				/>{/snippet}
		</SettingRow>
		<SettingRow
			id="client-app-support"
			label={t('client_branding.support_contact')}
			help={t('client_branding.support_hint')}
			{...edit('client.support_contact')}
		>
			{#snippet control(f)}<Input
					id={f.id}
					describedby={f.describedby}
					maxlength={256}
					class="wide-44"
					bind:value={() => text('client.support_contact'), (x) => setValue('client.support_contact', x)}
				/>{/snippet}
		</SettingRow>
		<SettingRow
			id="client-app-phone"
			label={t('client_branding.phone')}
			help={t('client_branding.contact_note')}
			{...edit('client.phone')}
		>
			{#snippet control(f)}<Input
					id={f.id}
					describedby={f.describedby}
					type="tel"
					maxlength={256}
					dir="ltr"
					class="wide-44"
					bind:value={() => text('client.phone'), (x) => setValue('client.phone', x)}
				/>{/snippet}
		</SettingRow>
		<SettingRow id="client-app-email" label={t('client_branding.email')} {...edit('client.email')}>
			{#snippet control(f)}<Input
					id={f.id}
					describedby={f.describedby}
					type="email"
					maxlength={256}
					dir="ltr"
					class="wide-44"
					bind:value={() => text('client.email'), (x) => setValue('client.email', x)}
				/>{/snippet}
		</SettingRow>
		<SettingRow
			id="client-app-website"
			label={t('client_branding.website')}
			help={tf('settings.brand.website_help', 'A full address, for example https://example.com.')}
			{...edit('client.website')}
		>
			{#snippet control(f)}<Input
					id={f.id}
					describedby={f.describedby}
					type="url"
					maxlength={512}
					dir="ltr"
					class="wide-44"
					bind:value={() => text('client.website'), (x) => setValue('client.website', x)}
				/>{/snippet}
		</SettingRow>

		<h3 class="cb-sub" id="cb-look-title">{t('client_branding.look_title')}</h3>
		<SettingRow
			id="client-app-accent"
			label={t('client_branding.accent_color')}
			help={t('client_branding.accent_hint')}
			error={accentError}
			{...edit('client.accent_color')}
		>
			{#snippet control(f)}
				<ColorField
					id={f.id}
					describedby={f.describedby}
					invalid={f.invalid}
					label={t('client_branding.accent_color')}
					bind:value={() => accent, (x) => setValue('client.accent_color', x)}
				/>
			{/snippet}
		</SettingRow>
		<SettingRow
			id="client-app-logo"
			label={t('client_branding.logo')}
			help={t('client_branding.logo_hint')}
			error={logoError || undefined}
			{...edit('client.logo')}
		>
			{#snippet control(f)}
				<div class="logo-current">
					{#if logo}
						<img src={logo} alt={t('client_branding.logo_preview_alt')} />
						<Button size="sm" class="cb-clear-logo" onclick={clearLogo}
							>{t('client_branding.clear_logo')}</Button
						>
					{:else}
						<span class="text-muted">{t('client_branding.no_logo')}</span>
					{/if}
				</div>
				<FilePicker
					id={f.id}
					describedby={f.describedby}
					accept="image/png,image/jpeg,image/webp,.png,.jpg,.jpeg,.webp"
					bind:files
					bind:element={fileInput}
					onchange={onLogoFile}
				/>
			{/snippet}
		</SettingRow>

		<ClientAppPreview {form} place="group" />
	</SettingsGroup>
{/if}

<style>
	.cb-sub {
		margin: var(--space-4) 0 0;
		padding: 0 var(--row-pad-x) var(--space-1);
		font-size: var(--text-sm);
		font-weight: 600;
		color: var(--text-muted);
	}
	.cb-revision {
		margin: 0;
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	.cb-revision:empty {
		display: none;
	}
	.logo-current {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-3);
		margin-bottom: var(--space-2);
	}
	.logo-current img {
		display: block;
		max-width: 160px;
		max-height: 64px;
		padding: var(--space-1);
		border: 1px solid var(--rule);
		border-radius: var(--radius);
		background: var(--bg-subtle);
		object-fit: contain;
	}
	:global(.client-branding-page .wide-44) {
		max-width: 44ch;
	}
</style>
