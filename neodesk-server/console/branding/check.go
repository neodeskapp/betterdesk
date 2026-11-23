package branding

import (
	"regexp"
	"strconv"
	"strings"
)

// A save of the appearance cleans what it stores (SaveBranding): colours
// and gradients lose the characters that do not belong, a refused picture
// address is skipped, a slide with one is dropped, custom CSS and SVG
// lose what is not allowed, values are cut to their length. CheckSettings
// finds those values first, so the settings route can refuse them with
// the reason instead of storing something else than what was typed
// (BUGS L366, L367, L368).

// Problem is a value a save refuses: the setting ("bgColor",
// "colors.accentBlue", "loginSlides.1.imageUrl") and the reason, the key
// of its text in the language files,
// with the parameters of that text: settings.appearance.refused_<reason>.
type Problem struct {
	Field  string
	Reason string
	Params map[string]string
}

// The reasons a value is refused.
const (
	RefusedColor          = "color"
	RefusedGradient       = "gradient"
	RefusedAddress        = "address"
	RefusedOutsideAddress = "outside_address"
	RefusedSVG            = "svg"
	RefusedSVGParts       = "svg_parts"
	RefusedCustomCSS      = "custom_css"
	RefusedCustomCSSLong  = "custom_css_long"
	RefusedChoice         = "choice"
	RefusedSlides         = "slides"
	RefusedTooLong        = "too_long"
)

