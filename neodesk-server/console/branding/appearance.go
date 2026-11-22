package branding

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

// FlattenAppearanceInput turns settings in either shape into the flat one
// that is stored. The nested shape groups the settings in sections
// (identity, palette, login ...), optionally under "appearance"; flat keys
// beside the sections win. The result always carries
// appearanceSchemaVersion "2", unless the input is no object at all.
func FlattenAppearanceInput(input any) (out *Object, err error) {
	defer catch(&err)
	return flattenAppearanceInput(input), nil
}

func flattenAppearanceInput(input any) *Object {
	flat := NewObject()
	if !Truthy(input) || !isObjectLike(input) {
		return flat
	}
	source := input
	if nested := property(input, "appearance"); Truthy(nested) && isObjectLike(nested) {
		source = nested
	}
	for _, section := range appearanceSections {
		data := property(source, section.name)
		if !Truthy(data) || !isObjectLike(data) {
			continue
		}
		for _, key := range section.keys {
			if hasOwn(data, key) {
				flat.Set(key, property(data, key))
			}
		}
	}
	for _, pair := range defaultSettings {
		if key := pair[0]; hasOwn(input, key) {
			flat.Set(key, property(input, key))
		}
	}
	if palette := property(source, "palette"); Truthy(palette) && isObjectLike(palette) {
		if colors := property(palette, "colors"); Truthy(colors) && isObjectLike(colors) {
			flat.Set("colors", colors)
		}
		if semantic := property(palette, "semantic"); Truthy(semantic) && isObjectLike(semantic) {
			merged := NewObject()
			if colors, ok := flat.Get("colors"); ok && Truthy(colors) {
				spread(merged, colors)
			}
			named := NewObject()
			entries(semantic, func(key string, value any) {
				if legacy := semanticAlias(key); legacy != "" {
					named.Set(legacy, value)
				}
			})
			flat.Set("colors", spread(merged, named))
		}
	}
	flat.Set("appearanceSchemaVersion", "2")
	for _, key := range []string{"bgSize", "rdclientBgSize"} {
		if size, ok := flat.Get(key); ok && Truthy(size) {
			flat.Set(key, normalizeBackgroundSize(size))
		}
	}
	return flat
}

// semanticAlias returns the palette colour a semantic name stands for,
// and "" for an unknown name.
func semanticAlias(name string) string {
	for _, pair := range semanticColorAliases {
		if pair[0] == name {
			return pair[1]
		}
	}
	return objectPrototype[name]
}

func (s *Service) palette(mode string) *Object {
	p, _ := s.palettes.Get(mode)
	out := NewObject()
	spread(out, p)
	return out
}

// GetThemePalettes returns the built-in dark and light palettes, as new
// objects.
func (s *Service) GetThemePalettes() *Object {
	return object("dark", s.palette("dark"), "light", s.palette("light"))
}

// ResolveThemeColors returns the colours the stylesheet uses for the
// settings: the built-in light palette for light and system, the built-in
// dark one for dark, and for custom the dark palette with the stored
// colours on top.
func (s *Service) ResolveThemeColors(branding any) (out *Object, err error) {
	defer catch(&err)
	return s.resolveThemeColors(branding), nil
}

func (s *Service) resolveThemeColors(branding any) *Object {
	var mode, stored any = Undefined, Undefined
	if Truthy(branding) {
		mode = property(branding, "themeMode")
	}
	switch normalizeThemeMode(mode) {
	case "light", "system":
		return s.palette("light")
	case "dark":
		return s.palette("dark")
	}
	if Truthy(branding) {
		stored = property(branding, "colors")
	}
	merged := s.palette("dark")
	entries(stored, func(key string, value any) {
		if !Truthy(value) || key == "__proto__" {
			return
		}
		if text := trim(toString(value)); text != "" {
			merged.Set(key, text)
		}
	})
	return merged
}

var reHex6 = regexp.MustCompile(`^[0-9a-fA-F]{6}$`)

// HexToMutedRGBA turns #RRGGBB (the # is optional) into a translucent
// rgba() with the given alpha. ok is false for anything else.
func HexToMutedRGBA(hex any, alpha float64) (out string, ok bool, err error) {
	defer catch(&err)
	out, ok = hexToMutedRGBA(hex, alpha)
	return out, ok, nil
}

