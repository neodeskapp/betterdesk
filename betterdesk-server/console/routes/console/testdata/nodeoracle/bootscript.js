'use strict';

/**
 * Records what GET /console-boot.js of routes/console.routes.js answered for
 * boot values of every kind, the ones that must be escaped among them. The
 * services the route reads are stand-ins that hand it the values of each
 * vector. See README.md.
 *
 *   node bootscript.js   (in the dev image, from this folder)
 *
 * Writes ../bootscript.json: per vector the boot object the route built
 * (publicBoot) and the whole script it sent, with its headers.
 */

const fs = require('fs');
const path = require('path');

const WEB = process.env.WEB_NODEJS || '/repo/web-nodejs';

function stub(rel, exports) {
    const file = require.resolve(path.join(WEB, rel));
    require.cache[file] = { id: file, filename: file, loaded: true, exports, children: [], paths: [] };
}

let state = {};
stub('services/brandingService', {
    getBranding: () => state.branding,
    getThemePalettes: () => state.palettes,
    getBrandingRevision: () => state.revision,
});
stub('services/i18nService', {
    manager: {
        getLanguageMeta: (code) => state.meta[code] || null,
        getMergedTranslations: () => state.merged,
        getAvailableList: () => [],
    },
});
stub('middleware/auth', { requireAuth: (req, res, next) => next(), requestHasPermission: () => false, isSuperAdminRole: () => false });
stub('middleware/panelAccess', { ROUTE_RULES: [], findRule: () => null });
stub('services/panelAccessPolicyService', { accessAllows: () => true });
stub('lib/panelAccessCatalog', { PANEL_ACCESS_CATALOG: [], normalizeAccessLevel: (v) => v });
stub('services/panelSessionService', { getPolicy: () => ({}) });
stub('services/avatarService', { getAvatarVersion: async () => null });
stub('services/supportersService', { loadSupporters: () => ({}) });
stub('lib/consoleNav', { buildNav: () => ({ sections: [] }) });

const ROUTES = require.resolve(path.join(WEB, 'routes/console.routes'));

function bootScript(lang) {
    // A fresh module each time: it keeps the strings revision per language
    delete require.cache[ROUTES];
    const router = require(ROUTES);
    const layer = router.stack.find((l) => l.route && l.route.path === '/console-boot.js');
    const headers = {};
    let body = null;
    const req = { lang, method: 'GET', headers: {} };
    const res = {
        setHeader: (name, value) => { headers[name.toLowerCase()] = value; },
        send: (text) => { body = text; },
    };
    layer.route.stack[0].handle(req, res, () => {});
    return { boot: router.publicBoot(req), headers, body };
}

const palettes = {
    dark: { bgPrimary: '#0d1117', accentBlue: '#58a6ff' },
    light: { bgPrimary: '#ffffff', accentBlue: '#1f6feb' },
};
const vectors = [];
const tricky = [
    '</script><script>alert(1)</script>', 'a&b>c', 'line sep para', '"quoted" \\ back', 'tab\tnew\nline',
    'ümlaut 中文 😀', '\u0000\u001f\u007f', '<!--', '<', '',
];
const langs = [undefined, 'en', 'de', 'ar', 'he', 'zh-TW', 'xx'];
for (const lang of langs) {
    for (const themeMode of [undefined, 'system', 'dark', ...tricky]) {
        state = {
            branding: themeMode === undefined ? {} : { themeMode },
            palettes,
            revision: 'mwb1fhy3',
            meta: { ar: { rtl: true }, he: { rtl: 1 }, de: { rtl: false }, en: {} },
            merged: { nav: { devices: 'Devices' }, lang: lang || 'none' },
        };
        vectors.push({ lang: lang === undefined ? null : lang, themeMode, ...bootScript(lang) });
    }
}
for (const value of tricky) {
    state = {
        branding: {},
        palettes: { dark: { bgPrimary: value, [value || 'empty']: 'x' }, light: null },
        revision: value,
        meta: {},
        merged: { text: value },
    };
    vectors.push({ lang: 'en', palette: value, ...bootScript('en') });
}
state = { branding: { themeMode: 'light' }, palettes: {}, revision: '2026-11-10010551', meta: {}, merged: {} };
vectors.push({ lang: 'en', palettes: 'none', ...bootScript('en') });

const file = path.join(__dirname, '..', 'bootscript.json');
fs.writeFileSync(file, `${JSON.stringify({ vectors }, null, 1)}\n`);
console.log(`${file}: ${vectors.length} vectors`);
