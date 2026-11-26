package console

import (
	"net/http"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/branding"
	"github.com/neodeskapp/neodesk/neodesk-server/console/httpx"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
)

// publicPaths are the pages that work without a session; the boot script
// starts no bootstrap request on them. "/remote" covers the remote client:
// its viewer opens for guest links too and gets its data elsewhere, and
// its dashboard asks for the bootstrap itself.
var publicPaths = []any{"/login", "/portal", "/d", "/remote"}

// publicBoot is what the boot script and the bootstrap share and anyone
// may know: the request language and its direction, the theme mode and
// palettes, the branding revision and the address of the strings. The
// caller has refreshed the branding.
func (h *handlers) publicBoot(r *http.Request) *branding.Object {
	lang := kit.Lang(r)
	if lang == "" {
		lang = h.d.Config.DefaultLanguage
	}
	if lang == "" {
		lang = "en"
	}
	dir := "ltr"
	if meta := h.d.I18n.Bundle.Meta(lang); meta != nil {
		if rtl, _ := meta.Get("rtl"); rtl == true {
			dir = "rtl"
		}
	}
	s := h.d.Branding
	boot := branding.NewObject()
	boot.Set("lang", lang)
	boot.Set("dir", dir)
	boot.Set("themeMode", s.ThemeMode())
	boot.Set("palettes", s.BootPalettes())
	boot.Set("brandingRevision", s.Revision())
	boot.Set("stringsUrl", h.d.I18n.StringsURL(lang))
	return boot
}

// bootScript answers GET /console-boot.js: the script the page runs in its
// head before the first paint. It sets the language, the direction and the
// theme on <html>, applies the accessibility settings and the sidebar
// width the browser keeps, and starts the strings and bootstrap requests.
// What it carries is what the sign-in page shows anyway.
func (h *handlers) bootScript(w http.ResponseWriter, r *http.Request) {
	boot := h.publicBoot(r)
	boot.Set("publicPaths", publicPaths)
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteBody(w, r, http.StatusOK, "", bootScriptBody(boot))
}

// bootScriptBody is the script with boot as its argument. JSON is valid
// script; "<" is escaped so that no value can close the script tag.
func bootScriptBody(boot *branding.Object) []byte {
	data := strings.ReplaceAll(branding.Stringify(boot), "<", `\u003c`)
	return []byte(bootScriptHead + data + ");\n")
}

// bootScriptHead is the script up to its argument. It runs synchronously
// in <head>, must stay small and free of dependencies, and must not throw
// when storage is blocked.
const bootScriptHead = `(function (boot) {
    var root = document.documentElement;
    var VARS = {
        bgPrimary: '--bg-primary', bgSecondary: '--bg-secondary', bgTertiary: '--bg-tertiary',
        bgElevated: '--bg-elevated', textPrimary: '--text-primary', textSecondary: '--text-secondary',
        accentBlue: '--accent-blue', accentBlueHover: '--accent-blue-hover', accentBlueMuted: '--accent-blue-muted',
        accentGreen: '--accent-green', accentGreenHover: '--accent-green-hover', accentGreenMuted: '--accent-green-muted',
        accentRed: '--accent-red', accentRedHover: '--accent-red-hover', accentRedMuted: '--accent-red-muted',
        accentYellow: '--accent-yellow', accentYellowHover: '--accent-yellow-hover', accentYellowMuted: '--accent-yellow-muted',
        accentPurple: '--accent-purple', accentPurpleHover: '--accent-purple-hover', accentPurpleMuted: '--accent-purple-muted',
        borderPrimary: '--border-primary', borderSecondary: '--border-secondary'
    };
    function get(k) { try { return localStorage.getItem(k); } catch (e) { return null; } }
    root.lang = boot.lang;
    root.dir = boot.dir;
    root.setAttribute('data-theme-mode', boot.themeMode);
    var choice = get('nd-theme-choice');
    var mode = boot.themeMode;
    if ((choice === 'light' || choice === 'dark') && boot.palettes[choice]) {
        root.setAttribute('data-theme-default', boot.themeMode);
        root.setAttribute('data-theme-mode', choice);
        mode = choice;
        var p = boot.palettes[choice];
        for (var k in VARS) { if (p[k]) root.style.setProperty(VARS[k], p[k]); }
    }
    var theme = mode === 'dark' ? 'dark' : 'light';
    if (mode === 'system' && window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches) theme = 'dark';
    root.setAttribute('data-theme', theme);
    root.style.colorScheme = theme;
` + bootScriptA11y + `    // Sidebar width (same key and 200-320 px range as the shell), so the layout does not jump on load
    var sw = parseInt(get('nd_ux35_sidebar_width') || '', 10);
    if (sw > 0) root.style.setProperty('--sidebar-w', Math.min(320, Math.max(200, sw)) + 'px');
    window.__ND_BOOT__ = boot;
    // Start both requests now, before the app's modules load
    var opts = { credentials: 'same-origin', headers: { Accept: 'application/json' } };
    window.__ND_BOOT__.strings = fetch(boot.stringsUrl, opts);
    // Public pages (login, portal, downloads) have no session to ask for
    var path = location.pathname || '/';
    var isPublic = false;
    for (var i = 0; i < boot.publicPaths.length; i++) { if (path === boot.publicPaths[i] || path.indexOf(boot.publicPaths[i] + '/') === 0) isPublic = true; }
    if (!isPublic) window.__ND_BOOT__.session = fetch('/api/console/bootstrap', opts);
})(`

// bootScriptA11y applies the accessibility settings the browser keeps,
// with the same key, defaults and rules as web-ui/src/lib/shell/a11y.svelte.ts
// (applyToRoot). Before the app has moved the old panel's settings to the
// new key, it reads them the way the app's fromV1 does.
const bootScriptA11y = `    // Accessibility settings (web-ui/src/lib/shell/a11y.svelte.ts)
    var a = null;
    try { a = JSON.parse(get('neodesk_accessibility_v2') || 'null'); } catch (e) { a = null; }
    if (!a || typeof a !== 'object') {
        var v1 = null;
        try { v1 = JSON.parse(get('neodesk_accessibility_v1') || 'null'); } catch (e) { v1 = null; }
        v1 = v1 && typeof v1 === 'object' ? v1 : {};
        a = { reduceMotion: v1.reduceMotion === true || v1.pauseAnimations === true ? 'on' : 'system',
            moreContrast: typeof v1.contrast === 'string' && v1.contrast !== 'normal' && v1.contrast !== '' ? 'on' : 'system',
            underlineLinks: v1.underlineLinks === true, readableFont: v1.readableFont === true || v1.dyslexiaFont === true };
    }
    function applies(choice, query) {
        if (choice === 'on' || choice === 'off') return choice === 'on';
        try { return !!(window.matchMedia && window.matchMedia(query).matches); } catch (e) { return false; }
    }
    root.dataset.a11yReduceMotion = String(applies(a.reduceMotion, '(prefers-reduced-motion: reduce)'));
    root.dataset.a11yMoreContrast = String(applies(a.moreContrast, '(prefers-contrast: more)'));
    root.dataset.a11yUnderlineLinks = String(a.underlineLinks === true);
    root.dataset.a11yReadableFont = String(a.readableFont === true);
`
