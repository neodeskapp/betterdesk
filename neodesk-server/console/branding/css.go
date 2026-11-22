package branding

import (
	"math"
	"regexp"
	"strings"
)

var reHashHex6 = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// semanticAliasLines are the custom properties that map the newer names
// (--color-*, --ux35-*) onto the palette. The color-scheme line goes
// between the two blocks.
var semanticAliasLines = [2][]string{
	{
		"    --color-primary: var(--accent-blue);",
		"    --color-primary-hover: var(--accent-blue-hover);",
		"    --color-primary-muted: var(--accent-blue-muted);",
		"    --accent-color: var(--accent-blue);",
		"    --color-success: var(--accent-green);",
		"    --color-success-hover: var(--accent-green-hover);",
		"    --color-success-muted: var(--accent-green-muted);",
		"    --color-danger: var(--accent-red);",
		"    --color-danger-hover: var(--accent-red-hover);",
		"    --color-danger-muted: var(--accent-red-muted);",
		"    --color-warning: var(--accent-yellow);",
		"    --color-warning-hover: var(--accent-yellow-hover);",
		"    --color-warning-muted: var(--accent-yellow-muted);",
		"    --color-info: var(--accent-purple);",
		"    --color-info-hover: var(--accent-purple-hover);",
		"    --color-info-muted: var(--accent-purple-muted);",
		"    --color-surface: var(--bg-secondary);",
		"    --color-surface-alt: var(--bg-primary);",
		"    --color-surface-hover: var(--bg-tertiary);",
		"    --color-surface-raised: var(--bg-elevated);",
		"    --color-text: var(--text-primary);",
		"    --color-text-muted: var(--text-secondary);",
		"    --color-border: var(--border-primary);",
		"    --focus-ring-color: var(--accent-blue-muted);",
	},
	{
		"    /* UX 3.5 chrome aliases — solid surfaces (no glass/blur in this shell) */",
		"    --ux35-bg: var(--bg-primary);",
		"    --ux35-sidebar-bg: var(--bg-secondary);",
		"    --ux35-card-bg: var(--bg-secondary);",
		"    --ux35-border: var(--border-primary);",
		"    --ux35-border-light: var(--border-secondary);",
		"    --ux35-text: var(--text-primary);",
		"    --ux35-muted: var(--text-secondary);",
		"    --ux35-hover: var(--bg-hover);",
		"    --ux35-primary: var(--accent-blue);",
		"    --ux35-focus-ring: var(--accent-blue);",
		"    --ux35-active-bg: var(--accent-blue-muted);",
		"    --ux35-glass-blur: 0px;",
		"    --ux35-glass-saturate: 1;",
		"    /* UX 3.5 topbar chrome — theme-invariant (always dark) */",
		"    --ux35-topbar-bg: #161b22;",
		"    --ux35-topbar-fg: #e6edf3;",
		"    --ux35-topbar-fg-muted: #8b949e;",
		"    --ux35-topbar-border: #30363d;",
	},
}

func semanticAliasCSS(themeMode string) string {
	lines := append([]string{}, semanticAliasLines[0]...)
	lines = append(lines, "    color-scheme: "+colorSchemeFor(themeMode)+";")
	lines = append(lines, semanticAliasLines[1]...)
	return ":root {\n" + strings.Join(lines, "\n") + "\n}\n"
}

// paletteDeclarations are the custom property lines of one palette. A
// muted colour given as hex becomes a translucent rgba.
func paletteDeclarations(colors *Object) []string {
	var out []string
	for _, pair := range colorCSSVars {
		value := property(colors, pair[0])
		if !Truthy(value) {
			continue
		}
		text := toString(value)
		if trim(text) == "" {
			continue
		}
		if strings.HasSuffix(pair[0], "Muted") && strings.HasPrefix(text, "#") {
			if muted, ok := hexToMutedRGBA(value, 0.15); ok {
				text = muted
			}
		}
		out = append(out, "    "+pair[1]+": "+text+";")
	}
	return out
}

const glassOffCSS = `:root {
    --surface-glass-blur: 0px;
    --surface-glass-saturate: 1;
    --surface-glass-bg-secondary: var(--bg-secondary);
    --surface-glass-bg-tertiary: var(--bg-tertiary);
    --surface-glass-bg-elevated: var(--bg-elevated);
    --surface-glass-border: var(--border-primary);
    --card-bg: var(--bg-secondary);
    --sidebar-glass-bg-rail: var(--sidebar-rail-bg);
    --sidebar-glass-bg-flyout: var(--sidebar-flyout-bg);
}
`

