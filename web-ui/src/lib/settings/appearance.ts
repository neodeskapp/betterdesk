/**
 * Settings › Appearance without a DOM: the stored appearance as the form
 * holds it, and back as the server takes it (old settings.js
 * populateBrandingForm / collectBrandingData), plus the preview's colour
 * helpers.
 */

export const COLOR_GROUPS = [
	{
		legend: 'branding.colors_backgrounds',
		colors: [
			['bgPrimary', 'color_bg'],
			['bgSecondary', 'color_bg_subtle'],
			['bgTertiary', 'color_hover'],
			['bgElevated', 'color_float']
		]
	},
	{
		legend: 'branding.colors_text',
		colors: [
			['textPrimary', 'color_text'],
			['textSecondary', 'color_text_muted']
		]
	},
	{
		legend: 'settings.appearance.colors_accents',
		colors: [
			['accentBlue', 'color_accent'],
			['accentGreen', 'color_success'],
			['accentRed', 'color_danger'],
			['accentYellow', 'color_warning'],
			['accentPurple', 'color_info']
		]
	},
	{
		legend: 'settings.appearance.colors_lines',
		colors: [
			['borderPrimary', 'color_border'],
			['borderSecondary', 'color_rule']
		]
	}
] as const;

export type ColorKey = (typeof COLOR_GROUPS)[number]['colors'][number][0];
export const COLOR_KEYS: readonly ColorKey[] = COLOR_GROUPS.flatMap((g) => g.colors.map((c) => c[0]));

export type ThemeMode = 'system' | 'light' | 'dark' | 'custom';
export type LogoType = 'default' | 'text' | 'icon' | 'svg' | 'image';
export type BgType = 'none' | 'inherit' | 'color' | 'gradient' | 'image';
export type PromoMode = 'classic' | 'single' | 'slides';

export interface Slide {
	id: string;
	slideKey: string;
	imageUrl: string;
	title: string;
	body: string;
	overlay: number;
}

export const MAX_SLIDES = 10;

export const DEFAULT_SLIDES: readonly Slide[] = ['free', 'remote', 'secure', 'platforms'].map((key) => ({
	id: key,
	slideKey: key,
	imageUrl: `/img/login-slides/${key}.jpg`,
	title: '',
	body: '',
	overlay: 45
}));

export interface BrandingValues {
	appName: string;
	logoType: LogoType;
	logoIcon: string;
	logoSvg: string;
	logoUrl: string;
	logoText: string;
	logoTextAccent: string;
	fontHeading: string;
	fontBody: string;
	colors: Record<ColorKey, string>;
	themeMode: ThemeMode;
	bgType: BgType;
	bgColor: string;
	bgGradient: string;
	bgImageUrl: string;
	bgBlur: number;
	bgOverlay: number;
	bgSize: string;
	loginTitle: string;
	loginSubtitle: string;
	loginPromoMode: PromoMode;
	loginSlides: Slide[];
	loginBgType: BgType;
	loginBgColor: string;
	loginBgGradient: string;
	loginBgImageUrl: string;
	loginBgOverlay: number;
	agentBgType: BgType;
	agentBgColor: string;
	agentBgGradient: string;
	agentBgImageUrl: string;
	agentShowPoweredBy: boolean;
	rdclientBgType: BgType;
	rdclientBgColor: string;
	rdclientBgGradient: string;
	rdclientBgImageUrl: string;
	rdclientBgOverlay: number;
	rdclientBgSize: string;
	footerText: string;
	showPoweredBy: boolean;
	customCss: string;
}

const s = (v: unknown, fallback = ''): string =>
	v === null || v === undefined || v === '' ? fallback : String(v);
const n = (v: unknown, fallback: number): number => {
	const x = Number(v);
	return v === '' || v === null || v === undefined || !Number.isFinite(x) ? fallback : x;
};
const pick = <T extends string>(v: unknown, allowed: readonly T[], fallback: T): T =>
	allowed.includes(v as T) ? (v as T) : fallback;

const BG4: readonly BgType[] = ['none', 'color', 'gradient', 'image'];
const BG_LOGIN: readonly BgType[] = ['inherit', 'none', 'color', 'gradient', 'image'];
const HEX = /^#[0-9a-fA-F]{6}$/;
/** A colour the form takes: #rgb or #rrggbb */
const HEX_COLOR = /^#(?:[0-9a-f]{3}|[0-9a-f]{6})$/i;

export function isHex(v: string): boolean {
	return HEX.test(v.trim());
}

