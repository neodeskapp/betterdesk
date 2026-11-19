package main

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

// The seed reads the console's JSON answers the way a script does: a
// missing property is undefined, any value can be tested for truth, ids
// go into addresses as text. These helpers give the answers those rules,
// so the requests and rows that depend on an answer come out the same.

// undefinedValue is what reading a property that is not there gives.
type undefinedValue struct{}

var undefined any = undefinedValue{}

// prop reads a property of a decoded JSON value.
func prop(v any, key string) any {
	if o, ok := v.(map[string]any); ok {
		if x, found := o[key]; found {
			return x
		}
	}
	return undefined
}

// truthy is JavaScript's truth test.
func truthy(v any) bool {
	switch x := v.(type) {
	case nil, undefinedValue:
		return false
	case bool:
		return x
	case float64:
		return x != 0 && !math.IsNaN(x)
	case string:
		return x != ""
	}
	return true
}

// or is a || b.
func or(a, b any) any {
	if truthy(a) {
		return a
	}
	return b
}

// list is the array in v, or nothing when v is not an array.
func list(v any) []any {
	a, _ := v.([]any)
	return a
}

// strictEqual is a === b for the values JSON decodes to. Two objects or
// arrays are never the same value here.
func strictEqual(a, b any) bool {
	switch x := a.(type) {
	case nil:
		return b == nil
	case undefinedValue:
		_, ok := b.(undefinedValue)
		return ok
	case bool:
		y, ok := b.(bool)
		return ok && x == y
	case float64:
		y, ok := b.(float64)
		return ok && x == y
	case string:
		y, ok := b.(string)
		return ok && x == y
	}
	return false
}

// find returns the first element of items for which match is true, or
// undefined.
func find(items []any, match func(any) bool) any {
	for _, it := range items {
		if match(it) {
			return it
		}
	}
	return undefined
}

// some reports whether match is true for an element of items.
func some(items []any, match func(any) bool) bool {
	for _, it := range items {
		if match(it) {
			return true
		}
	}
	return false
}

// toNumber is JavaScript's Number(v).
func toNumber(v any) float64 {
	switch x := v.(type) {
	case nil:
		return 0
	case bool:
		if x {
			return 1
		}
		return 0
	case float64:
		return x
	case string:
		return stringToNumber(x)
	}
	return math.NaN()
}

// stringToNumber reads text as Number(text) does for decimal numbers,
// Infinity and 0x, 0o and 0b integers. Anything else is NaN.
func stringToNumber(s string) float64 {
	s = strings.TrimSpace(s)
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
			n, err := strconv.ParseUint(s[2:], base, 64)
			if err != nil {
				return math.NaN()
			}
			return float64(n)
		}
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789.eE+-", c) {
			return math.NaN()
		}
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return math.NaN()
	}
	return f
}

// toString is String(v), as a template literal puts v into text.
func toString(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case undefinedValue:
		return "undefined"
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return numberString(x)
	case string:
		return x
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			if e != nil && e != undefined {
				parts[i] = toString(e)
			}
		}
		return strings.Join(parts, ",")
	}
	return "[object Object]"
}

// numberString formats a number as JavaScript does: the shortest text that
// reads back as the same number, exponent form below 1e-6 and from 1e21.
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
	abs := math.Abs(f)
	if abs >= 1e-6 && abs < 1e21 {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	s := strconv.FormatFloat(f, 'e', -1, 64)
	mantissa, exp, _ := strings.Cut(s, "e")
	sign := exp[:1]
	exp = strings.TrimLeft(exp[1:], "0")
	return mantissa + "e" + sign + exp
}

// round is Math.round: the nearest integer, halves towards +Infinity.
func round(x float64) float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return x
	}
	r := math.Floor(x)
	if x-r >= 0.5 {
		r++
	}
	return r
}

// jsMin and jsMax are Math.min and Math.max: NaN when any argument is NaN.
func jsMin(a float64, rest ...float64) float64 {
	m := a
	for _, x := range rest {
		if math.IsNaN(x) || math.IsNaN(m) {
			m = math.NaN()
			continue
		}
		m = math.Min(m, x)
	}
	return m
}

func jsMax(a float64, rest ...float64) float64 {
	m := a
	for _, x := range rest {
		if math.IsNaN(x) || math.IsNaN(m) {
			m = math.NaN()
			continue
		}
		m = math.Max(m, x)
	}
	return m
}

// mul is a*b rounded to a float64 on its own, so the compiler never fuses
// it with an addition into one instruction with another rounding.
func mul(a, b float64) float64 { return float64(a * b) }

// field is one property of an object.
type field struct {
	Key   string
	Value any
}

// object is a JSON object that keeps its properties in order, as
// JSON.stringify writes them. A property whose value is undefined is left
// out.
type object []field

// MarshalJSON writes the object as JSON.stringify would.
func (o object) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	first := true
	for _, f := range o {
		if f.Value == undefined {
			continue
		}
		if !first {
			b.WriteByte(',')
		}
		first = false
		b.Write(jsonString(f.Key))
		b.WriteByte(':')
		v, err := stringify(f.Value)
		if err != nil {
			return nil, err
		}
		b.Write(v)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// stringify is JSON.stringify for the values the seed sends and stores:
// no HTML escapes, numbers in JavaScript's form, undefined in an array as
// null.
func stringify(v any) ([]byte, error) {
	switch x := v.(type) {
	case nil, undefinedValue:
		return []byte("null"), nil
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return []byte("null"), nil
		}
		return []byte(numberString(x)), nil
	case string:
		return jsonString(x), nil
	case []any:
		var b bytes.Buffer
		b.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			s, err := stringify(e)
			if err != nil {
				return nil, err
			}
			b.Write(s)
		}
		b.WriteByte(']')
		return b.Bytes(), nil
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

func jsonString(s string) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return bytes.TrimRight(b.Bytes(), "\n")
}

// mustStringify is stringify for values built from the demo data, which
// always encode.
func mustStringify(v any) string {
	b, err := stringify(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}
