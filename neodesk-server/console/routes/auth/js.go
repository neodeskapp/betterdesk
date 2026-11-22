package auth

import (
	"math"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/unitronix/betterdesk-server/console/middleware"
)

// The old routes took the values of a JSON body as they came, of whatever
// type. These helpers answer what JavaScript answered for the few things
// the routes did with them.

// maxCredentialLength is the longest user name and password a sign-in
// takes, in UTF-16 units.
const maxCredentialLength = 128

// utf16Length is s.length in JavaScript.
func utf16Length(s string) int {
	n := 0
	for _, r := range s {
		n += utf16.RuneLen(r)
	}
	return n
}

// cutUTF16 is s.slice(0, n) in JavaScript.
func cutUTF16(s string, n int) string {
	if len(s) <= n {
		return s
	}
	units := utf16.Encode([]rune(s))
	if len(units) <= n {
		return s
	}
	return string(utf16.Decode(units[:n]))
}

// isJSSpace reports the white space JavaScript's trim removes.
func isJSSpace(r rune) bool {
	switch {
	case r >= '\t' && r <= '\r', r == ' ', r == 0xA0, r == 0x1680:
		return true
	case r >= 0x2000 && r <= 0x200A:
		return true
	case r == 0x2028, r == 0x2029, r == 0x202F, r == 0x205F, r == 0x3000, r == 0xFEFF:
		return true
	}
	return false
}

func jsTrim(s string) string { return strings.TrimFunc(s, isJSSpace) }

// jsString is String(v): what a value looks like inside a text. A list is
// its items joined by commas, an object "[object Object]".
func jsString(v middleware.Value) string {
	switch v.Kind() {
	case middleware.KindUndefined:
		return "undefined"
	case middleware.KindNull:
		return "null"
	case middleware.KindNumber:
		f, _ := v.Float()
		switch {
		case math.IsInf(f, 1):
			return "Infinity"
		case math.IsInf(f, -1):
			return "-Infinity"
		}
		return v.Text("NaN")
	case middleware.KindArray:
		items, _ := v.Array()
		parts := make([]string, len(items))
		for i, item := range items {
			if k := item.Kind(); k != middleware.KindNull && k != middleware.KindUndefined {
				parts[i] = jsString(item)
			}
		}
		return strings.Join(parts, ",")
	case middleware.KindObject:
		return "[object Object]"
	}
	return v.Text("")
}

// jsNumber is Number(v).
func jsNumber(v middleware.Value) float64 {
	switch v.Kind() {
	case middleware.KindNull:
		return 0
	case middleware.KindBool:
		if v.Truthy() {
			return 1
		}
		return 0
	case middleware.KindNumber:
		f, _ := v.Float()
		return f
	case middleware.KindString, middleware.KindArray:
		// A list is turned into text first
		return textNumber(jsString(v))
	}
	return math.NaN()
}

// textNumber is Number(s) for a text: white space around it is dropped,
// an empty text is 0, and the text must be a number as a whole.
func textNumber(s string) float64 {
	s = jsTrim(s)
	if s == "" {
		return 0
	}
	switch s {
	case "Infinity", "+Infinity":
		return math.Inf(1)
	case "-Infinity":
		return math.Inf(-1)
	}
	if len(s) > 2 && s[0] == '0' {
		base := map[byte]int{'x': 16, 'X': 16, 'o': 8, 'O': 8, 'b': 2, 'B': 2}[s[1]]
		if base != 0 {
			n, err := strconv.ParseUint(s[2:], base, 64)
			if err != nil || strings.ContainsAny(s[2:], "_+-") {
				return math.NaN()
			}
			return float64(n)
		}
	}
	if strings.ContainsAny(s, "_xXpPnNiI") {
		return math.NaN()
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil && !math.IsInf(f, 0) {
		return math.NaN()
	}
	return f
}

// jsLength is v.length as a comparison sees it: the length of a text in
// UTF-16 units, the number of items of a list, and for an object its
// member "length" as a number. A number or a boolean has no length, which
// is NaN here: it is neither above nor below any limit.
func jsLength(v middleware.Value) float64 {
	switch v.Kind() {
	case middleware.KindString:
		s, _ := v.Str()
		return float64(utf16Length(s))
	case middleware.KindArray:
		items, _ := v.Array()
		return float64(len(items))
	case middleware.KindObject:
		object, _ := v.Object()
		if length := object.Get("length"); length.Exists() {
			return jsNumber(length)
		}
	}
	return math.NaN()
}

// tooLong is v.length > 128 for a user name or a password.
func tooLong(v middleware.Value) bool {
	return jsLength(v) > maxCredentialLength
}

// hasLength reports v.length === n for the code of the two-factor set-up.
func hasLength(v middleware.Value, n int) bool {
	switch v.Kind() {
	case middleware.KindString:
		s, _ := v.Str()
		return utf16Length(s) == n
	case middleware.KindArray:
		items, _ := v.Array()
		return len(items) == n
	case middleware.KindObject:
		object, _ := v.Object()
		length := object.Get("length")
		f, isNumber := length.Float()
		return isNumber && f == float64(n)
	}
	return false
}
