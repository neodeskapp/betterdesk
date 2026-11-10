package access

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

// Request bodies reach this package as decoded JSON (nil, bool, float64,
// json.Number, string, []any, map[string]any). The helpers below read such a
// value the way the console always has: what counts as empty, how a value
// reads as text and how a number is cut out of text. The policy and scope
// normalisers depend on these rules for odd input.

// jsTruthy reports whether a decoded JSON value counts as set: nil, false,
// 0, NaN and "" do not.
func jsTruthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	}
	if f, ok := jsNumber(v); ok {
		return f != 0 && !math.IsNaN(f)
	}
	return true
}

// jsNumber returns the value as a float when it is a number.
func jsNumber(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int32:
		return float64(x), true
	case int64:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		if err != nil {
			return 0, false
		}
		return f, true
	}
	return 0, false
}

// jsString turns a decoded JSON value into text: numbers in their shortest
// form, lists as their items joined by commas (nil items empty), objects as
// "[object Object]".
func jsString(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case []any:
		parts := make([]string, len(x))
		for i, item := range x {
			if item != nil {
				parts[i] = jsString(item)
			}
		}
		return strings.Join(parts, ",")
	case []string:
		return strings.Join(x, ",")
	case map[string]any:
		return "[object Object]"
	}
	if f, ok := jsNumber(v); ok {
		return jsNumberString(f)
	}
	return "[object Object]"
}

// jsStringOrEmpty is jsString for values that count as set, and "" for the
// rest.
func jsStringOrEmpty(v any) string {
	if !jsTruthy(v) {
		return ""
	}
	return jsString(v)
}

// jsNumberString writes a number in its shortest form: plain digits up to
// 21 integer digits and down to six leading zeros, an exponent beyond.
func jsNumberString(f float64) string {
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
	// "d.ddde±xx": the shortest digits that read back as f.
	mantissa, exponent, _ := strings.Cut(strconv.FormatFloat(f, 'e', -1, 64), "e")
	digits := strings.Replace(mantissa, ".", "", 1)
	exp, _ := strconv.Atoi(exponent)
	k, n := len(digits), exp+1
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

// jsSpace reports whether r is trimmed from the ends of text.
func jsSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return r >= 0x2000 && r <= 0x200A
}

// jsTrim removes white space and line ends from both ends of s.
func jsTrim(s string) string {
	return strings.TrimFunc(s, jsSpace)
}

// jsParseInt reads a whole decimal number from the start of the value's
// text: optional white space, an optional sign, then digits; the rest is
// ignored. ok is false when there are no digits.
func jsParseInt(v any) (float64, bool) {
	s := strings.TrimLeftFunc(jsString(v), jsSpace)
	negative := false
	if s != "" && (s[0] == '+' || s[0] == '-') {
		negative = s[0] == '-'
		s = s[1:]
	}
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0, false
	}
	f, err := strconv.ParseFloat(s[:end], 64)
	if err != nil && math.IsInf(f, 0) {
		return 0, false
	}
	if negative {
		f = -f
	}
	return f, true
}

// uniqueTrimmed trims each item, drops empty ones and repeats, and keeps at
// most limit items in their first order.
func uniqueTrimmed(items []string, limit int) []string {
	out := make([]string, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		item = jsTrim(item)
		if item == "" {
			continue
		}
		if _, dup := seen[item]; dup {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// listOrSplit returns the items of a list value as text, or the value's
// text split at commas when it is not a list.
func listOrSplit(v any) []string {
	switch x := v.(type) {
	case []string:
		return x
	case []any:
		out := make([]string, len(x))
		for i, item := range x {
			out[i] = jsStringOrEmpty(item)
		}
		return out
	}
	return strings.Split(jsStringOrEmpty(v), ",")
}
