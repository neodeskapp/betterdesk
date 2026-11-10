'use strict';

/**
 * Records what the functions of services/brandingService.js and
 * services/fontService.js that need no database answer for generated
 * input. Writes ../pure.json.gz. See README.md in this folder.
 *
 *   node pure.js
 */

const path = require('path');
const lib = require('./lib');

const root = lib.scratchConsole('branding-pure');
process.env.NODE_ENV = 'test';
process.env.DATA_DIR = path.join(root, 'data');
process.chdir(root);

// The database is not touched by the functions recorded here
lib.stub(root, 'services/database.js', new Proxy({}, {
    get: (_t, prop) => (prop === 'then' ? undefined : async () => { throw new Error('no database in pure.js'); }),
}));

const branding = require(path.join(root, 'services/brandingService'));
const fonts = require(path.join(root, 'services/fontService'));

const vectors = {};
const counts = {};

/** Call fn of mod with each argument list and record the answers. */
function run(name, mod, fnName, argLists) {
    const out = [];
    const seen = new Set();
    for (const raw of argLists) {
        const args = raw.map((a) => lib.jsonClone(a));
        const key = JSON.stringify(args.map(lib.encodeArg));
        if (seen.has(key)) continue;
        seen.add(key);
        out.push({ a: args.map(lib.encodeArg), ...lib.record(() => mod[fnName](...args), 1500) });
    }
    vectors[name] = out;
    counts[name] = out.length;
}

function times(n, make) {
    const out = [];
    for (let i = 0; i < n; i++) out.push(make(i));
    return out;
}

const each = (list) => list.map((v) => [v]);
const WRONG = (() => { const r = lib.rng(7); return times(200, () => [lib.wrongType(r)]); })();
const NOTHING = [[undefined], [null]];

