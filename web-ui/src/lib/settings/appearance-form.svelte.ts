/**
 * The appearance (POST /api/settings/branding) as a settings draft, shared
 * by Settings › Appearance and Settings › Branding: each tab edits its
 * part, and Save sends the whole appearance, as the server stores it in
 * one piece. One field per setting (appearance.ts flatBranding), so the
 * save bar names each change.
 *
 * Pictures: a background picture uploads when it is picked and becomes the
 * field's value, unsaved like any other change; Save waits for uploads that
 * still run. A logo file is kept until Save uploads it (the draft has its
 * name as `logoUpload`).
 *
 * The values the server would refuse are said at their row before anything
 * is sent (appearanceProblems), and the server's own refusal lands there
 * too (`refusedAt`).
 */

import {
	getBranding,
	saveBranding,
	uploadBackground,
	uploadLogo,
	type Readability
} from '#lib/api/settings.ts';
import { SvelteSet } from 'svelte/reactivity';
import { ApiError } from '#lib/api/client.ts';
import { t, tf } from '#lib/i18n/i18n.svelte.ts';
import { session } from '#lib/state/session.svelte.ts';
import { applyTheme, theme } from '#lib/state/theme.svelte.ts';
import {
	SettingsDraft,
	inWords,
	saveAndConfirm,
	type Change,
	type FieldSpec
} from '#lib/ui/settings/draft.svelte.ts';
import {
	COLOR_GROUPS,
	appearanceProblems,
	brandingFromServer,
	brandingPayload,
	flatBranding,
	nestBranding,
	slideNames,
	valueAt,
	type BrandingValues,
	type FlatBranding
} from './appearance.ts';

/** The draft: the appearance, the name of a logo file waiting for Save, and fields a tab adds (the client app's) */
export type AppearanceValues = FlatBranding & { logoUpload: string } & Record<string, unknown>;

const errText = (err: unknown, fallback = t('errors.server_error')) =>
	err instanceof Error && err.message ? err.message : fallback;

const BG_KIND: Record<string, string> = {
	none: 'branding.bg_type_none',
	inherit: 'branding.login_bg_inherit',
	color: 'branding.bg_type_color',
	gradient: 'branding.bg_type_gradient',
	image: 'branding.bg_type_image'
};
const BG_SIZE: Record<string, string> = {
	cover: 'branding.bg_size_cover',
	contain: 'branding.bg_size_contain',
	auto: 'branding.bg_size_auto'
};
const LOGO_KIND: Record<string, string> = {
	default: 'branding.logo_type_default',
	text: 'branding.logo_type_text',
	icon: 'branding.logo_type_icon',
	svg: 'branding.logo_type_svg',
	image: 'branding.logo_type_image'
};
const PROMO: Record<string, string> = {
	classic: 'branding.login_promo_classic',
	single: 'branding.login_promo_single',
	slides: 'branding.login_promo_slides'
};
const MODE: Record<string, string> = {
	system: 'theme.auto',
	light: 'theme.light',
	dark: 'theme.dark',
	custom: 'theme.custom'
};

const word = (table: Record<string, string>) => (v: unknown) => {
	const key = table[String(v)];
	return key ? t(key) : inWords(v);
};