/** The stored appearance as the form holds it */
export function brandingFromServer(d: Record<string, unknown>): BrandingValues {
	const colorsIn = d.colors && typeof d.colors === 'object' ? (d.colors as Record<string, unknown>) : {};
	const colors = Object.fromEntries(COLOR_KEYS.map((k) => [k, s(colorsIn[k])])) as Record<ColorKey, string>;
	const rawSlides = Array.isArray(d.loginSlides) ? (d.loginSlides as Record<string, unknown>[]) : [];
	const slides = rawSlides.length
		? rawSlides.slice(0, MAX_SLIDES).map((x, i) => ({
				id: s(x.id, `slide-${i + 1}`),
				slideKey: s(x.slideKey),
				imageUrl: s(x.imageUrl),
				title: s(x.title),
				body: s(x.body),
				overlay: n(x.overlay, 45)
			}))
		: DEFAULT_SLIDES.map((x) => ({ ...x }));
	const mode = d.themeMode === 'auto' ? 'dark' : d.themeMode;
	return {
		appName: s(d.appName),
		logoType: pick(d.logoType, ['default', 'text', 'icon', 'svg', 'image'], 'default'),
		logoIcon: s(d.logoIcon, 'dns'),
		logoSvg: s(d.logoSvg),
		logoUrl: s(d.logoUrl),
		logoText: s(d.logoText),
		logoTextAccent: s(d.logoTextAccent),
		fontHeading: s(d.fontHeading),
		fontBody: s(d.fontBody),
		colors,
		themeMode: pick(mode, ['system', 'light', 'dark', 'custom'], 'system'),
		bgType: pick(d.bgType, BG4, 'none'),
		bgColor: s(d.bgColor),
		bgGradient: s(d.bgGradient),
		bgImageUrl: s(d.bgImageUrl),
		bgBlur: n(d.bgBlur, 0),
		bgOverlay: n(d.bgOverlay, 0),
		bgSize: s(d.bgSize, 'cover'),
		loginTitle: s(d.loginTitle),
		loginSubtitle: s(d.loginSubtitle),
		loginPromoMode: pick(d.loginPromoMode, ['classic', 'single', 'slides'], 'slides'),
		loginSlides: slides,
		loginBgType: pick(d.loginBgType, BG_LOGIN, 'inherit'),
		loginBgColor: s(d.loginBgColor),
		loginBgGradient: s(d.loginBgGradient),
		loginBgImageUrl: s(d.loginBgImageUrl),
		loginBgOverlay: n(d.loginBgOverlay, 0),
		agentBgType: pick(d.agentBgType, BG4, 'none'),
		agentBgColor: s(d.agentBgColor),
		agentBgGradient: s(d.agentBgGradient),
		agentBgImageUrl: s(d.agentBgImageUrl),
		agentShowPoweredBy: d.agentShowPoweredBy !== 'false' && d.agentShowPoweredBy !== false,
		rdclientBgType: pick(d.rdclientBgType, BG_LOGIN, 'inherit'),
		rdclientBgColor: s(d.rdclientBgColor),
		rdclientBgGradient: s(d.rdclientBgGradient),
		rdclientBgImageUrl: s(d.rdclientBgImageUrl),
		rdclientBgOverlay: n(d.rdclientBgOverlay, 0),
		rdclientBgSize: s(d.rdclientBgSize, 'cover'),
		footerText: s(d.footerText),
		showPoweredBy: d.showPoweredBy !== 'false' && d.showPoweredBy !== false,
		customCss: s(d.customCss)
	};
}