{
    const r = lib.rng(101);
    run('sanitizeSvg', branding, 'sanitizeSvg', [
        ...each(lib.SVG_PARTS), ...times(3000, () => [lib.svgText(r)]), ...times(200, () => [lib.anyText(r)]), ...WRONG, ...NOTHING,
    ]);
}
{
    const r = lib.rng(102);
    run('validateBrandingUrl', branding, 'validateBrandingUrl', [
        ...each(lib.URL_PARTS.whole), ...times(9000, () => [lib.urlText(r)]), ...times(400, () => [lib.anyText(r)]), ...WRONG, ...NOTHING,
        // every ASCII character in every part of a URL
        ...times(128, (c) => [`https://example.com/a${String.fromCharCode(c)}b`]),
        ...times(128, (c) => [`https://example.com/?a${String.fromCharCode(c)}b`]),
        ...times(128, (c) => [`https://example.com/#a${String.fromCharCode(c)}b`]),
        ...times(128, (c) => [`https://a${String.fromCharCode(c)}b.example/`]),
        ...times(128, (c) => [`https://a${String.fromCharCode(c)}b@example.com/`]),
        ...times(128, (c) => [`https://u:a${String.fromCharCode(c)}b@example.com/`]),
        ...times(128, (c) => [`https://example.com:8${String.fromCharCode(c)}0/`]),
        ...times(128, (c) => [`http${String.fromCharCode(c)}://example.com/`]),
        ...times(128, (c) => [`${String.fromCharCode(c)}https://example.com/${String.fromCharCode(c)}`]),
        ...times(128, (c) => [`/a${String.fromCharCode(c)}b`]),
        ...times(256, (c) => [`https://a%${c.toString(16).padStart(2, '0')}b.example/p%${c.toString(16).padStart(2, '0')}`]),
    ]);
}
{
    const r = lib.rng(103);
    run('sanitizeCssColorValue', branding, 'sanitizeCssColorValue', [
        ...each(lib.CSS_ATTACKS), ...each(lib.ODD_TEXT), ...times(3000, () => [lib.anyValue(r)]), ...times(300, () => [lib.colorValue(r)]), ...WRONG, ...NOTHING,
    ]);
}
{
    const r = lib.rng(104);
    run('sanitizeCustomCss', branding, 'sanitizeCustomCss', [
        ...each(lib.CUSTOM_CSS_PARTS), ...times(3500, () => [lib.cssText(r)]), ...times(300, () => [lib.anyValue(r)]), ...times(200, () => [lib.svgText(r)]), ...WRONG, ...NOTHING,
    ]);
}
{
    const r = lib.rng(105);
    const type = () => (r.chance(0.85) ? r.pick(['color', 'gradient', 'image', 'image', 'none', 'inherit']) : lib.anyValue(r));
    run('buildBackgroundValue', branding, 'buildBackgroundValue', times(3000, () => [
        type(), lib.colorValue(r), r.chance(0.5) ? 'linear-gradient(90deg, #000, #fff)' : lib.anyValue(r), r.chance(0.3) ? '/uploads/bg-0123456789abcdef.jpg' : lib.urlText(r),
    ]));
}
{
    const r = lib.rng(106);
    const texts = [...lib.ODD_TEXT, ...lib.CSS_ATTACKS.slice(0, 8)];
    run('normalizeBackgroundSize', branding, 'normalizeBackgroundSize', [...each(texts), ...WRONG, ...NOTHING, ...times(100, () => [lib.anyValue(r)])]);
    run('normalizeLoginPromoMode', branding, 'normalizeLoginPromoMode', [...each(texts), ...WRONG, ...NOTHING, ...times(100, () => [lib.anyValue(r)])]);
    run('normalizeThemeMode', branding, 'normalizeThemeMode', [...each(texts), ...WRONG, ...NOTHING, ...times(100, () => [lib.anyValue(r)])]);
    run('resolveLoginPromoMode', branding, 'resolveLoginPromoMode', [
        ...texts.flatMap((t) => [[t, {}, true], [t, {}, false]]), ...WRONG.map(([w]) => [w, {}, true]), [undefined, {}, true], [undefined, {}, false],
    ]);
}
{
    const r = lib.rng(107);
    run('sanitizeLoginSlides', branding, 'sanitizeLoginSlides', [
        ...times(3500, () => [lib.slidesValue(r)]), ...WRONG, ...NOTHING,
    ]);
}
{
    const r = lib.rng(108);
    run('flattenAppearanceInput', branding, 'flattenAppearanceInput', [
        ...times(3200, () => [lib.settingsObject(r)]), ...WRONG, ...NOTHING,
    ]);
}
{
    const r = lib.rng(109);
    run('resolveThemeColors', branding, 'resolveThemeColors', [
        ...times(1500, () => {
            const o = {};
            if (r.chance(0.9)) o.themeMode = lib.settingValue(r, 'themeMode');
            if (r.chance(0.9)) o.colors = lib.colorsValue(r);
            return [o];
        }),
        ...WRONG, ...NOTHING,
    ]);
}
{
    const r = lib.rng(110);
    const alpha = () => r.pick([undefined, 0.15, 0.1, 0, 1, 0.5, 2, -1, 1e-7, 0.30000000000000004]);
    run('hexToMutedRgba', branding, 'hexToMutedRgba', [
        ...times(600, () => [lib.colorValue(r), alpha()]), ...each(lib.ODD_TEXT), ...WRONG, ...NOTHING,
    ]);
}
{
    const r = lib.rng(111);
    run('usesDefaultLogo', branding, 'usesDefaultLogo', [
        ...times(1200, () => {
            const o = {};
            for (const key of ['logoType', 'logoIcon', 'logoSvg', 'logoUrl', 'logoText']) {
                if (r.chance(0.7)) o[key] = lib.settingValue(r, key);
            }
            return [o];
        }),
        ...WRONG, ...NOTHING,
    ]);
}

