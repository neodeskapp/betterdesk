package branding

import (
	"strconv"
	"strings"
)

var builtinSlideKeys = []string{"free", "remote", "secure", "platforms"}

// DefaultLoginSlides returns the four built-in sign-in slides.
func DefaultLoginSlides() []any {
	out := make([]any, len(builtinSlideKeys))
	for i, key := range builtinSlideKeys {
		out[i] = object(
			"id", key,
			"slideKey", key,
			"order", float64(i),
			"imageUrl", "/img/login-slides/"+key+".jpg",
			"title", "",
			"body", "",
			"overlay", "45",
		)
	}
	return out
}

// NormalizeLoginPromoMode returns the sign-in layout: classic, single or
// slides, and classic for anything else.
func NormalizeLoginPromoMode(mode any) (out string, err error) {
	defer catch(&err)
	return normalizeLoginPromoMode(mode), nil
}

func normalizeLoginPromoMode(mode any) string {
	switch m := toLower(trim(toString(or(mode, "")))); m {
	case "classic", "single", "slides":
		return m
	}
	return "classic"
}

// ResolveLoginPromoMode returns the layout for a stored mode. A console
// that never saved one shows the plain sign-in form.
func ResolveLoginPromoMode(storedMode any, hadExplicitMode bool) (out string, err error) {
	defer catch(&err)
	if hadExplicitMode {
		return normalizeLoginPromoMode(storedMode), nil
	}
	return "classic", nil
}

// NormalizeThemeMode returns system, dark, light or custom. The old value
// auto and everything unknown become dark.
func NormalizeThemeMode(mode any) (out string, err error) {
	defer catch(&err)
	return normalizeThemeMode(mode), nil
}

func normalizeThemeMode(mode any) string {
	switch m := toString(or(mode, "dark")); m {
	case "system", "dark", "light", "custom":
		return m
	}
	return "dark"
}

// colorSchemeFor is the CSS color-scheme value of a theme mode.
func colorSchemeFor(mode string) string {
	switch mode {
	case "system":
		return "light dark"
	case "light":
		return "light"
	}
	return "dark"
}

// keepRunes drops every character keep does not accept. Halves of a
// broken pair never pass, as in JavaScript.
func keepRunes(s string, keep func(r rune) bool) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x80 && keep(rune(c)) {
			b.WriteByte(c)
		}
	}
	return b.String()
}

func isSlideKeyRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-'
}

func isSlideIDRune(r rune) bool {
	return isSlideKeyRune(r) || r >= 'A' && r <= 'Z'
}

// SanitizeLoginSlides cleans a list of sign-in slides: at most
// MaxLoginSlides, each with a safe id, key, picture address, texts of
// limited length and an overlay from 0 to 90. Text is read as JSON first.
// Anything that is no list, an empty list, and a list without a usable
// slide give the built-in slides.
func SanitizeLoginSlides(raw any) (out []any, err error) {
	defer catch(&err)
	return sanitizeLoginSlides(raw), nil
}

func sanitizeLoginSlides(raw any) []any {
	list := raw
	if text, ok := list.(string); ok {
		parsed, err := parseJSON(text)
		if err != nil {
			return DefaultLoginSlides()
		}
		list = parsed
	}
	items, ok := list.([]any)
	if !ok || len(items) == 0 {
		return DefaultLoginSlides()
	}
	out := []any{}
	for i := 0; i < len(items) && len(out) < MaxLoginSlides; i++ {
		item := items[i]
		if !Truthy(item) || !isObjectLike(item) {
			continue
		}
		imageURL, valid := validateBrandingURL(property(item, "imageUrl"))
		if !valid {
			continue
		}
		fallbackID := "slide-" + strconv.Itoa(i+1)
		slideKey := keepRunes(toLower(trim(toString(or(property(item, "slideKey"), "")))), isSlideKeyRune)
		if len(slideKey) > 32 {
			slideKey = ""
		}
		idRaw := substring(trim(toString(or(or(property(item, "id"), slideKey), fallbackID))), 64)
		id := keepRunes(idRaw, isSlideIDRune)
		if id == "" {
			id = fallbackID
		}
		overlay := "null"
		if n, ok := clampNumber(property(item, "overlay"), 0, 90); ok {
			overlay = numberString(n)
		}
		out = append(out, object(
			"id", id,
			"slideKey", slideKey,
			"order", float64(len(out)),
			"imageUrl", imageURL,
			"title", substring(toString(or(property(item, "title"), "")), 120),
			"body", substring(toString(or(property(item, "body"), "")), 400),
			"overlay", overlay,
		))
	}
	if len(out) == 0 {
		return DefaultLoginSlides()
	}
	return out
}

// UsesDefaultLogo reports whether the console shows the BetterDesk logo:
// logoType "default", or what older versions stored as the default (the
// icon "dns", the old logo image, or a type whose content is empty).
func UsesDefaultLogo(branding any) (out bool, err error) {
	defer catch(&err)
	return usesDefaultLogo(branding), nil
}

func usesDefaultLogo(branding any) bool {
	b := or(branding, NewObject())
	logoType := toString(or(property(b, "logoType"), ""))
	if logoType == "default" {
		return true
	}
	if logoType == "text" && Truthy(property(b, "logoText")) {
		return false
	}
	if logoType == "svg" && Truthy(property(b, "logoSvg")) {
		return false
	}
	if logoURL := property(b, "logoUrl"); logoType == "image" && Truthy(logoURL) {
		return logoURL == LegacyDefaultLogoURL
	}
	icon := trim(toString(or(property(b, "logoIcon"), "")))
	return icon == "" || icon == "dns"
}