/** The names and wording of the appearance's fields in the save bar and the toast */
export function appearanceSpecs(): Record<string, FieldSpec> {
	const specs: Record<string, FieldSpec> = {
		themeMode: { label: tf('settings.appearance.theme_default', 'Default theme'), format: word(MODE) },
		appName: { label: t('branding.app_name') },
		logoType: { label: t('branding.logo_title'), format: word(LOGO_KIND) },
		logoText: { label: t('branding.logo_text') },
		logoTextAccent: { label: t('branding.logo_text_accent') },
		logoIcon: { label: t('branding.logo_icon_name') },
		logoSvg: {
			label: t('branding.logo_svg_code'),
			format: (v) =>
				v
					? tf('settings.appearance.n_characters', '{count} characters', { count: String(v).length })
					: inWords(v)
		},
		logoUrl: { label: t('branding.logo_image_url') },
		logoUpload: { label: tf('settings.appearance.logo_file', 'Logo file to upload') },
		fontHeading: {
			label: t('branding.font_heading'),
			format: (v) => String(v || t('branding.font_system_default'))
		},
		fontBody: {
			label: t('branding.font_body'),
			format: (v) => String(v || t('branding.font_system_default'))
		},
		footerText: { label: t('branding.footer_text') },
		showPoweredBy: { label: t('branding.show_powered') },
		loginTitle: { label: t('branding.login_heading') },
		loginSubtitle: { label: t('branding.login_subheading') },
		loginPromoMode: { label: t('branding.login_promo_mode'), format: word(PROMO) },
		loginSlides: {
			label: t('branding.login_slides_title'),
			format: (v) => (Array.isArray(v) ? slideNames(v).join(', ') : inWords(v))
		},
		agentShowPoweredBy: { label: t('branding.agent_show_powered') },
		customCss: {
			label: t('branding.custom_css_title'),
			format: (v) =>
				v
					? tf('settings.appearance.n_characters', '{count} characters', { count: String(v).length })
					: inWords(v)
		}
	};
	for (const group of COLOR_GROUPS)
		for (const [key, labelKey] of group.colors)
			specs[`colors.${key}`] = { label: t(`settings.appearance.${labelKey}`) };
	const backgrounds: [string, string][] = [
		['bg', t('settings.appearance.console_background')],
		['loginBg', t('settings.appearance.login_background')],
		['agentBg', t('branding.agent_title')],
		['rdclientBg', t('settings.appearance.rdclient')]
	];
	const px = (v: unknown) => `${String(v)} px`;
	const pct = (v: unknown) => `${String(v)} %`;
	for (const [p, where] of backgrounds) {
		const of = (what: string) => `${where}: ${what}`;
		specs[`${p}Type`] = { label: of(t('settings.appearance.background_type')), format: word(BG_KIND) };
		specs[`${p}Color`] = { label: of(t('branding.bg_color')) };
		specs[`${p}Gradient`] = { label: of(t('branding.bg_gradient')) };
		specs[`${p}ImageUrl`] = { label: of(t('branding.bg_image_url')) };
		specs[`${p}Size`] = { label: of(t('branding.bg_size')), format: word(BG_SIZE) };
		specs[`${p}Overlay`] = { label: of(t('branding.bg_overlay')), format: pct };
		specs[`${p}Blur`] = { label: of(t('branding.bg_blur')), format: px };
	}
	return specs;
}

/** The fields of a settings tab outside the appearance (the client app's), saved by the tab */
export interface ExtraPart {
	/** Field names start with this ("client.") */
	prefix: string;
	/** Why the part's values cannot be saved, before anything is sent; null when they can */
	check?: (values: Record<string, unknown>) => string | null;
	save: (values: Record<string, unknown>, loaded: Record<string, unknown>) => Promise<void>;
}

export class AppearanceForm {
	readonly draft: SettingsDraft<AppearanceValues>;
	loaded = $state(false);
	loadError = $state('');
	readability = $state.raw<Readability>(null);
	/** The logo file picked; uploaded on Save while `logoUpload` names it */
	logoFile = $state.raw<File | null>(null);
	/** Background uploads still running; Save waits for them */
	uploading = $state(0);
	/** Save pressed while uploads run */
	waiting = $state(false);
	/**
	 * Values a save refuses, by field path ("bgColor", "colors.accentBlue",
	 * "loginSlides.1.imageUrl"): the reason, shown at the row while its value
	 * is the one refused.
	 */
	refused = $state<Record<string, { message: string; value: string }>>({});

	#uploads = new SvelteSet<Promise<unknown>>();
	#extra: ExtraPart | null = null;

	constructor() {
		this.draft = new SettingsDraft<AppearanceValues>(
			{ ...flatBranding(brandingFromServer({})), logoUpload: '' } as AppearanceValues,
			appearanceSpecs()
		);
	}

	/** The appearance as the form has it now, and as saved */
	get values(): BrandingValues {
		return nestBranding(this.draft.values);
	}
	get saved(): BrandingValues {
		return nestBranding(this.draft.loaded);
	}

	/** A tab's own fields, saved with the appearance by the same Save */
	addPart(part: ExtraPart, values: Record<string, unknown>, specs: Record<string, FieldSpec>): void {
		this.#extra = part;
		this.draft.describe(specs);
		this.#replace(values, (k) => k.startsWith(part.prefix));
	}

