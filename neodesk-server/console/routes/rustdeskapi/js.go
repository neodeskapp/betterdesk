package rustdeskapi

import (
	"errors"
	"math"
	"regexp"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/middleware"
)

// The route file read request values the way JavaScript does. Values are
// held as package goapi holds JSON: nil, bool, float64, string, []any and
// *goapi.Object; undefined is the type undefined below.

// undefined is a value JavaScript did not have: a missing member.
type undefined struct{}

// errNotPrimitive is the TypeError String() throws for an object that has
// a member named toString, which no JSON value can make a function of.
var errNotPrimitive = errors.New("Cannot convert object to primitive value")

// valueOf is a body member as JavaScript held it after JSON.parse;
// undefined for a member that is missing.
func valueOf(v middleware.Value) any {
	if !v.Exists() {
		return undefined{}
	}
	parsed, err := goapi.Parse(v.Raw())
	if err != nil {
		return undefined{}
	}
	return parsed
}

// member is body[name] as a JavaScript value. JSON.parse makes a member
// named __proto__ an own member like any other, so nothing is inherited.
func member(body middleware.JSONObject, name string) any {
	return valueOf(body.Get(name))
}

// truthy is !!v.
func truthy(v any) bool {
	if _, ok := v.(undefined); ok {
		return false
	}
	return goapi.Truthy(v)
}

// or is a || b.
func or(a, b any) any {
	if truthy(a) {
		return a
	}
	return b
}

// isUndefined is v === undefined.
func isUndefined(v any) bool {
	_, ok := v.(undefined)
	return ok
}

// str is String(v). It fails where JavaScript throws: an object, also
// inside a list, that has a member named toString.
func str(v any) (string, error) {
	switch x := v.(type) {
	case undefined:
		return "undefined", nil
	case nil:
		return "null", nil
	case bool:
		if x {
			return "true", nil
		}
		return "false", nil
	case float64:
		return jsbind.NumberString(x), nil
	case string:
		return x, nil
	case []any:
		parts := make([]string, len(x))
		for i, item := range x {
			if item == nil || isUndefined(item) {
				continue
			}
			text, err := str(item)
			if err != nil {
				return "", err
			}
			parts[i] = text
		}
		return strings.Join(parts, ","), nil
	case *goapi.Object:
		if x.Has("toString") {
			return "", errNotPrimitive
		}
		return "[object Object]", nil
	}
	return "", errNotPrimitive
}

// strOr is String(v) where a throw cannot happen or does not matter: the
// text "[object Object]" stands in.
func strOr(v any) string {
	text, err := str(v)
	if err != nil {
		return "[object Object]"
	}
	return text
}

// controlChars are the characters sanitizeStr strips: the control
// characters but tab, line feed and carriage return.
var controlChars = regexp.MustCompile(`[\x00-\x08\x0B\x0C\x0E-\x1F]`)

// sanitize is sanitizeStr(val, maxLen): "" for anything that is not text,
// else the text without control characters (tab, line feed and carriage
// return stay), cut to maxLen UTF-16 units.
func sanitize(v any, maxLen int) string {
	text, ok := v.(string)
	if !ok {
		return ""
	}
	return cutUTF16(controlChars.ReplaceAllString(text, ""), maxLen)
}

// cutUTF16 is s.substring(0, n). A character cut in half (the first half
// of a surrogate pair) is stored as U+FFFD, which is what the database
// drivers wrote for it.
func cutUTF16(s string, n int) string {
	units := 0
	for i, r := range s {
		width := 1
		if r > 0xFFFF {
			width = 2
		}
		if units+width > n {
			if units < n {
				return s[:i] + string(utf8.RuneError)
			}
			return s[:i]
		}
		units += width
	}
	return s
}

// utf16Len is the length of a text in UTF-16 units.
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n += len(utf16.Encode([]rune{r}))
	}
	return n
}

// deviceIDPattern is the form of a device id the client API accepts.
var deviceIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// isValidDeviceID is isValidDeviceId: text of 1 to 32 characters, letters,
// digits, _ and -.
func isValidDeviceID(id string) bool {
	return id != "" && utf16Len(id) <= maxIDLen && deviceIDPattern.MatchString(id)
}

// jsTrim is String.prototype.trim.
func jsTrim(s string) string { return jsbind.Trim(s) }

// parseInt10 is Number.parseInt(String(v), 10): the digits at the start of
// the trimmed text, with a sign; NaN without any.
func parseInt10(v any) float64 {
	text, err := str(v)
	if err != nil {
		return math.NaN()
	}
	return parseIntText(text)
}

func parseIntText(text string) float64 {
	text = jsTrim(text)
	sign := 1.0
	if strings.HasPrefix(text, "-") {
		sign, text = -1, text[1:]
	} else if strings.HasPrefix(text, "+") {
		text = text[1:]
	}
	end := 0
	for end < len(text) && text[end] >= '0' && text[end] <= '9' {
		end++
	}
	if end == 0 {
		return math.NaN()
	}
	n := 0.0
	for _, c := range text[:end] {
		n = n*10 + float64(c-'0')
	}
	return sign * n
}

// isFinite is Number.isFinite for a number.
func isFinite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }

// number is the value as a number when it is one (typeof v === 'number').
func number(v any) (float64, bool) {
	f, ok := v.(float64)
	return f, ok
}

// stringify is JSON.stringify(v) of a value: undefined gives "" (Node
// stored the text "undefined" nowhere, the callers check first), NaN and
// the infinities are null, numbers have JavaScript's shortest form, and
// strings escape only what JSON needs.
func stringify(v any) string {
	var b strings.Builder
	writeJSON(&b, v)
	return b.String()
}

func writeJSON(b *strings.Builder, v any) {
	switch x := v.(type) {
	case undefined, nil:
		b.WriteString("null")
	case bool:
		if x {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case float64:
		if !isFinite(x) {
			b.WriteString("null")
			return
		}
		b.WriteString(jsbind.NumberString(x))
	case string:
		writeJSONString(b, x)
	case []any:
		b.WriteByte('[')
		for i, item := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			writeJSON(b, item)
		}
		b.WriteByte(']')
	case []string:
		b.WriteByte('[')
		for i, item := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			writeJSONString(b, item)
		}
		b.WriteByte(']')
	case *goapi.Object:
		if x == nil {
			b.WriteString("null")
			return
		}
		b.WriteByte('{')
		for i, key := range x.Keys() {
			if i > 0 {
				b.WriteByte(',')
			}
			writeJSONString(b, key)
			b.WriteByte(':')
			writeJSON(b, x.Get(key))
		}
		b.WriteByte('}')
	default:
		b.WriteString("null")
	}
}

// writeJSONString writes a JSON string the way JSON.stringify does: ", \
// and the control characters escaped, everything else as it is.
func writeJSONString(b *strings.Builder, s string) {
	const hexDigits = "0123456789abcdef"
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				b.WriteString(`\u00`)
				b.WriteByte(hexDigits[r>>4])
				b.WriteByte(hexDigits[r&0xF])
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
}

// clamp is Math.min(hi, Math.max(lo, f)).
func clamp(f, lo, hi float64) float64 {
	return math.Min(hi, math.Max(lo, f))
}