/** What POST /api/settings/branding takes: the old page's flat keys, numbers and switches as strings */
export function brandingPayload(v: BrandingValues): Record<string, unknown> {
	const colors: Record<string, string> = {};
	for (const k of COLOR_KEYS) {
		const c = (v.colors[k] ?? '').trim();
		if (HEX_COLOR.test(c)) colors[k] = c;
	}
	return {
		appName: v.appName || 'NeoDesk',
		logoType: v.logoType,
		logoIcon: v.logoIcon || 'dns',
		logoSvg: v.logoSvg,
		logoUrl: v.logoUrl,
		logoText: v.logoText,
		logoTextAccent: v.logoTextAccent,
		fontHeading: v.fontHeading,
		fontBody: v.fontBody,
		colors,
		themeMode: v.themeMode,
		bgType: v.bgType,
		bgColor: v.bgColor.trim(),
		bgGradient: v.bgGradient.trim(),
		bgImageUrl: v.bgImageUrl.trim(),
		bgBlur: String(v.bgBlur),
		bgOverlay: String(v.bgOverlay),
		bgSize: v.bgSize || 'cover',
		loginBgType: v.loginBgType,
		loginBgColor: v.loginBgColor.trim(),
		loginBgGradient: v.loginBgGradient.trim(),
		loginBgImageUrl: v.loginBgImageUrl.trim(),
		loginBgOverlay: String(v.loginBgOverlay),
		loginTitle: v.loginTitle.trim(),
		loginSubtitle: v.loginSubtitle.trim(),
		loginPromoMode: v.loginPromoMode,
		loginSlides: v.loginSlides.slice(0, MAX_SLIDES).map((x, i) => ({
			id: x.id || `slide-${i + 1}`,
			slideKey: x.slideKey,
			order: i,
			imageUrl: x.imageUrl.trim(),
			title: x.title.trim(),
			body: x.body.trim(),
			overlay: String(x.overlay)
		})),
		agentBgType: v.agentBgType,
		agentBgColor: v.agentBgColor.trim(),
		agentBgGradient: v.agentBgGradient.trim(),
		agentBgImageUrl: v.agentBgImageUrl.trim(),
		agentShowPoweredBy: v.agentShowPoweredBy ? 'true' : 'false',
		rdclientBgType: v.rdclientBgType,
		rdclientBgColor: v.rdclientBgColor.trim(),
		rdclientBgGradient: v.rdclientBgGradient.trim(),
		rdclientBgImageUrl: v.rdclientBgImageUrl.trim(),
		rdclientBgOverlay: String(v.rdclientBgOverlay),
		rdclientBgSize: v.rdclientBgSize || 'cover',
		footerText: v.footerText.trim(),
		showPoweredBy: v.showPoweredBy ? 'true' : 'false',
		customCss: v.customCss
	};
}

/**
 * The logo icons to pick from: names the console's icon font has (the font
 * is a subset, scripts/icon-names.ts), so each draws its symbol.
 */
export const LOGO_ICONS: readonly string[] = [
	'dns',
	'cloud',
	'security',
	'shield',
	'verified_user',
	'support_agent',
	'desktop_windows',
	'devices',
	'monitor',
	'router',
	'storage',
	'settings_ethernet',
	'cell_tower',
	'home',
	'business',
	'workspaces',
	'apps',
	'dashboard',
	'lock',
	'key',
	'language',
	'build',
	'code',
	'terminal',
	'developer_board',
	'electrical_services',
	'favorite',
	'volunteer_activism'
];

