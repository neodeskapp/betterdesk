package serverinfo

import (
	"encoding/json"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"

	"github.com/unitronix/betterdesk-server/console/goapi"
)

// Some functions take a field of a request body as it was parsed from
// JSON: nil, bool, float64, string, []any, map[string]any or *goapi.Object.
// They treat it by JavaScript's rules for turning a value into text, a
// number or a truth value, as the console's API always has, so that
// "3000", 3000 and [3000] are the same number and a missing field is not
// the same as null.

type undefinedValue struct{}

// Undefined is the value of a field the request body does not have.
var Undefined any = undefinedValue{}

// Field returns the value of key in a parsed JSON object, or Undefined when
// the object has no such key or body is not an object.
func Field(body any, key string) any {
	switch t := body.(type) {
	case *goapi.Object:
		if t.Has(key) {
			return t.Get(key)
		}
	case map[string]any:
		if v, ok := t[key]; ok {
			return v
		}
	}
	return Undefined
}

func isUndefined(v any) bool {
	_, ok := v.(undefinedValue)
	return ok
}

// truthy is JavaScript's Boolean(v).
func truthy(v any) bool {
	if isUndefined(v) {
		return false
	}
	return goapi.Truthy(v)
}

// isFalse is v === false, isTrue is v === true.
func isFalse(v any) bool {
	b, ok := v.(bool)
	return ok && !b
}

func isTrue(v any) bool {
	b, ok := v.(bool)
	return ok && b
}

// jsString is JavaScript's String(v).
func jsString(v any) string {
	switch t := v.(type) {
	case undefinedValue:
		return "undefined"
	case nil:
		return "null"
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return numberString(t)
	case Number:
		return numberString(float64(t))
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case []any:
		parts := make([]string, len(t))
		for i, el := range t {
			if el != nil && !isUndefined(el) {
				parts[i] = jsString(el)
			}
		}
		return strings.Join(parts, ",")
	}
	return "[object Object]"
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
	// encoding/json writes a float the way JavaScript does.
	b, err := json.Marshal(f)
	if err != nil {
		return strconv.FormatFloat(f, 'g', -1, 64)
	}
	return string(b)
}

var (
	decimalLiteral = regexp.MustCompile(`^[+-]?(?:[0-9]+\.?[0-9]*|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$`)
	radixLiteral   = regexp.MustCompile(`^0([xXoObB])([0-9a-fA-F]+)$`)
)

// jsNumber is JavaScript's Number(v).
func jsNumber(v any) float64 {
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
	case Number:
		return float64(t)
	case int:
		return float64(t)
	case int64:
		return float64(t)
	case string:
		return stringToNumber(t)
	case []any:
		return stringToNumber(jsString(t))
	}
	return math.NaN()
}

func stringToNumber(text string) float64 {
	s := jsTrim(text)
	switch s {
	case "":
		return 0
	case "Infinity", "+Infinity":
		return math.Inf(1)
	case "-Infinity":
		return math.Inf(-1)
	}
	if m := radixLiteral.FindStringSubmatch(s); m != nil {
		base := map[byte]int{'x': 16, 'o': 8, 'b': 2}[m[1][0]|0x20]
		n, ok := new(big.Int).SetString(m[2], base)
		if !ok {
			return math.NaN()
		}
		f, _ := new(big.Float).SetInt(n).Float64()
		return f
	}
	if !decimalLiteral.MatchString(s) {
		return math.NaN()
	}
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

// jsParseInt is JavaScript's parseInt(text, 10): white space, a sign and
// the digits that follow. Without a digit the result is NaN.
func jsParseInt(text string) float64 {
	s := strings.TrimLeftFunc(text, isJSSpace)
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
		return math.NaN()
	}
	f, _ := strconv.ParseFloat(s[:end], 64)
	if negative {
		f = -f
	}
	return f
}

// Number is a JavaScript number in a JSON answer. It is written as
// JSON.stringify writes it: whole numbers without a fraction, and null for
// NaN and the infinities.
type Number float64

// MarshalJSON implements json.Marshaler.
func (n Number) MarshalJSON() ([]byte, error) {
	f := float64(n)
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return []byte("null"), nil
	}
	return []byte(numberString(f)), nil
}

// IsFinite reports whether n is neither NaN nor an infinity.
func (n Number) IsFinite() bool {
	f := float64(n)
	return !math.IsNaN(f) && !math.IsInf(f, 0)
}