/** Settings as the cache holds them, or as a caller might pass them. */
function brandingLike(r) {
    const k = r.int(10);
    if (k === 0) return lib.settingsObject(r);
    const o = k < 6 ? JSON.parse(JSON.stringify(branding.DEFAULT_BRANDING)) : {};
    const n = r.pick([0, 1, 3, 6, 12, 25]);
    for (let i = 0; i < n; i++) {
        const key = r.pick(lib.FLAT_KEYS);
        o[key] = lib.settingValue(r, key);
    }
    if (r.chance(0.5)) {
        const colors = (o.colors && typeof o.colors === 'object' && !Array.isArray(o.colors)) ? o.colors : (o.colors = {});
        for (const key of ['bgPrimary', 'bgSecondary', 'textPrimary', 'textSecondary', 'accentBlue']) {
            if (r.chance(0.8)) lib.own(colors, key, r.chance(0.9) ? lib.hexColor(r) : lib.colorValue(r));
        }
    }
    return o;
}
{
    const r = lib.rng(112);
    run('getAppearanceModel', branding, 'getAppearanceModel', [
        ...times(2200, () => [brandingLike(r)]), ...WRONG, ...NOTHING,
    ]);
}
{
    const r = lib.rng(113);
    const gray = (v) => '#' + v.toString(16).padStart(2, '0').repeat(3);
    run('assessAppearanceReadability', branding, 'assessAppearanceReadability', [
        ...times(2000, () => [brandingLike(r)]),
        // contrast ratios that sit close to the limits and to rounding edges
        ...times(256, (v) => [{ colors: { textPrimary: gray(v), bgPrimary: gray(255 - v), bgSecondary: gray((v * 7) % 256), textSecondary: gray((v * 13) % 256), accentBlue: gray((v * 29) % 256) } }]),
        ...times(2500, () => [{ colors: { textPrimary: lib.hexColor(r), bgPrimary: lib.hexColor(r), bgSecondary: lib.hexColor(r), textSecondary: lib.hexColor(r), accentBlue: lib.hexColor(r) } }]),
        ...WRONG, ...NOTHING,
    ]);
}
{
    const r = lib.rng(114);
    const names = ['Inter', 'Open Sans', 'JetBrains Mono', ' Roboto ', 'A  B\tC', 'a\u00a0b', 'a\u2028b', 'caf\u00e9', '\u4f60\u597d', '.', '..', '-', '--', 'A.B', 'a/b', 'a\\b', '\u0130stanbul', '\u212aelvin', 'X'.repeat(300)];
    run('sanitizeFontName', fonts, 'sanitizeFontName', [...each(names), ...each(lib.ODD_TEXT), ...times(500, () => [lib.anyValue(r)]), ...WRONG, ...NOTHING]);
    run('escapeCssString', fonts, 'escapeCssString', [
        ...each(names), ...each(lib.ODD_TEXT), ...each(lib.CSS_ATTACKS), ...times(128, (c) => [`a${String.fromCharCode(c)}b`]), ...times(500, () => [lib.anyValue(r)]), ...WRONG, ...NOTHING,
    ]);
}

vectors.constants = {
    DEFAULT_BRANDING: JSON.stringify(branding.DEFAULT_BRANDING),
    COLOR_TO_CSS_VAR: JSON.stringify(branding.COLOR_TO_CSS_VAR),
    BUILTIN_THEME_PALETTES: JSON.stringify(branding.BUILTIN_THEME_PALETTES),
    getThemePalettes: JSON.stringify(branding.getThemePalettes()),
    getDefaultLoginSlides: JSON.stringify(branding.getDefaultLoginSlides()),
    MAX_LOGIN_SLIDES: branding.MAX_LOGIN_SLIDES,
    LEGACY_DEFAULT_LOGO_URL: branding.LEGACY_DEFAULT_LOGO_URL,
    CURATED_FONTS: JSON.stringify(fonts.CURATED_FONTS),
    getBrandingBeforeLoad: JSON.stringify(branding.getBranding()),
    getBrandingRevisionBeforeLoad: branding.getBrandingRevision(),
    // The module keeps luminanceComponent to itself. These are the bits of
    // what its formula gives in this engine for every colour component,
    // which the Go table has to match (Math.pow is not the same everywhere
    // in the last bit).
    luminance: Array.from({ length: 256 }, (_, value) => {
        const srgb = value / 255;
        const linear = srgb <= 0.03928 ? srgb / 12.92 : Math.pow((srgb + 0.055) / 1.055, 2.4);
        const bits = Buffer.alloc(8);
        bits.writeDoubleBE(linear);
        return bits.toString('hex');
    }).join(' '),
};

const info = lib.writeVectors('pure', vectors);
let total = 0;
for (const name of Object.keys(counts)) total += counts[name];
console.log(JSON.stringify({ ...info, total, counts }, null, 1));
require('fs').rmSync(root, { recursive: true, force: true });
