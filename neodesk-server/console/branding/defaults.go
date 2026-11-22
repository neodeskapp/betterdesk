package branding

// MaxLoginSlides is the most slides the sign-in page shows.
const MaxLoginSlides = 10

// LegacyDefaultLogoURL is the logo image older versions stored as the
// default. It counts as "no logo of your own".
const LegacyDefaultLogoURL = "/img/betterdesk_icon.png"

// colorKeys are the colours of a palette, in the order they are stored and
// written.
var colorKeys = []string{
	"bgPrimary", "bgSecondary", "bgTertiary", "bgElevated",
	"textPrimary", "textSecondary",
	"accentBlue", "accentBlueHover", "accentBlueMuted",
	"accentGreen", "accentGreenHover", "accentGreenMuted",
	"accentRed", "accentRedHover", "accentRedMuted",
	"accentYellow", "accentYellowHover", "accentYellowMuted",
	"accentPurple", "accentPurpleHover", "accentPurpleMuted",
	"borderPrimary", "borderSecondary",
}

// colorCSSVars pairs each palette colour with its CSS custom property.
var colorCSSVars = [][2]string{
	{"bgPrimary", "--bg-primary"},
	{"bgSecondary", "--bg-secondary"},
	{"bgTertiary", "--bg-tertiary"},
	{"bgElevated", "--bg-elevated"},
	{"textPrimary", "--text-primary"},
	{"textSecondary", "--text-secondary"},
	{"accentBlue", "--accent-blue"},
	{"accentBlueHover", "--accent-blue-hover"},
	{"accentBlueMuted", "--accent-blue-muted"},
	{"accentGreen", "--accent-green"},
	{"accentGreenHover", "--accent-green-hover"},
	{"accentGreenMuted", "--accent-green-muted"},
	{"accentRed", "--accent-red"},
	{"accentRedHover", "--accent-red-hover"},
	{"accentRedMuted", "--accent-red-muted"},
	{"accentYellow", "--accent-yellow"},
	{"accentYellowHover", "--accent-yellow-hover"},
	{"accentYellowMuted", "--accent-yellow-muted"},
	{"accentPurple", "--accent-purple"},
	{"accentPurpleHover", "--accent-purple-hover"},
	{"accentPurpleMuted", "--accent-purple-muted"},
	{"borderPrimary", "--border-primary"},
	{"borderSecondary", "--border-secondary"},
}

// ColorToCSSVar returns the CSS custom property of every palette colour,
// in palette order.
func ColorToCSSVar() *Object {
	o := NewObject()
	for _, pair := range colorCSSVars {
		o.Set(pair[0], pair[1])
	}
	return o
}

// semanticColorAliases pairs the names of the appearance model with the
// palette colours they stand for.
var semanticColorAliases = [][2]string{
	{"primary", "accentBlue"},
	{"primaryHover", "accentBlueHover"},
	{"primaryMuted", "accentBlueMuted"},
	{"success", "accentGreen"},
	{"successHover", "accentGreenHover"},
	{"successMuted", "accentGreenMuted"},
	{"danger", "accentRed"},
	{"dangerHover", "accentRedHover"},
	{"dangerMuted", "accentRedMuted"},
	{"warning", "accentYellow"},
	{"warningHover", "accentYellowHover"},
	{"warningMuted", "accentYellowMuted"},
	{"info", "accentPurple"},
	{"infoHover", "accentPurpleHover"},
	{"infoMuted", "accentPurpleMuted"},
	{"surface", "bgSecondary"},
	{"surfaceRaised", "bgTertiary"},
	{"surfaceElevated", "bgElevated"},
	{"textPrimary", "textPrimary"},
	{"textMuted", "textSecondary"},
	{"border", "borderPrimary"},
}

// appearanceSections lists, for each section of the nested appearance
// shape, the flat settings it holds.
var appearanceSections = []struct {
	name string
	keys []string
}{
	{"identity", []string{"appName", "appDescription", "logoType", "logoIcon", "logoSvg", "logoUrl", "logoText", "logoTextAccent", "faviconSvg"}},
	{"typography", []string{"fontHeading", "fontBody"}},
	{"palette", []string{"themeMode", "colors"}},
	{"console", []string{"bgType", "bgColor", "bgGradient", "bgImageUrl", "bgBlur", "bgOverlay", "bgSize"}},
	{"surfaces", []string{"glassEnabled", "glassColor", "glassBlur", "glassOpacity"}},
	{"login", []string{"loginBgType", "loginBgColor", "loginBgGradient", "loginBgImageUrl", "loginBgOverlay", "loginTitle", "loginSubtitle", "loginPromoMode", "loginSlides"}},
	{"agent", []string{"agentBgType", "agentBgColor", "agentBgGradient", "agentBgImageUrl", "agentShowPoweredBy"}},
	{"rdclient", []string{"rdclientBgType", "rdclientBgColor", "rdclientBgGradient", "rdclientBgImageUrl", "rdclientBgOverlay", "rdclientBgSize"}},
	{"footer", []string{"footerText", "showPoweredBy"}},
	{"advanced", []string{"customCss"}},
}

