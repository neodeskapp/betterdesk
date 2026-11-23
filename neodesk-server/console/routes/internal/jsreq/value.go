// Package jsreq reads a request the way the handlers of the Node console
// read it, for route groups whose answer depends on what JavaScript made of
// an odd value: a query parameter sent twice, a number where text was
// meant, a list where one value was meant.
//
// Values are the ones package goapi uses for JSON: nil, bool, float64,
// string, []any and *goapi.Object. nil stands for both null and undefined;
// where a handler told them apart, its port has to look at the body
// itself.
package jsreq

import (
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/middleware"
)

// Body is req.body as JSON.parse left it: an object or a list for a JSON
// body, an object of strings for a form, an empty object without a body.
func Body(r *http.Request) any {
	v, err := goapi.Parse(middleware.BodyValue(r).Raw())
	if err != nil {
		return goapi.NewObject()
	}
	return v
}

// Prop is body[name] for a value from Body: the member of an object, nil
// for everything else.
func Prop(v any, name string) any {
	o, _ := v.(*goapi.Object)
	return o.Get(name)
}

// String is JavaScript's String(v): text as it is, a number in its
// shortest form, a list as its elements joined by commas (null elements
// empty), an object as "[object Object]", nil as "null".
func String(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return numberString(t)
	case []any:
		parts := make([]string, len(t))
		for i, el := range t {
			if el != nil {
				parts[i] = String(el)
			}
		}
		return strings.Join(parts, ",")
	}
	return "[object Object]"
}

// StringOr is String(v || fallback) for a text fallback: the fallback
// for null, false, 0, NaN and "".
func StringOr(v any, fallback string) string {
	if !goapi.Truthy(v) {
		return fallback
	}
	return String(v)
}

// ParseInt is parseInt(v, radix) for radix 10, and parseInt(v) for radix
// 0, which also reads text that starts with 0x as hexadecimal. ok is false
// where JavaScript gives NaN. A missing value (nil) is NaN: neither
// "undefined" nor "null" starts with a digit.
func ParseInt(v any, radix int) (value float64, ok bool) {
	if v == nil {
		return 0, false
	}
	s := strings.TrimLeftFunc(String(v), isJSSpace)
	negative := false
	if s != "" && (s[0] == '+' || s[0] == '-') {
		negative = s[0] == '-'
		s = s[1:]
	}
	base := 10
	if radix == 0 && len(s) >= 2 && s[0] == '0' && (s[1] == 'x' || s[1] == 'X') {
		base, s = 16, s[2:]
	}
	seen := false
	for i := 0; i < len(s); i++ {
		digit, isDigit := hexDigit(s[i])
		if !isDigit || int(digit) >= base {
			break
		}
		value = value*float64(base) + float64(digit)
		seen = true
	}
	if !seen {
		return 0, false
	}
	if base == 10 {
		// The digits as one decimal number, so that long runs round the
		// way JavaScript rounds them
		end := 0
		for end < len(s) && s[end] >= '0' && s[end] <= '9' {
			end++
		}
		value, _ = strconv.ParseFloat(s[:end], 64)
	}
	if negative {
		value = -value
	}
	return value, true
}

// ClampedInt is Math.min(Math.max(parseInt(v, 10) || fallback, low), high):
// the whole number v starts with, the fallback for NaN and 0, held between
// low and high.
func ClampedInt(v any, fallback, low, high float64) float64 {
	n, ok := ParseInt(v, 10)
	if !ok || n == 0 {
		n = fallback
	}
	return math.Min(math.Max(n, low), high)
}

// NumberString is String(n) of JavaScript.
func NumberString(n float64) string { return numberString(n) }

// isJSSpace reports the white space JavaScript skips in front of a number.
func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return r >= 0x2000 && r <= 0x200A
}

// numberString is JavaScript's String(f) for a number.
func numberString(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	case f == 0:
		return "0"
	}
	sign := ""
	if f < 0 {
		sign = "-"
		f = -f
	}
	// d.ddddde±xx with the shortest digits that read back as f
	e := strconv.FormatFloat(f, 'e', -1, 64)
	mant, expText, _ := strings.Cut(e, "e")
	exp, _ := strconv.Atoi(expText)
	digits := strings.Replace(mant, ".", "", 1)
	k := len(digits)
	n := exp + 1
	switch {
	case k <= n && n <= 21:
		return sign + digits + strings.Repeat("0", n-k)
	case 0 < n && n <= 21:
		return sign + digits[:n] + "." + digits[n:]
	case -6 < n && n <= 0:
		return sign + "0." + strings.Repeat("0", -n) + digits
	}
	out := sign + digits[:1]
	if k > 1 {
		out += "." + digits[1:]
	}
	if n-1 < 0 {
		return out + "e-" + strconv.Itoa(1-n)
	}
	return out + "e+" + strconv.Itoa(n-1)
}

// Number is JavaScript's Number(v) for a JSON value: a number as it is,
// true as 1, false and null as 0, text by its number ("" and blank text
// are 0, "0x10" is 16), a list by its text, and NaN for everything else.
func Number(v any) float64 {
	switch t := v.(type) {
	case nil:
		return 0
	case bool:
		if t {
			return 1
		}
		return 0
	case float64:
		return t
	case string:
		return numberOfText(t)
	case []any:
		return numberOfText(String(t))
	}
	return math.NaN()
}

func numberOfText(text string) float64 {
	s := strings.TrimFunc(text, isJSSpace)
	switch s {
	case "":
		return 0
	case "Infinity", "+Infinity":
		return math.Inf(1)
	case "-Infinity":
		return math.Inf(-1)
	}
	if len(s) > 2 && s[0] == '0' {
		base := 0
		switch s[1] {
		case 'x', 'X':
			base = 16
		case 'o', 'O':
			base = 8
		case 'b', 'B':
			base = 2
		}
		if base != 0 {
			value := 0.0
			for i := 2; i < len(s); i++ {
				digit, ok := hexDigit(s[i])
				if !ok || int(digit) >= base {
					return math.NaN()
				}
				value = value*float64(base) + float64(digit)
			}
			return value
		}
	}
	if !decimalNumber(s) {
		return math.NaN()
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil && !math.IsInf(f, 0) {
		return math.NaN()
	}
	return f
}

// decimalNumber reports whether s is a decimal literal of JavaScript:
// an optional sign, digits with an optional point, an optional exponent.
func decimalNumber(s string) bool {
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	digits := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
		digits++
	}
	if i < len(s) && s[i] == '.' {
		i++
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
			digits++
		}
	}
	if digits == 0 {
		return false
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		exponent := 0
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
			exponent++
		}
		if exponent == 0 {
			return false
		}
	}
	return i == len(s)
}