const GRADIENT = /^(?:repeating-)?(?:linear|radial|conic)-gradient\(.+\)$/i;
/** What the server keeps of a colour or gradient (sanitizeCSSColorValue) */
const GRADIENT_CHARS = /^[A-Za-z0-9\s#%.,()/-]*$/;
// eslint-disable-next-line no-control-regex -- control characters are what it looks for
const CONTROL = /[\x00-\x1f\x7f]/;

/** Why a picture address is refused: the server's check (validateBrandingURL), and only this server's pictures load (img-src 'self') */
function addressProblem(url: string): string | null {
	const u = url.trim();
	if (!u) return null;
	if (u.startsWith('/') && !u.startsWith('//')) {
		return u.includes('..') || CONTROL.test(u) ? 'refused_address' : null;
	}
	return /^https?:\/\/[^/]/i.test(u) ? 'refused_outside_address' : 'refused_address';
}

/**
 * The values a save would refuse, by field ("bgColor", "colors.accentBlue",
 * "loginSlides.1.imageUrl"), each with the key of its reason: what the
 * server would otherwise clean or drop without a word (server:
 * branding.CheckSettings), and a logo kind without its content. Only what
 * the form shows is checked, and a value that is still the saved one
 * passes, as on the server. SVG code and custom CSS are left to the server.
 */
export function appearanceProblems(
	v: BrandingValues,
	saved: BrandingValues,
	opts: { pendingLogo: boolean }
): Record<string, string> {
	const out: Record<string, string> = {};
	const refuse = (field: string, reason: string | null) => {
		if (reason) out[field] = `settings.appearance.${reason}`;
	};
	const changed = (a: string, b: string | undefined) => a.trim() !== (b ?? '').trim();

	if (v.themeMode === 'custom') {
		for (const k of COLOR_KEYS) {
			const c = (v.colors[k] ?? '').trim();
			if (c && !HEX_COLOR.test(c) && changed(c, saved.colors[k])) refuse(`colors.${k}`, 'refused_color');
		}
	}
	const backgrounds = [
		['bgType', 'bgColor', 'bgGradient', 'bgImageUrl'],
		['loginBgType', 'loginBgColor', 'loginBgGradient', 'loginBgImageUrl'],
		['agentBgType', 'agentBgColor', 'agentBgGradient', 'agentBgImageUrl'],
		['rdclientBgType', 'rdclientBgColor', 'rdclientBgGradient', 'rdclientBgImageUrl']
	] as const;
	for (const [typeKey, colorKey, gradientKey, urlKey] of backgrounds) {
		if (typeKey === 'loginBgType' && v.loginPromoMode === 'slides') continue;
		const type = v[typeKey];
		const color = v[colorKey].trim();
		const gradient = v[gradientKey].trim();
		if (type === 'color' && color && !HEX_COLOR.test(color) && changed(color, saved[colorKey])) {
			refuse(colorKey, 'refused_color');
		}
		const badGradient = !GRADIENT.test(gradient) || !GRADIENT_CHARS.test(gradient) || gradient.length > 400;
		if (type === 'gradient' && gradient && badGradient && changed(gradient, saved[gradientKey])) {
			refuse(gradientKey, 'refused_gradient');
		}
		if (type === 'image' && changed(v[urlKey], saved[urlKey])) refuse(urlKey, addressProblem(v[urlKey]));
	}
	if (v.loginPromoMode === 'slides') {
		v.loginSlides.forEach((slide, i) => refuse(`loginSlides.${i}.imageUrl`, addressProblem(slide.imageUrl)));
	}
	switch (v.logoType) {
		case 'text':
			if (!v.logoText.trim()) refuse('logoText', 'refused_logo_text');
			break;
		case 'svg':
			if (!v.logoSvg.trim()) refuse('logoSvg', 'refused_logo_svg');
			break;
		case 'image':
			if (!v.logoUrl.trim() && !opts.pendingLogo) refuse('logoUrl', 'refused_logo_image');
			else if (changed(v.logoUrl, saved.logoUrl)) refuse('logoUrl', addressProblem(v.logoUrl));
			break;
		case 'icon':
			if (!LOGO_ICONS.includes(v.logoIcon.trim())) refuse('logoIcon', 'refused_icon');
			break;
	}
	return out;
}

/** The value at a field path of the form ("colors.accentBlue", "loginSlides.1.imageUrl") */
export function valueAt(values: unknown, path: string): unknown {
	let at: unknown = values;
	for (const part of path.split('.')) {
		if (at === null || typeof at !== 'object') return undefined;
		at = (at as Record<string, unknown>)[part];
	}
	return at;
}

/** The colours a palette (light, dark, or the console's own for system) gives the custom fields */
export function paletteColors(
	palette: Record<string, string> | null | undefined
): Partial<Record<ColorKey, string>> {
	const out: Partial<Record<ColorKey, string>> = {};
	for (const k of COLOR_KEYS) {
		const c = palette?.[k];
		if (c) out[k] = c;
	}
	return out;
}

/** Move a slide one place up (-1) or down (+1) */
export function moveSlide(slides: readonly Slide[], index: number, by: -1 | 1): Slide[] {
	const to = index + by;
	if (to < 0 || to >= slides.length) return [...slides];
	const out = [...slides];
	const a = out[index];
	const b = out[to];
	if (!a || !b) return out;
	out[index] = b;
	out[to] = a;
	return out;
}

function rgb(hex: string): [number, number, number] | null {
	const m = /^#?([0-9a-f]{6})$/i.exec(String(hex || '').trim());
	if (!m || !m[1]) return null;
	const v = parseInt(m[1], 16);
	return [(v >> 16) & 255, (v >> 8) & 255, v & 255];
}

/** Relative luminance (WCAG) of a hex colour, null for anything else */
export function luminance(hex: string): number | null {
	const c = rgb(hex);
	if (!c) return null;
	const [r, g, b] = c.map((x) => {
		const v = x / 255;
		return v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4);
	}) as [number, number, number];
	return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

/** Black or white, whichever reads better on the colour */
export function textOn(hex: string): string {
	const l = luminance(hex);
	if (l === null) return '';
	return (l + 0.05) / 0.05 >= 1.05 / (l + 0.05) ? '#000000' : '#ffffff';
}

/** A font family for CSS: quotes taken out, the console font after it */
export function fontStack(family: string): string {
	const f = family.replace(/["'\\;{}<>]/g, '').trim();
	return f ? `"${f}", var(--font)` : '';
}

/** Logo and background checks the old page made before uploading */
export const LOGO_TYPES = ['image/png', 'image/jpeg', 'image/gif', 'image/webp', 'image/svg+xml'];
export const LOGO_MAX = 2 * 1024 * 1024;
export const BG_TYPES = ['image/png', 'image/jpeg', 'image/gif', 'image/webp'];
export const BG_MAX = 8 * 1024 * 1024;

export function fileProblem(
	file: { size: number; type: string },
	types: readonly string[],
	max: number
): 'size' | 'type' | null {
	if (file.size > max) return 'size';
	if (!types.includes(file.type)) return 'type';
	return null;
}
