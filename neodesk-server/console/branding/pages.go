package branding

import "strconv"

// What the sign-in page and the console's bootstrap send of the branding.
// Both read the cache only.

// orEmpty is the value of a setting, and "" when it is empty or missing.
func orEmpty(b *Object, key string) any {
	return or(property(b, key), "")
}

// showPoweredBy is String(value) !== 'false' for the setting of that name.
func showPoweredBy(b *Object) bool {
	return b.Text("showPoweredBy") != "false"
}

// AppName returns the name of the console: the appName setting, and
// fallback when that is empty.
func (s *Service) AppName(fallback string) string {
	if name := s.GetBranding().Text("appName"); name != "" {
		return name
	}
	return fallback
}

// SignInBranding returns the "branding" object of GET
// /api/auth/login-page: the logo, the sign-in texts, the layout
// (loginPromoMode: classic, single or slides), the slides when the layout
// is slides (an empty list otherwise, each slide with its overlay as a
// number), the footer text and showPoweredBy as a boolean.
func (s *Service) SignInBranding() *Object {
	b := s.GetBranding()
	// the cache holds one of classic, single and slides
	mode := b.Text("loginPromoMode")
	slides := []any{}
	if list, ok := property(b, "loginSlides").([]any); ok && mode == "slides" {
		for _, slide := range list {
			text := func(key string) string {
				value, _ := Field(slide, key).(string)
				return value
			}
			// the overlay is stored as the text of a number, or "null"
			overlay, err := strconv.ParseFloat(text("overlay"), 64)
			if err != nil {
				overlay = 0
			}
			slides = append(slides, object(
				"id", text("id"),
				"slideKey", text("slideKey"),
				"imageUrl", text("imageUrl"),
				"title", text("title"),
				"body", text("body"),
				"overlay", overlay,
			))
		}
	}
	return object(
		"logoType", or(property(b, "logoType"), "default"),
		"logoSvg", orEmpty(b, "logoSvg"),
		"logoUrl", orEmpty(b, "logoUrl"),
		"logoText", orEmpty(b, "logoText"),
		"logoTextAccent", orEmpty(b, "logoTextAccent"),
		"loginTitle", orEmpty(b, "loginTitle"),
		"loginSubtitle", orEmpty(b, "loginSubtitle"),
		"loginPromoMode", mode,
		"loginSlides", slides,
		"footerText", orEmpty(b, "footerText"),
		"showPoweredBy", showPoweredBy(b),
	)
}

// ThemeMode returns the themeMode setting, and "system" when it is empty:
// what /console-boot.js and the bootstrap send as themeMode.
func (s *Service) ThemeMode() string {
	if mode := s.GetBranding().Text("themeMode"); mode != "" {
		return mode
	}
	return "system"
}

// BootPalettes returns the built-in palettes as /console-boot.js and the
// bootstrap send them: {light, dark}.
func (s *Service) BootPalettes() *Object {
	return object("light", s.palette("light"), "dark", s.palette("dark"))
}

// BootstrapBranding returns the "branding" object of GET
// /api/console/bootstrap. fallbackAppName is the configured name of the
// console, used when the appName setting is empty.
func (s *Service) BootstrapBranding(fallbackAppName string) *Object {
	b := s.GetBranding()
	return object(
		"appName", s.AppName(fallbackAppName),
		"appDescription", orEmpty(b, "appDescription"),
		"themeMode", s.ThemeMode(),
		"logoType", or(property(b, "logoType"), nil),
		"logoIcon", orEmpty(b, "logoIcon"),
		"logoSvg", orEmpty(b, "logoSvg"),
		"logoUrl", orEmpty(b, "logoUrl"),
		"logoText", orEmpty(b, "logoText"),
		"logoTextAccent", orEmpty(b, "logoTextAccent"),
		"footerText", orEmpty(b, "footerText"),
		"showPoweredBy", showPoweredBy(b),
		"revision", s.Revision(),
	)
}