	/**
	 * Put fresh values in place of some fields, as loaded; the other fields
	 * keep what they hold, saved or not.
	 */
	#replace(next: Record<string, unknown>, owns: (field: string) => boolean): void {
		const loaded: Record<string, unknown> = {};
		const values: Record<string, unknown> = {};
		const before = $state.snapshot(this.draft.values) as Record<string, unknown>;
		for (const [k, v] of Object.entries(this.draft.loaded)) if (!owns(k)) loaded[k] = v;
		for (const [k, v] of Object.entries(before)) if (!owns(k)) values[k] = v;
		this.draft.load({ ...loaded, ...next } as AppearanceValues);
		for (const [k, v] of Object.entries(values)) (this.draft.values as Record<string, unknown>)[k] = v;
	}

	/** Fresh appearance from the server: the appearance's fields as loaded, a waiting logo dropped */
	fill(next: BrandingValues): void {
		const extra = this.#extra?.prefix;
		this.#replace({ ...flatBranding(next), logoUpload: '' }, (k) => !extra || !k.startsWith(extra));
		this.logoFile = null;
		this.refused = {};
	}

	async load(): Promise<void> {
		try {
			const resp = await getBranding();
			this.fill(brandingFromServer(resp.data));
			this.readability = resp.readability;
			this.loaded = true;
			this.loadError = '';
		} catch (err) {
			this.loadError = errText(err);
		}
	}

	/** After something saved the appearance on the server (theme, saved appearance, import, reset) */
	async reloadFromServer(revision?: string | number | null): Promise<void> {
		const resp = await getBranding();
		const next = brandingFromServer(resp.data);
		this.fill(next);
		this.readability = resp.readability;
		applyToConsole(brandingPayload(next), revision ?? resp.revision);
	}

	refusedAt(path: string): string | undefined {
		const r = this.refused[path];
		return r && JSON.stringify(valueAt(this.values, path) ?? '') === r.value ? r.message : undefined;
	}

	#setRefused(entries: [string, string][], values: BrandingValues): void {
		this.refused = Object.fromEntries(
			entries.map(([field, message]) => [
				field,
				{ message, value: JSON.stringify(valueAt(values, field) ?? '') }
			])
		);
	}

	/** Pick a logo file: Save uploads it; the current logo stays until then */
	pickLogo(file: File | null): void {
		this.logoFile = file;
		this.draft.set('logoUpload', file?.name ?? '');
	}

	/** The logo file still waits for Save (Discard takes the name away) */
	get pendingLogo(): File | null {
		return this.draft.values.logoUpload ? this.logoFile : null;
	}

	/**
	 * Upload a background picture. It is stored on the server and becomes
	 * the field's value, saved with the next Save; the picture kind is set
	 * to Picture with it.
	 */
	uploadBackground(
		file: File,
		urlField: keyof AppearanceValues & string,
		typeField: keyof AppearanceValues & string,
		onProgress?: (p: number) => void
	): Promise<unknown> {
		const job = (async () => {
			const r = await uploadBackground(file, '', session.raw?.csrfToken ?? '', onProgress);
			const values = this.draft.values as Record<string, unknown>;
			values[urlField] = r.url;
			values[typeField] = 'image';
		})();
		this.#track(job);
		return job;
	}

	/** Upload a slide's picture; it becomes the slide's picture address */
	uploadSlide(file: File, index: number): Promise<unknown> {
		const job = (async () => {
			const r = await uploadBackground(file, '', session.raw?.csrfToken ?? '');
			const slide = this.draft.values.loginSlides[index];
			if (slide) slide.imageUrl = r.url;
		})();
		this.#track(job);
		return job;
	}

	#track(job: Promise<unknown>): void {
		this.#uploads.add(job);
		this.uploading = this.#uploads.size;
		void job
			.finally(() => {
				this.#uploads.delete(job);
				this.uploading = this.#uploads.size;
			})
			.catch(() => undefined);
	}

	/** Save: waits for uploads, checks the values, uploads a waiting logo, sends the appearance and the tab's own part */
	async save(): Promise<boolean> {
		if (this.waiting || this.draft.busy) return false;
		if (this.#uploads.size) {
			this.waiting = true;
			try {
				await Promise.allSettled([...this.#uploads]);
			} finally {
				this.waiting = false;
			}
		}
		return saveAndConfirm(this.draft, (changes) => this.#send(changes));
	}

	async #send(changes: Change[]): Promise<void> {
		const prefix = this.#extra?.prefix;
		const own = (field: string) => !prefix || !field.startsWith(prefix);
		const extraChanged = !!prefix && changes.some((c) => c.field.startsWith(prefix));
		const brandingChanged = changes.some((c) => own(c.field));
		const problem = extraChanged
			? this.#extra?.check?.($state.snapshot(this.draft.values) as Record<string, unknown>)
			: null;
		if (problem) throw new Error(problem);
		if (brandingChanged) {
			const values = this.values;
			const problems = Object.entries(
				appearanceProblems(values, this.saved, { pendingLogo: !!this.pendingLogo })
			);
			this.#setRefused(
				problems.map(([field, key]) => [field, t(key)]),
				values
			);
			if (problems.length) throw new Error(t('settings.appearance.refused_summary'));
			let next = values;
			const logo = this.pendingLogo;
			if (logo) {
				const url = await uploadLogo(logo);
				next = { ...values, logoUrl: url };
				this.draft.values.logoUrl = url;
			}
			const payload = brandingPayload(next);
			let resp;
			try {
				resp = await saveBranding(payload);
			} catch (err) {
				const fields = refusedFields(err);
				if (!fields.length) throw err;
				this.#setRefused(fields, next);
				throw new Error(t('settings.appearance.refused_summary'), { cause: err });
			}
			this.readability = resp.readability;
			this.logoFile = null;
			this.draft.values.logoUpload = '';
			applyToConsole(payload, resp.revision);
			// Saved: if the tab's own part fails next, these are not unsaved any more
			const saved: Record<string, unknown> = {};
			for (const [k, v] of Object.entries($state.snapshot(this.draft.values))) if (own(k)) saved[k] = v;
			this.draft.loaded = { ...this.draft.loaded, ...saved } as AppearanceValues;
		}
		if (extraChanged && this.#extra) {
			const part = this.#extra.prefix;
			const pick = (o: Record<string, unknown>) =>
				Object.fromEntries(Object.entries(o).filter(([k]) => k.startsWith(part)));
			await this.#extra.save(
				pick($state.snapshot(this.draft.values) as Record<string, unknown>),
				pick(this.draft.loaded as Record<string, unknown>)
			);
		}
	}
}