func hexToMutedRGBA(hex any, alpha float64) (string, bool) {
	if hex == nil || hex == Undefined {
		return "", false
	}
	raw := strings.TrimPrefix(trim(toString(hex)), "#")
	if !reHex6.MatchString(raw) {
		return "", false
	}
	if math.IsNaN(alpha) || math.IsInf(alpha, 0) {
		alpha = 0.15
	}
	r, _ := strconv.ParseUint(raw[0:2], 16, 8)
	g, _ := strconv.ParseUint(raw[2:4], 16, 8)
	b, _ := strconv.ParseUint(raw[4:6], 16, 8)
	return "rgba(" + strconv.Itoa(int(r)) + ", " + strconv.Itoa(int(g)) + ", " + strconv.Itoa(int(b)) + ", " + numberString(alpha) + ")", true
}

type rgb struct{ r, g, b int }

// hexToRGB reads a six digit hex colour; the first # anywhere in it is
// ignored.
func hexToRGB(hex any) (rgb, bool) {
	h := trim(strings.Replace(toString(or(hex, "")), "#", "", 1))
	if !reHex6.MatchString(h) {
		return rgb{}, false
	}
	r, _ := strconv.ParseUint(h[0:2], 16, 8)
	g, _ := strconv.ParseUint(h[2:4], 16, 8)
	b, _ := strconv.ParseUint(h[4:6], 16, 8)
	return rgb{int(r), int(g), int(b)}, true
}

func luminance(c rgb) float64 {
	return float64(0.2126*luminanceComponent[c.r]) + float64(0.7152*luminanceComponent[c.g]) + float64(0.0722*luminanceComponent[c.b])
}

// contrastRatio is the WCAG contrast of two colours. ok is false when one
// of them is no six digit hex colour.
func contrastRatio(hexA, hexB any) (float64, bool) {
	a, okA := hexToRGB(hexA)
	b, okB := hexToRGB(hexB)
	if !okA || !okB {
		return 0, false
	}
	lumA, lumB := luminance(a), luminance(b)
	lighter, darker := lumA, lumB
	if lumB > lumA {
		lighter, darker = lumB, lumA
	}
	return (lighter + 0.05) / (darker + 0.05), true
}

// backgroundSpec is one of the login, agent and rdclient backgrounds of
// the appearance model.
func backgroundSpec(branding any, prefix, defaultType string) *Object {
	return object(
		"type", or(property(branding, prefix+"BgType"), defaultType),
		"color", or(property(branding, prefix+"BgColor"), ""),
		"gradient", or(property(branding, prefix+"BgGradient"), ""),
		"imageUrl", or(property(branding, prefix+"BgImageUrl"), ""),
		"overlay", or(property(branding, prefix+"BgOverlay"), ""),
		"size", normalizeBackgroundSize(or(property(branding, prefix+"BgSize"), "cover")),
	)
}

// GetAppearanceModel returns the settings in the nested shape (version
// "2.0") the settings page works with. A branding that is nil, or empty in
// JavaScript's eyes, stands for the current settings.
func (s *Service) GetAppearanceModel(branding any) (out *Object, err error) {
	defer catch(&err)
	return s.appearanceModel(branding), nil
}