// glassCSS are the custom properties of the frosted panel surfaces.
func glassCSS(branding *Object, themeMode string, colors *Object) string {
	if v, _ := branding.Get("glassEnabled"); v == "false" {
		return glassOffCSS
	}
	blur, ok := clampNumber(property(branding, "glassBlur"), 0, 40)
	if !ok {
		blur = 16
	}
	percent, ok := clampNumber(property(branding, "glassOpacity"), 0, 100)
	if !ok {
		percent = 55
	}
	opacity := percent / 100
	color := trim(toString(or(property(branding, "glassColor"), "")))
	if !reHashHex6.MatchString(color) {
		if fromColors := toString(or(property(colors, "bgSecondary"), "")); reHashHex6.MatchString(fromColors) {
			color = fromColors
		} else if themeMode == "light" {
			color = "#ffffff"
		} else {
			color = "#161b22"
		}
	}
	c, ok := hexToRGB(color)
	if !ok {
		return ""
	}
	rgba := func(alpha float64) string {
		return "rgba(" + numberString(float64(c.r)) + ", " + numberString(float64(c.g)) + ", " + numberString(float64(c.b)) + ", " + toFixed2(alpha) + ")"
	}
	return ":root {\n" +
		"    --surface-glass-blur: " + numberString(blur) + "px;\n" +
		"    --surface-glass-saturate: 1.2;\n" +
		"    --surface-glass-bg-secondary: " + rgba(opacity) + ";\n" +
		"    --surface-glass-bg-tertiary: " + rgba(math.Min(1, opacity+0.08)) + ";\n" +
		"    --surface-glass-bg-elevated: " + rgba(math.Min(1, opacity+0.15)) + ";\n" +
		"    --surface-glass-border: " + rgba(math.Min(1, opacity+0.35)) + ";\n" +
		"    --card-bg: var(--surface-glass-bg-secondary);\n" +
		"    --sidebar-glass-bg-rail: " + rgba(math.Max(0.32, math.Min(0.78, opacity-0.07))) + ";\n" +
		"    --sidebar-glass-bg-flyout: " + rgba(math.Max(0.42, math.Min(0.84, opacity+0.03))) + ";\n" +
		"}\n"
}

func sizeRule(size any) string {
	switch size {
	case "contain":
		return "background-size: contain; background-repeat: no-repeat; background-position: center;"
	case "repeat":
		return "background-repeat: repeat;"
	case "auto", "center":
		return "background-size: auto; background-repeat: no-repeat; background-position: center;"
	}
	return "background-size: cover; background-repeat: no-repeat; background-position: center;"
}

// overlayAlpha is the opacity of a dark overlay with two decimals. ok is
// false when the setting holds no number above zero.
func overlayAlpha(v any) (string, bool) {
	n, ok := clampNumber(v, 0, 95)
	if !ok || n == 0 {
		return "", false
	}
	return toFixed2(n / 100), true
}