/** The fields and reasons of a refused save ({fields: [{field, message}]}) */
function refusedFields(err: unknown): [string, string][] {
	if (!(err instanceof ApiError) || !err.data || typeof err.data !== 'object') return [];
	const list = (err.data as { fields?: unknown }).fields;
	if (!Array.isArray(list)) return [];
	return list.flatMap((x: unknown) => {
		const f = x as { field?: unknown; message?: unknown };
		return typeof f.field === 'string' && typeof f.message === 'string'
			? [[f.field, f.message] as [string, string]]
			: [];
	});
}

/** The sidebar, the tab title, the theme and branding.css follow what was saved */
export function applyToConsole(
	p: Record<string, unknown>,
	revision: string | number | null | undefined
): void {
	const rev = encodeURIComponent(String(revision ?? Date.now()));
	for (const el of document.querySelectorAll<HTMLLinkElement>('link[href*="/css/branding.css"]')) {
		const href = el.getAttribute('href') ?? '';
		el.setAttribute('href', `${href.split('?')[0]}?v=${rev}`);
	}
	const icon = document.querySelector<HTMLLinkElement>('link[rel="icon"]');
	if (icon) icon.setAttribute('href', `${(icon.getAttribute('href') ?? '').split('?')[0]}?v=${rev}`);
	const raw = session.raw;
	const name = String(p.appName || 'NeoDesk');
	session.appName = name;
	if (raw) {
		session.raw = {
			...raw,
			branding: {
				...raw.branding,
				appName: name,
				themeMode: String(p.themeMode ?? 'system'),
				logoType: String(p.logoType ?? 'default'),
				logoIcon: String(p.logoIcon ?? ''),
				logoSvg: String(p.logoSvg ?? ''),
				logoUrl: String(p.logoUrl ?? ''),
				logoText: String(p.logoText ?? ''),
				logoTextAccent: String(p.logoTextAccent ?? ''),
				footerText: String(p.footerText ?? ''),
				showPoweredBy: p.showPoweredBy !== 'false',
				revision: revision ?? raw.branding.revision
			}
		};
	}
	theme.consoleMode = String(p.themeMode ?? 'system');
	applyTheme();
}