func (s *Service) appearanceModel(input any) *Object {
	branding := input
	if !Truthy(branding) {
		branding = s.GetBranding()
	}
	get := func(key string) any { return property(branding, key) }
	notFalse := func(key string) bool { return get(key) != "false" }

	colors := get("colors")
	semantic := NewObject()
	for _, pair := range semanticColorAliases {
		var value any = Undefined
		if colors != nil && colors != Undefined {
			value = property(colors, pair[1])
		}
		semantic.Set(pair[0], or(value, ""))
	}
	out := object(
		"version", "2.0",
		"type", "betterdesk-appearance",
		"revision", s.Revision(),
		"identity", object(
			"appName", get("appName"),
			"appDescription", get("appDescription"),
			"logoType", get("logoType"),
			"logoIcon", get("logoIcon"),
			"logoSvg", get("logoSvg"),
			"logoUrl", get("logoUrl"),
			"logoText", get("logoText"),
			"logoTextAccent", get("logoTextAccent"),
			"faviconSvg", get("faviconSvg"),
		),
		"typography", object(
			"fontHeading", get("fontHeading"),
			"fontBody", get("fontBody"),
		),
		"palette", object(
			"mode", or(get("themeMode"), "dark"),
			"colors", spread(NewObject(), or(colors, NewObject())),
			"semantic", semantic,
		),
		"backgrounds", object(
			"console", object(
				"type", or(get("bgType"), "none"),
				"color", or(get("bgColor"), ""),
				"gradient", or(get("bgGradient"), ""),
				"imageUrl", or(get("bgImageUrl"), ""),
				"blur", or(get("bgBlur"), ""),
				"overlay", or(get("bgOverlay"), ""),
				"size", normalizeBackgroundSize(or(get("bgSize"), "cover")),
			),
			"login", backgroundSpec(branding, "login", "inherit"),
			"agent", backgroundSpec(branding, "agent", "none"),
			"rdclient", backgroundSpec(branding, "rdclient", "inherit"),
		),
		"surfaces", object(
			"glassEnabled", notFalse("glassEnabled"),
			"glassColor", get("glassColor"),
			"glassBlur", get("glassBlur"),
			"glassOpacity", get("glassOpacity"),
		),
	)
	promoMode := normalizeLoginPromoMode(get("loginPromoMode"))
	slides := get("loginSlides")
	if _, isArray := slides.([]any); !isArray {
		slides = sanitizeLoginSlides(slides)
	}
	out.Set("login", object(
		"title", get("loginTitle"),
		"subtitle", get("loginSubtitle"),
		"promoMode", promoMode,
		"slides", slides,
	))
	out.Set("footer", object(
		"text", get("footerText"),
		"showPoweredBy", notFalse("showPoweredBy"),
		"agentShowPoweredBy", notFalse("agentShowPoweredBy"),
	))
	customCSS := false
	if css := get("customCss"); Truthy(css) {
		text, isString := css.(string)
		if !isString {
			throwType("branding.customCss.trim is not a function")
		}
		customCSS = trim(text) != ""
	}
	out.Set("advanced", object("customCssEnabled", customCSS))
	return out
}

// AssessAppearanceReadability checks the contrast of text on its
// backgrounds and whether picture and gradient backgrounds have enough
// overlay. The answer is {ok, issues}; ok is false when an issue is an
// error. A branding that is nil stands for the current settings.
func (s *Service) AssessAppearanceReadability(branding any) (out *Object, err error) {
	defer catch(&err)
	return s.assessReadability(branding), nil
}

func (s *Service) assessReadability(input any) *Object {
	branding := input
	if !Truthy(branding) {
		branding = s.GetBranding()
	}
	get := func(key string) any { return property(branding, key) }
	colors := NewObject()
	for _, key := range colorKeys {
		colors.Set(key, "")
	}
	spread(colors, or(get("colors"), NewObject()))
	color := func(key string) any { return property(colors, key) }

	issues := []any{}
	ok := true
	checkPair := func(id string, foreground, background any, minimum float64) {
		ratio, valid := contrastRatio(foreground, background)
		if !valid || ratio >= minimum {
			return
		}
		severity := "warning"
		if ratio < 3 {
			severity = "error"
			ok = false
		}
		fixed := toFixed2(ratio)
		rounded, _ := strconv.ParseFloat(fixed, 64)
		issues = append(issues, object(
			"id", id,
			"severity", severity,
			"ratio", rounded,
			"minimum", minimum,
			"messageKey", "branding.readability_contrast",
			"messageParams", object("item", id, "ratio", fixed, "minimum", minimum),
			"message", id+" contrast ratio "+fixed+" is below "+numberString(minimum)+":1",
		))
	}
	checkPair("page-text", color("textPrimary"), color("bgPrimary"), 4.5)
	checkPair("card-text", color("textPrimary"), color("bgSecondary"), 4.5)
	checkPair("muted-text", color("textSecondary"), color("bgSecondary"), 3)
	checkPair("button-primary", "#ffffff", color("accentBlue"), 3)

	for _, item := range []struct {
		id            string
		kind, overlay any
	}{
		{"console-background", get("bgType"), get("bgOverlay")},
		{"login-background", get("loginBgType"), or(get("loginBgOverlay"), get("bgOverlay"))},
		{"rdclient-background", get("rdclientBgType"), or(get("rdclientBgOverlay"), get("bgOverlay"))},
	} {
		if item.kind != "image" && item.kind != "gradient" {
			continue
		}
		if overlay, _ := clampNumber(item.overlay, 0, 95); overlay < 25 {
			kind := item.kind.(string)
			issues = append(issues, object(
				"id", item.id,
				"severity", "warning",
				"messageKey", "branding.readability_background",
				"messageParams", object("item", item.id, "type", kind),
				"message", item.id+" uses "+kind+" with a low overlay; text may be hard to read.",
			))
		}
	}
	return object("ok", ok, "issues", issues)
}