var (
	reHexColor  = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{4}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$`)
	reFuncColor = regexp.MustCompile(`^(?:rgba?|hsla?)\(\s*-?[0-9.]+%?(?:\s*[,/ ]\s*-?[0-9.]+%?){2,3}\s*\)$`)
	reGradient  = regexp.MustCompile(`^(?:repeating-)?(?:linear|radial|conic)-gradient\(.+\)$`)
	reWord      = regexp.MustCompile(`^[a-zA-Z]+$`)
)

// isCSSColor reports a colour the stylesheet can use as it is: #rgb,
// #rgba, #rrggbb, #rrggbbaa, rgb(), rgba(), hsl(), hsla() with numbers,
// or a colour name.
func isCSSColor(v string) bool {
	switch {
	case reHexColor.MatchString(v), reFuncColor.MatchString(strings.ToLower(v)):
		return true
	case reWord.MatchString(v):
		return cssColorNames[strings.ToLower(v)]
	}
	return false
}

// checkText is the value of a setting as text, and false for a value that
// a save stores as something else than its text (an object, a list).
func checkText(value any) (string, bool) {
	switch v := value.(type) {
	case nil:
		return "", true
	case string:
		return v, true
	case float64, bool:
		return toString(v), true
	}
	return "", value == Undefined
}

// CheckSettings returns the values of updates (flat or nested, as
// SaveBranding takes them) that a save would not store as given. A value
// that is the same as in current, the stored settings, is not refused:
// the form sends every setting back, also one it hides, and what was
// stored before (by an import, a saved appearance or an older version)
// stays as it is. It changes nothing.
func CheckSettings(updates, current any) (problems []Problem) {
	refuse := func(field, reason string, params ...string) {
		p := Problem{Field: field, Reason: reason}
		if len(params) > 0 {
			p.Params = map[string]string{}
			for i := 0; i+1 < len(params); i += 2 {
				p.Params[params[i]] = params[i+1]
			}
		}
		problems = append(problems, p)
	}
	checkAddress := func(field string, value any) {
		text, ok := checkText(value)
		if !ok {
			refuse(field, RefusedAddress)
			return
		}
		text = trim(text)
		if text == "" {
			return
		}
		if _, valid := validateBrandingURL(text); !valid {
			refuse(field, RefusedAddress)
			return
		}
		// the console's pages load pictures only from this server
		// (img-src 'self'), so an address elsewhere never shows
		if !strings.HasPrefix(text, "/") {
			refuse(field, RefusedOutsideAddress)
		}
	}
	checkColor := func(field string, value any) {
		text, ok := checkText(value)
		if text = trim(text); ok && (text == "" || isCSSColor(text)) {
			return
		}
		refuse(field, RefusedColor)
	}

	flat := flattenAppearanceInput(updates)
	defer func() { problems = withoutStored(problems, updates, flat, current) }()
	for _, key := range flat.Keys() {
		value, _ := flat.Get(key)
		switch {
		case key == "colors":
			if !Truthy(value) {
				continue
			}
			if !isObjectLike(value) {
				refuse("colors", RefusedColor)
				continue
			}
			for _, name := range colorKeys {
				if hasOwn(value, name) {
					checkColor("colors."+name, property(value, name))
				}
			}
		case !isSetting[key]:
		case urlSettings[key]:
			checkAddress(key, value)
		case key == "bgColor" || key == "loginBgColor" || key == "agentBgColor" || key == "rdclientBgColor" || key == "glassColor":
			checkColor(key, value)
		case colorSettings[key]:
			// the gradients
			text, ok := checkText(value)
			text = trim(text)
			if !ok || text != "" && (sanitizeCSSColorValue(text) != text || !reGradient.MatchString(strings.ToLower(text))) {
				refuse(key, RefusedGradient)
			}
		case key == "logoSvg" || key == "faviconSvg":
			text, ok := checkText(value)
			if !ok {
				refuse(key, RefusedSVG)
				continue
			}
			if trim(text) == "" {
				continue
			}
			removed, valid := SVGRemovals(text)
			switch {
			case !valid:
				refuse(key, RefusedSVG)
			case len(removed) > 0:
				refuse(key, RefusedSVGParts, "parts", strings.Join(removed, ", "))
			}
		case key == "customCss":
			text, ok := checkText(value)
			switch {
			case !ok:
				refuse(key, RefusedCustomCSS)
			case len(units(text)) > 20000:
				refuse(key, RefusedCustomCSSLong, "max", "20000")
			case sanitizeCustomCSS(text) != text:
				refuse(key, RefusedCustomCSS)
			}
		case key == "themeMode":
			if text, _ := checkText(value); normalizeThemeMode(text) != text {
				refuse(key, RefusedChoice)
			}
		case key == "loginPromoMode":
			if text, _ := checkText(value); normalizeLoginPromoMode(text) != text {
				refuse(key, RefusedChoice)
			}
		case key == "bgSize" || key == "rdclientBgSize":
			// flattenAppearanceInput already made the size one of the
			// choices: look at what was sent
			section := "console"
			if key == "rdclientBgSize" {
				section = "rdclient"
			}
			if text, _ := checkText(sentSetting(updates, section, key)); text != "" && normalizeBackgroundSize(text) != text {
				refuse(key, RefusedChoice)
			}
		case key == "loginSlides":
			checkSlides(value, refuse, checkAddress)
		}
	}
	return problems
}

// withoutStored drops the problems of values that are the same as the
// stored ones: a top-level setting or a colour of the palette.
func withoutStored(problems []Problem, updates any, flat *Object, current any) []Problem {
	if !Truthy(current) || len(problems) == 0 {
		return problems
	}
	same := func(sent, stored any) bool {
		a, okA := checkText(sent)
		b, okB := checkText(stored)
		return okA && okB && trim(a) == trim(b)
	}
	kept := problems[:0]
	for _, p := range problems {
		var sent, stored any = Undefined, Undefined
		switch color, isColor := strings.CutPrefix(p.Field, "colors."); {
		case p.Field == "bgSize":
			sent, stored = sentSetting(updates, "console", p.Field), property(current, p.Field)
		case p.Field == "rdclientBgSize":
			sent, stored = sentSetting(updates, "rdclient", p.Field), property(current, p.Field)
		case isColor:
			sent, stored = property(property(flat, "colors"), color), property(property(current, "colors"), color)
		case !strings.Contains(p.Field, ".") && p.Field != "colors" && p.Field != "loginSlides":
			sent, stored = property(flat, p.Field), property(current, p.Field)
		}
		if sent != Undefined && stored != Undefined && same(sent, stored) {
			continue
		}
		kept = append(kept, p)
	}
	return kept
}

// sentSetting is a setting as the body of a save gave it, flat or in its
// section of the nested shape; the flat one counts, as in
// flattenAppearanceInput.
func sentSetting(updates any, section, key string) any {
	if !Truthy(updates) || !isObjectLike(updates) {
		return Undefined
	}
	if hasOwn(updates, key) {
		return property(updates, key)
	}
	source := updates
	if nested := property(updates, "appearance"); Truthy(nested) && isObjectLike(nested) {
		source = nested
	}
	if data := property(source, section); Truthy(data) && isObjectLike(data) && hasOwn(data, key) {
		return property(data, key)
	}
	return Undefined
}

// checkSlides finds what sanitizeLoginSlides would drop or cut: a value
// that is no list, more than MaxLoginSlides slides, a slide that is no
// object, a refused picture address (the whole slide went), a title over
// 120 or a text over 400 characters.
func checkSlides(value any, refuse func(field, reason string, params ...string), checkAddress func(string, any)) {
	if text, ok := value.(string); ok {
		parsed, err := parseJSON(text)
		if err != nil {
			refuse("loginSlides", RefusedSlides)
			return
		}
		value = parsed
	}
	items, ok := value.([]any)
	if !ok {
		refuse("loginSlides", RefusedSlides)
		return
	}
	if len(items) > MaxLoginSlides {
		refuse("loginSlides", RefusedSlides)
	}
	for i, item := range items {
		field := "loginSlides." + strconv.Itoa(i)
		if !Truthy(item) || !isObjectLike(item) {
			refuse(field, RefusedSlides)
			continue
		}
		checkAddress(field+".imageUrl", property(item, "imageUrl"))
		for _, part := range []struct {
			key string
			max int
		}{{"title", 120}, {"body", 400}} {
			if text, _ := checkText(property(item, part.key)); len(units(text)) > part.max {
				refuse(field+"."+part.key, RefusedTooLong, "max", strconv.Itoa(part.max))
			}
		}
	}
}

// cssColorNames are the colour keywords of CSS.
var cssColorNames = setOf(
	"transparent", "currentcolor",
	"aliceblue", "antiquewhite", "aqua", "aquamarine", "azure", "beige", "bisque", "black", "blanchedalmond",
	"blue", "blueviolet", "brown", "burlywood", "cadetblue", "chartreuse", "chocolate", "coral", "cornflowerblue",
	"cornsilk", "crimson", "cyan", "darkblue", "darkcyan", "darkgoldenrod", "darkgray", "darkgreen", "darkgrey",
	"darkkhaki", "darkmagenta", "darkolivegreen", "darkorange", "darkorchid", "darkred", "darksalmon",
	"darkseagreen", "darkslateblue", "darkslategray", "darkslategrey", "darkturquoise", "darkviolet", "deeppink",
	"deepskyblue", "dimgray", "dimgrey", "dodgerblue", "firebrick", "floralwhite", "forestgreen", "fuchsia",
	"gainsboro", "ghostwhite", "gold", "goldenrod", "gray", "green", "greenyellow", "grey", "honeydew", "hotpink",
	"indianred", "indigo", "ivory", "khaki", "lavender", "lavenderblush", "lawngreen", "lemonchiffon", "lightblue",
	"lightcoral", "lightcyan", "lightgoldenrodyellow", "lightgray", "lightgreen", "lightgrey", "lightpink",
	"lightsalmon", "lightseagreen", "lightskyblue", "lightslategray", "lightslategrey", "lightsteelblue",
	"lightyellow", "lime", "limegreen", "linen", "magenta", "maroon", "mediumaquamarine", "mediumblue",
	"mediumorchid", "mediumpurple", "mediumseagreen", "mediumslateblue", "mediumspringgreen", "mediumturquoise",
	"mediumvioletred", "midnightblue", "mintcream", "mistyrose", "moccasin", "navajowhite", "navy", "oldlace",
	"olive", "olivedrab", "orange", "orangered", "orchid", "palegoldenrod", "palegreen", "paleturquoise",
	"palevioletred", "papayawhip", "peachpuff", "peru", "pink", "plum", "powderblue", "purple", "rebeccapurple",
	"red", "rosybrown", "royalblue", "saddlebrown", "salmon", "sandybrown", "seagreen", "seashell", "sienna",
	"silver", "skyblue", "slateblue", "slategray", "slategrey", "snow", "springgreen", "steelblue", "tan", "teal",
	"thistle", "tomato", "turquoise", "violet", "wheat", "white", "whitesmoke", "yellow", "yellowgreen",
)
