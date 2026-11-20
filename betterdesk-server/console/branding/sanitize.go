package branding

import (
	"regexp"
	"strings"
)

// The CSS cleaning rules are regular expressions written for JavaScript.
// Two things differ in Go and are spelled out in the patterns below:
//
//   - \s is JavaScript's white space (jsSpaceClass), not only ASCII.
//   - "Ignore case" covers ASCII letters only. Go's (?i) would also let
//     ſ stand for s and the Kelvin sign for k, so every letter is written
//     as a class: [sS].
//
// SVG markup is cleaned with a parser instead (svg.go).

const (
	sp    = `[` + jsSpaceClass + `]`
	notSp = `[^` + jsSpaceClass + `>]`
)

// ci spells a word so that each ASCII letter matches in both cases.
func ci(word string) string {
	var b strings.Builder
	for i := 0; i < len(word); i++ {
		c := word[i]
		switch {
		case c >= 'a' && c <= 'z':
			b.WriteByte('[')
			b.WriteByte(c)
			b.WriteByte(c - 32)
			b.WriteByte(']')
		case strings.IndexByte(`.+*?()[]{}|^$\`, c) >= 0:
			b.WriteByte('\\')
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func ciAny(words ...string) string {
	parts := make([]string, len(words))
	for i, w := range words {
		parts[i] = ci(w)
	}
	return `(?:` + strings.Join(parts, `|`) + `)`
}

var (
	urlSchemes = ciAny("javascript", "data", "vbscript", "file")

	// An event handler attribute with its value
	reEventAttr = regexp.MustCompile(sp + `+` + ci("on") + `[a-zA-Z]+` + sp + `*=` + sp + `*(?:"[^"]*"|'[^']*'|` + notSp + `*)`)

	reCSSStyleTag   = regexp.MustCompile(`<` + sp + `*/?` + sp + `*` + ci("style") + `[^>]*>`)
	reCSSAnyTag     = regexp.MustCompile(`<[^>]*>`)
	reCSSImport     = regexp.MustCompile(ci("@import") + `\b[^;]*;?`)
	reCSSExpression = regexp.MustCompile(ci("expression") + sp + `*\(`)
	reCSSBehavior   = regexp.MustCompile(`(?:-\w+-)?` + ci("behavior") + sp + `*:`)
	reCSSMozBinding = regexp.MustCompile(ci("-moz-binding") + sp + `*:`)
	reCSSScriptURL  = regexp.MustCompile(ci("url") + sp + `*\(` + sp + `*["']?` + sp + `*` + urlSchemes + `:[^)]*\)`)
	reCSSURL        = regexp.MustCompile(ci("url") + sp + `*\(`)

	scriptTag = newTagStripper("script")
	styleTag  = newTagStripper("style")
)

// tagStripper removes the opening, closing and self-closing tags of one
// element name, in any letter case.
type tagStripper struct{ open, close, selfClose *regexp.Regexp }

func newTagStripper(name string) tagStripper {
	n := ci(name)
	return tagStripper{
		open:      regexp.MustCompile(`<` + sp + `*` + n + `\b[^>]*>`),
		close:     regexp.MustCompile(`<` + sp + `*/` + sp + `*` + n + `\b[^>]*>`),
		selfClose: regexp.MustCompile(`<` + sp + `*` + n + `\b[^>]*/` + sp + `*>`),
	}
}

func (t tagStripper) strip(s string) string {
	for {
		prev := s
		s = t.open.ReplaceAllLiteralString(s, "")
		s = t.close.ReplaceAllLiteralString(s, "")
		s = t.selfClose.ReplaceAllLiteralString(s, "")
		if s == prev {
			return s
		}
	}
}

// stripUntilStable runs the replacers in order, again and again, until a
// full pass changes nothing.
func stripUntilStable(s string, replacers ...func(string) string) string {
	for {
		prev := s
		for _, replace := range replacers {
			s = replace(s)
		}
		if s == prev {
			return s
		}
	}
}

func remover(re *regexp.Regexp, with string) func(string) string {
	return func(s string) string { return re.ReplaceAllLiteralString(s, with) }
}

// SanitizeCSSColorValue cleans a colour or gradient that goes into the
// generated stylesheet. Only letters, digits, white space and # % . , ( )
// / - stay, "expression(" and "url(" are taken out until none is left (also
// one wrapped around itself), and the result is cut at 400 characters.
func SanitizeCSSColorValue(value any) (out string, err error) {
	defer catch(&err)
	return sanitizeCSSColorValue(value), nil
}

func sanitizeCSSColorValue(value any) string {
	if value == nil || value == Undefined {
		return ""
	}
	v := trim(toString(value))
	if v == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(v))
	for _, r := range v {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '#', r == '%', r == '.', r == ',', r == '(', r == ')', r == '/', r == '-':
		case isJSSpace(r):
		default:
			continue
		}
		b.WriteRune(r)
	}
	v = stripUntilStable(b.String(), remover(reCSSExpression, ""), remover(reCSSURL, ""))
	return substring(v, 400)
}

// SanitizeCustomCSS cleans the free CSS an administrator can add: no tags,
// no @import, expression(), behavior or -moz-binding, no script or data
// URLs, at most 20,000 characters. The rules run until none finds
// anything, so a word wrapped around itself is gone too.
func SanitizeCustomCSS(value any) (out string, err error) {
	defer catch(&err)
	return sanitizeCustomCSS(value), nil
}

func sanitizeCustomCSS(value any) string {
	if value == nil || value == Undefined {
		return ""
	}
	css := stripUntilStable(toString(value),
		remover(reCSSStyleTag, ""),
		remover(reCSSAnyTag, ""),
		scriptTag.strip,
		styleTag.strip,
		remover(reEventAttr, " "),
		remover(reCSSImport, ""),
		remover(reCSSExpression, ""),
		remover(reCSSBehavior, ""),
		remover(reCSSMozBinding, ""),
		remover(reCSSScriptURL, "none"),
	)
	return substring(css, 20000)
}

// NormalizeBackgroundSize returns the size when it is one of contain,
// repeat, center, auto and cover, and "cover" otherwise.
func NormalizeBackgroundSize(size any) (out string, err error) {
	defer catch(&err)
	return normalizeBackgroundSize(size), nil
}

func normalizeBackgroundSize(size any) string {
	switch trim(toString(or(size, ""))) {
	case "contain", "repeat", "center", "auto", "cover":
		return trim(toString(size))
	}
	return "cover"
}

// BuildBackgroundValue returns the CSS background for a background
// setting: the cleaned colour or gradient, url("...") for an image whose
// address passes ValidateBrandingURL, and "" for anything else.
func BuildBackgroundValue(kind, color, gradient, imageURL any) (out string, err error) {
	defer catch(&err)
	return buildBackgroundValue(kind, color, gradient, imageURL), nil
}

func buildBackgroundValue(kind, color, gradient, imageURL any) string {
	switch kind {
	case "color":
		return sanitizeCSSColorValue(color)
	case "gradient":
		return sanitizeCSSColorValue(gradient)
	case "image":
		u, ok := validateBrandingURL(imageURL)
		if !ok || u == "" {
			return ""
		}
		var b strings.Builder
		b.WriteString(`url("`)
		for i := 0; i < len(u); i++ {
			switch u[i] {
			case '"':
				b.WriteString("%22")
			case '\\':
				b.WriteString("%5C")
			default:
				// ( and ) are named in the rule but stay: escaping
				// them does not change them
				b.WriteByte(u[i])
			}
		}
		b.WriteString(`")`)
		return b.String()
	}
	return ""
}