// GetPublicAppearance returns the part of the current appearance that
// clients may read without signing in: identity, a small palette, the
// glass settings, one background and the readability check.
//
// It fails with a TypeError when a stored colour is an object that cannot
// be turned into text, which only a row written past SaveBranding can be.
func (s *Service) GetPublicAppearance() (out *Object, err error) {
	defer catch(&err)
	return s.publicAppearance(), nil
}

func (s *Service) publicAppearance() *Object {
	branding := s.GetBranding()
	appearance := s.appearanceModel(branding)
	sub := func(v any, keys ...string) any {
		for _, key := range keys {
			v = property(v, key)
		}
		return v
	}
	identity := func(key string) any { return sub(appearance, "identity", key) }
	colors := or(sub(appearance, "palette", "colors"), NewObject())
	color := func(key, fallback string) any { return or(property(colors, key), fallback) }
	only := func(logoType, key string) any {
		if identity("logoType") == logoType {
			return identity(key)
		}
		return ""
	}
	background := sub(appearance, "backgrounds", "rdclient")
	if property(background, "type") == "inherit" {
		background = sub(appearance, "backgrounds", "console")
	}
	bg := func(key, fallback string) any { return or(property(background, key), fallback) }

	return object(
		"version", "2.0",
		"revision", s.Revision(),
		"product", "betterdesk-appearance",
		"identity", object(
			"appName", or(identity("appName"), "BetterDesk"),
			"appDescription", or(identity("appDescription"), ""),
			"logoType", or(identity("logoType"), "icon"),
			"logoIcon", or(identity("logoIcon"), "dns"),
			"logoUrl", only("image", "logoUrl"),
			"logoText", only("text", "logoText"),
			"logoTextAccent", only("text", "logoTextAccent"),
		),
		"palette", object(
			"mode", or(sub(appearance, "palette", "mode"), "dark"),
			"primary", color("accentBlue", "#58a6ff"),
			"primaryHover", color("accentBlueHover", "#79c0ff"),
			"background", color("bgPrimary", "#0d1117"),
			"surface", color("bgSecondary", "#161b22"),
			"surfaceRaised", color("bgTertiary", "#21262d"),
			"text", color("textPrimary", "#e6edf3"),
			"muted", color("textSecondary", "#8b949e"),
			"border", color("borderPrimary", "#30363d"),
			"danger", color("accentRed", "#f85149"),
			"warning", color("accentYellow", "#d29922"),
			"success", color("accentGreen", "#2ea44f"),
		),
		"surfaces", object(
			"glassEnabled", sub(appearance, "surfaces", "glassEnabled"),
			"glassBlur", sub(appearance, "surfaces", "glassBlur"),
			"glassOpacity", sub(appearance, "surfaces", "glassOpacity"),
		),
		"background", object(
			"type", bg("type", "none"),
			"color", bg("color", ""),
			"gradient", bg("gradient", ""),
			"imageUrl", bg("imageUrl", ""),
			"overlay", bg("overlay", ""),
			"size", normalizeBackgroundSize(bg("size", "cover")),
		),
		"readability", s.assessReadability(branding),
	)
}