// defaultSettings are the settings of an untouched console, in the order
// the console lists them. "loginSlides" and "colors" are filled in by
// DefaultBranding.
var defaultSettings = [][2]string{
	{"appName", "BetterDesk"},
	{"appDescription", "BetterDesk Server Management"},
	{"logoType", "default"},
	{"logoIcon", "dns"},
	{"logoSvg", ""},
	{"logoUrl", ""},
	{"logoText", ""},
	{"logoTextAccent", ""},
	{"fontHeading", ""},
	{"fontBody", ""},
	{"faviconSvg", ""},
	{"bgType", "none"},
	{"bgColor", ""},
	{"bgGradient", ""},
	{"bgImageUrl", ""},
	{"bgBlur", ""},
	{"bgOverlay", ""},
	{"bgSize", "cover"},
	{"glassEnabled", "false"},
	{"glassColor", "#161b22"},
	{"glassBlur", "16"},
	{"glassOpacity", "55"},
	{"loginBgType", "inherit"},
	{"loginBgColor", ""},
	{"loginBgGradient", ""},
	{"loginBgImageUrl", ""},
	{"loginBgOverlay", ""},
	{"loginTitle", ""},
	{"loginSubtitle", ""},
	{"loginPromoMode", "classic"},
	{"loginSlides", ""},
	{"footerText", ""},
	{"showPoweredBy", "true"},
	{"agentBgType", "none"},
	{"agentBgColor", ""},
	{"agentBgGradient", ""},
	{"agentBgImageUrl", ""},
	{"agentShowPoweredBy", "true"},
	{"customCss", ""},
	{"appearanceSchemaVersion", "2"},
	{"themeMode", "system"},
	{"colors", ""},
	{"rdclientBgType", "inherit"},
	{"rdclientBgColor", ""},
	{"rdclientBgGradient", ""},
	{"rdclientBgImageUrl", ""},
	{"rdclientBgOverlay", ""},
	{"rdclientBgSize", "cover"},
}

// isSetting reports whether key is one of the settings.
var isSetting = func() map[string]bool {
	m := make(map[string]bool, len(defaultSettings))
	for _, pair := range defaultSettings {
		m[pair[0]] = true
	}
	return m
}()

// DefaultBranding returns the settings of an untouched console as a new
// object: every setting a string, "colors" an object of empty strings and
// "loginSlides" the four built-in slides.
func DefaultBranding() *Object {
	o := NewObject()
	for _, pair := range defaultSettings {
		switch pair[0] {
		case "loginSlides":
			o.Set("loginSlides", DefaultLoginSlides())
		case "colors":
			colors := NewObject()
			for _, key := range colorKeys {
				colors.Set(key, "")
			}
			o.Set("colors", colors)
		default:
			o.Set(pair[0], pair[1])
		}
	}
	return o
}

// objectPrototype lists the names every JavaScript object answers to
// without having them as keys. Code that asks "is this name in the
// object" or looks a name up in a table gets a yes for these, and the text
// below when the found value is used as a key.
var objectPrototype = map[string]string{
	"constructor":          "function Object() { [native code] }",
	"__defineGetter__":     "function __defineGetter__() { [native code] }",
	"__defineSetter__":     "function __defineSetter__() { [native code] }",
	"hasOwnProperty":       "function hasOwnProperty() { [native code] }",
	"__lookupGetter__":     "function __lookupGetter__() { [native code] }",
	"__lookupSetter__":     "function __lookupSetter__() { [native code] }",
	"isPrototypeOf":        "function isPrototypeOf() { [native code] }",
	"propertyIsEnumerable": "function propertyIsEnumerable() { [native code] }",
	"toString":             "function toString() { [native code] }",
	"valueOf":              "function valueOf() { [native code] }",
	"__proto__":            "[object Object]",
	"toLocaleString":       "function toLocaleString() { [native code] }",
}
