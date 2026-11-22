package console

import (
	"net/http"
	"strings"

	"github.com/unitronix/betterdesk-server/console/branding"
	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/kit"
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
    var choice = get('bd-theme-choice');
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
    // Accessibility panel settings: same key, defaults and limits as
    // web-ui/src/lib/shell/a11y.svelte.ts
    var a = {};
    try { a = JSON.parse(get('betterdesk_accessibility_v1') || '{}') || {}; } catch (e) { a = {}; }
    function num(v, min, max, d) { v = Number(v); return isFinite(v) ? Math.min(max, Math.max(min, v)) : d; }
    var fontScale = num(a.fontScale, 90, 160, 100), lineHeight = num(a.lineHeight, 1.2, 2.1, 1.5);
    var letter = num(a.letterSpacing, 0, 0.14, 0), word = num(a.wordSpacing, 0, 0.22, 0), sat = num(a.saturation, 0, 180, 100);
    var contrast = ['normal', 'high-dark', 'high-light', 'yellow-black'].indexOf(a.contrast) >= 0 ? a.contrast : 'normal';
    var filter = ['none', 'protanopia', 'deuteranopia', 'tritanopia', 'achromatopsia'].indexOf(a.colorFilter) >= 0 ? a.colorFilter : 'none';
    var FLAGS = { readableFont: 'a11yReadableFont', dyslexiaFont: 'a11yDyslexia', underlineLinks: 'a11yUnderlineLinks',
        highlightLinks: 'a11yHighlightLinks', highlightHeadings: 'a11yHighlightHeadings', strongFocus: 'a11yStrongFocus',
        largeCursor: 'a11yLargeCursor', reduceMotion: 'a11yReduceMotion', pauseAnimations: 'a11yPauseAnimations',
        readingGuide: 'a11yReadingGuide', readingMask: 'a11yReadingMask', hideMedia: 'a11yHideMedia' };
    var active = fontScale !== 100 || lineHeight !== 1.5 || letter !== 0 || word !== 0 || sat !== 100 || contrast !== 'normal' || filter !== 'none';
    for (var f in FLAGS) { var on = a[f] === true; if (on) active = true; root.dataset[FLAGS[f]] = String(on); }
    root.style.setProperty('--bd-a11y-font-scale', String(fontScale / 100));
    root.style.setProperty('--bd-a11y-line-height', String(lineHeight));
    root.style.setProperty('--bd-a11y-letter-spacing', letter + 'em');
    root.style.setProperty('--bd-a11y-word-spacing', word + 'em');
    root.dataset.a11yActive = String(active);
    root.dataset.a11yFontScaled = String(fontScale !== 100);
    root.dataset.a11yContrast = contrast;
    var filters = [];
    if (filter !== 'none') filters.push('url(#bd-a11y-' + filter + ')');
    if (sat !== 100) filters.push('saturate(' + sat + '%)');
    root.dataset.a11yFiltered = String(filters.length > 0);
    root.style.setProperty('--bd-a11y-visual-filter', filters.length ? filters.join(' ') : 'none');
    // Sidebar width (same key and 200-320 px range as the shell), so the layout does not jump on load
    var sw = parseInt(get('bd_ux35_sidebar_width') || '', 10);
    if (sw > 0) root.style.setProperty('--sidebar-w', Math.min(320, Math.max(200, sw)) + 'px');
    window.__BD_BOOT__ = boot;
    // Start both requests now, before the app's modules load
    var opts = { credentials: 'same-origin', headers: { Accept: 'application/json' } };
    window.__BD_BOOT__.strings = fetch(boot.stringsUrl, opts);
    // Public pages (login, portal, downloads) have no session to ask for
    var path = location.pathname || '/';
    var isPublic = false;
    for (var i = 0; i < boot.publicPaths.length; i++) { if (path === boot.publicPaths[i] || path.indexOf(boot.publicPaths[i] + '/') === 0) isPublic = true; }
    if (!isPublic) window.__BD_BOOT__.session = fetch('/api/console/bootstrap', opts);
})(`