// backgroundCSS paints the wallpapers: behind the console, on the sign-in
// page (or its promo pane) and behind the remote desktop page. The sign-in
// and remote backgrounds can take over the console's.
func backgroundCSS(branding *Object) string {
	get := func(key string) any { return property(branding, key) }
	var out strings.Builder

	consoleBg := buildBackgroundValue(get("bgType"), get("bgColor"), get("bgGradient"), get("bgImageUrl"))
	if consoleBg != "" {
		size := ""
		if get("bgType") == "image" {
			size = sizeRule(get("bgSize"))
		}
		out.WriteString("body.app-page::before {\n" +
			"    content: '';\n    position: fixed;\n    inset: 0;\n    z-index: 0;\n    pointer-events: none;\n" +
			"    background: " + consoleBg + ";\n    " + size + "\n")
		if blur, ok := clampNumber(get("bgBlur"), 0, 40); ok && blur != 0 {
			out.WriteString("    filter: blur(" + numberString(blur) + "px);\n    transform: scale(1.05);\n")
		}
		out.WriteString("}\n")
		if alpha, ok := overlayAlpha(get("bgOverlay")); ok {
			out.WriteString("body.app-page::after {\n" +
				"    content: '';\n    position: fixed;\n    inset: 0;\n    z-index: 0;\n" +
				"    background: rgba(0, 0, 0, " + alpha + ");\n    pointer-events: none;\n}\n")
		}
		out.WriteString("body.app-page { background-color: transparent; position: relative; isolation: isolate; }\n")
		out.WriteString("body.app-page .app-layout,\nbody.app-page .ux35-shell,\nbody.app-page #desktop-shell,\nbody.app-page #modal-container,\nbody.app-page #toast-container { position: relative; z-index: 1; }\n")
		out.WriteString("body.app-page .main-wrapper,\nbody.app-page .app-layout,\nbody.app-page .ux35-shell { background: transparent; }\n")
		out.WriteString("body.app-page .main-content,\nbody.app-page .ux35-content { background: transparent; }\n")
	}

	loginType := get("loginBgType")
	ownLogin := Truthy(loginType) && loginType != "none"
	switch normalizeLoginPromoMode(get("loginPromoMode")) {
	case "classic":
		loginBg := ""
		var overlay any
		if loginType == "inherit" {
			loginBg, overlay = consoleBg, get("bgOverlay")
		} else if ownLogin {
			loginBg = buildBackgroundValue(loginType, get("loginBgColor"), get("loginBgGradient"), get("loginBgImageUrl"))
			overlay = get("loginBgOverlay")
		}
		alpha, hasOverlay := "", false
		if loginType == "inherit" || ownLogin {
			alpha, hasOverlay = overlayAlpha(overlay)
		}
		if loginBg != "" {
			out.WriteString("body.login-page::before {\n" +
				"    content: '';\n    position: fixed;\n    inset: 0;\n    z-index: -2;\n" +
				"    background: " + loginBg + ";\n    background-size: cover;\n    background-position: center;\n    background-repeat: no-repeat;\n}\n")
			if hasOverlay {
				out.WriteString("body.login-page::after {\n" +
					"    content: '';\n    position: fixed;\n    inset: 0;\n    z-index: -1;\n" +
					"    background: rgba(0, 0, 0, " + alpha + ");\n    pointer-events: none;\n}\n")
			}
			out.WriteString("body.login-page { background-color: transparent; }\n")
		}
	case "single":
		promoBg := ""
		if loginType == "inherit" {
			promoBg = consoleBg
		} else if ownLogin {
			promoBg = buildBackgroundValue(loginType, get("loginBgColor"), get("loginBgGradient"), get("loginBgImageUrl"))
		}
		if promoBg != "" {
			out.WriteString("body.login-page.login-layout-single .login-promo {\n    background: " + promoBg + ";\n    background-size: cover;\n    background-position: center;\n}\n")
		}
		overlay := get("loginBgOverlay")
		if loginType == "inherit" {
			overlay = get("bgOverlay")
		}
		if alpha, ok := overlayAlpha(overlay); ok {
			out.WriteString("body.login-page.login-layout-single .login-promo::after {\n" +
				"    content: '';\n    position: absolute;\n    inset: 0;\n    background: rgba(0, 0, 0, " + alpha + ");\n    pointer-events: none;\n}\n")
		}
	}

	rdType := or(get("rdclientBgType"), "inherit")
	rdBg := ""
	rdAlpha, rdOverlay := "", false
	if rdType == "inherit" {
		rdBg = consoleBg
		rdType = get("bgType")
		rdAlpha, rdOverlay = overlayAlpha(get("bgOverlay"))
	} else if rdType != "none" {
		rdBg = buildBackgroundValue(rdType, get("rdclientBgColor"), get("rdclientBgGradient"), get("rdclientBgImageUrl"))
		rdAlpha, rdOverlay = overlayAlpha(get("rdclientBgOverlay"))
	}
	if rdBg != "" {
		size := ""
		if rdType == "image" {
			size = sizeRule(or(get("rdclientBgSize"), get("bgSize")))
		}
		out.WriteString("body.rd-desk-body::before {\n" +
			"    content: '';\n    position: fixed;\n    inset: 0;\n    z-index: 0;\n    pointer-events: none;\n" +
			"    background: " + rdBg + ";\n    " + size + "\n}\n")
		if rdOverlay {
			out.WriteString("body.rd-desk-body::after {\n" +
				"    content: '';\n    position: fixed;\n    inset: 0;\n    z-index: 0;\n" +
				"    background: rgba(0, 0, 0, " + rdAlpha + ");\n    pointer-events: none;\n}\n")
		}
		out.WriteString("body.rd-desk-body { background-color: transparent; position: relative; isolation: isolate; }\n")
		out.WriteString("body.rd-desk-body .rd-desk-app { position: relative; z-index: 1; }\n")
	}
	return out.String()
}

// GenerateThemeCSS returns the stylesheet /css/branding.css serves for
// the current settings: the font rules, the palette as custom properties
// (with the dark palette under prefers-color-scheme when the theme follows
// the system), color-scheme, the alias and glass properties, the
// wallpapers, and the administrator's own CSS at the end.
//
// It fails when a chosen font has a name without any ASCII letter or
// digit; the route then answers with a comment instead of a stylesheet.
func (s *Service) GenerateThemeCSS() (css string, err error) {
	defer catch(&err)
	branding := s.GetBranding()
	themeMode := normalizeThemeMode(property(branding, "themeMode"))
	colors := s.resolveThemeColors(branding)
	overrides := paletteDeclarations(colors)

	var out strings.Builder
	if fontCSS := s.generateFontCSS(property(branding, "fontHeading"), property(branding, "fontBody")); fontCSS != "" {
		out.WriteString(fontCSS + "\n")
	}
	if len(overrides) > 0 {
		out.WriteString(":root {\n" + strings.Join(overrides, "\n") + "\n}\n")
	}
	if themeMode == "system" {
		dark := paletteDeclarations(s.palette("dark"))
		out.WriteString("@media (prefers-color-scheme: dark) {\n:root {\n" + strings.Join(dark, "\n") + "\n}\n}\n")
	}
	out.WriteString("html { color-scheme: " + colorSchemeFor(themeMode) + "; }\n")
	out.WriteString(semanticAliasCSS(themeMode))
	out.WriteString(glassCSS(branding, themeMode, colors))
	out.WriteString(backgroundCSS(branding))
	if custom := sanitizeCustomCSS(property(branding, "customCss")); trim(custom) != "" {
		out.WriteString("\n/* --- custom branding CSS --- */\n" + custom + "\n")
	}
	return out.String(), nil
}
