// Package jsbind carries values through a route the way the console's
// JavaScript did: a body member of any JSON type, or a number made from a
// path parameter, becomes the statement parameter better-sqlite3 or
// node-postgres made of it, with the same failures.
//
// The console's routes handed request values to their queries mostly
// unchecked, and the two drivers treat a value of the wrong type
// differently:
//
//	value        better-sqlite3                    node-postgres
//	"text"       text                              text
//	5, 1.5       the double 5.0, 1.5               "5", "1.5"
//	NaN          NULL                              "NaN"
//	true         TypeError                         "true"
//	null         NULL                              NULL
//	undefined    NULL                              NULL
//	["a", 2]     two parameters in a row           the array text {"a","2"}
//	{"a": 1}     no parameter (named parameters)   the JSON text {"a":1}
//
// SQLite then stores a double in a TEXT column as "5.0" and in an INTEGER
// column as 5, and PostgreSQL parses the text by the column's type, which
// fails for "1.5" in an integer column. Args gives a statement what each
// driver gave it, so the database decides as it did.
package jsbind

import (
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/unitronix/betterdesk-server/console/middleware"
	"github.com/unitronix/betterdesk-server/console/store"
)

type kind uint8

const (
	kindUndefined kind = iota
	kindNull
	kindBool
	kindNumber
	kindString
	kindArray
	kindObject
)

// Value is a JavaScript value. The zero Value is undefined.
type Value struct {
	kind  kind
	num   float64
	str   string
	flag  bool
	items []Value
	// object is the body value of an object
	object middleware.Value
	// complex marks a query parameter Query did not take apart
	complex bool
}

// Undefined and Null are the two empty values.
var (
	Undefined = Value{}
	Null      = Value{kind: kindNull}
)

// Number is the number f; NaN and the infinities are numbers too.
func Number(f float64) Value { return Value{kind: kindNumber, num: f} }

// String is the text s.
func String(s string) Value { return Value{kind: kindString, str: s} }

// Bool is true or false.
func Bool(b bool) Value { return Value{kind: kindBool, flag: b} }

// Of is a member of a request body as JavaScript had it after JSON.parse.
func Of(v middleware.Value) Value {
	switch v.Kind() {
	case middleware.KindNull:
		return Null
	case middleware.KindBool:
		b, _ := v.Bool()
		return Bool(b)
	case middleware.KindNumber:
		f, _ := v.Float()
		return Number(f)
	case middleware.KindString:
		s, _ := v.Str()
		return String(s)
	case middleware.KindArray:
		elements, _ := v.Array()
		items := make([]Value, len(elements))
		for i, element := range elements {
			items[i] = Of(element)
		}
		return Value{kind: kindArray, items: items}
	case middleware.KindObject:
		return Value{kind: kindObject, object: v}
	}
	return Undefined
}

// IsUndefined is v === undefined.
func (v Value) IsUndefined() bool { return v.kind == kindUndefined }

// IsNull is v === null.
func (v Value) IsNull() bool { return v.kind == kindNull }

// IsString is typeof v === 'string'.
func (v Value) IsString() bool { return v.kind == kindString }

// IsArray is Array.isArray(v).
func (v Value) IsArray() bool { return v.kind == kindArray }

// IsObject is true for an object that is not an array and not null.
func (v Value) IsObject() bool { return v.kind == kindObject }

// Str is the text of a string value, "" for every other type.
func (v Value) Str() string { return v.str }

// Items is the elements of an array, nil for every other type.
func (v Value) Items() []Value { return v.items }

// Member is v[name] for an object, undefined for every other type and
// for a member that is missing.
func (v Value) Member(name string) Value {
	if v.kind != kindObject {
		return Undefined
	}
	obj, _ := v.object.Object()
	return Of(obj.Get(name))
}

// Truthy is !!v.
func (v Value) Truthy() bool {
	switch v.kind {
	case kindUndefined, kindNull:
		return false
	case kindBool:
		return v.flag
	case kindNumber:
		return v.num != 0 && !math.IsNaN(v.num)
	case kindString:
		return v.str != ""
	}
	return true
}

// Or is v || fallback.
func (v Value) Or(fallback Value) Value {
	if v.Truthy() {
		return v
	}
	return fallback
}

// ToString is String(v).
func (v Value) ToString() string {
	switch v.kind {
	case kindUndefined:
		return "undefined"
	case kindNull:
		return "null"
	case kindBool:
		if v.flag {
			return "true"
		}
		return "false"
	case kindNumber:
		return NumberString(v.num)
	case kindString:
		return v.str
	case kindArray:
		parts := make([]string, len(v.items))
		for i, item := range v.items {
			if item.kind != kindUndefined && item.kind != kindNull {
				parts[i] = item.ToString()
			}
		}
		return strings.Join(parts, ",")
	}
	return "[object Object]"
}

// ToNumber is Number(v).
func (v Value) ToNumber() float64 {
	switch v.kind {
	case kindUndefined:
		return math.NaN()
	case kindNull:
		return 0
	case kindBool:
		if v.flag {
			return 1
		}
		return 0
	case kindNumber:
		return v.num
	}
	return ParseNumber(v.ToString())
}

var decimalLiteral = regexp.MustCompile(`^[+-]?(?:[0-9]+\.?[0-9]*(?:[eE][+-]?[0-9]+)?|\.[0-9]+(?:[eE][+-]?[0-9]+)?)$`)

// ParseNumber is Number(s) for a string: white space around the number
// does not count, the empty string is 0, and text that is not one number
// (decimal, Infinity, or 0x, 0o, 0b with digits) is NaN.
func ParseNumber(s string) float64 {
	s = Trim(s)
	switch s {
	case "":
		return 0
	case "Infinity", "+Infinity":
		return math.Inf(1)
	case "-Infinity":
		return math.Inf(-1)
	}
	if decimalLiteral.MatchString(s) {
		// Out of range is an infinity or zero, with the sign, as in
		// JavaScript.
		f, _ := strconv.ParseFloat(s, 64)
		return f
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
			if n, ok := new(big.Int).SetString(s[2:], base); ok && n.Sign() >= 0 && !strings.ContainsAny(s[2:], "+-_") {
				f, _ := new(big.Float).SetInt(n).Float64()
				return f
			}
		}
	}
	return math.NaN()
}

// NumberString is String(f): the shortest digits that read back as f,
// plain up to 21 digits and down to 1e-7, with an exponent beyond.
func NumberString(f float64) string {
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
		sign, f = "-", -f
	}
	// digits d1...dk and the exponent n with f = 0.d1...dk x 10^n
	mantissa, expText, _ := strings.Cut(strconv.FormatFloat(f, 'e', -1, 64), "e")
	exp, _ := strconv.Atoi(expText)
	digits := strings.Replace(mantissa, ".", "", 1)
	k, n := len(digits), exp+1
	switch {
	case k <= n && n <= 21:
		return sign + digits + strings.Repeat("0", n-k)
	case 0 < n && n <= 21:
		return sign + digits[:n] + "." + digits[n:]
	case -6 < n && n <= 0:
		return sign + "0." + strings.Repeat("0", -n) + digits
	}
	e := strconv.Itoa(n - 1)
	if n-1 > 0 {
		e = "+" + e
	}
	if k == 1 {
		return sign + digits + "e" + e
	}
	return sign + digits[:1] + "." + digits[1:] + "e" + e
}

// IsSpace reports the characters String.prototype.trim removes and
// Number() skips: JavaScript's white space and line ends.
func IsSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a || unicode.Is(unicode.Zs, r)
}

// Trim is s.trim().
func Trim(s string) string { return strings.TrimFunc(s, IsSpace) }

// ToLower is s.toLowerCase(), with one difference: a capital sigma at the
// end of a word becomes the letter sigma here and the final sigma in
// JavaScript. Callers keep only ASCII letters of the result (slugs), where
// both are the same. Unlike strings.ToLower it gives the dotted capital I
// its two characters, an i and a dot above.
func ToLower(s string) string {
	if !strings.ContainsRune(s, 0x130) {
		return strings.ToLower(s)
	}
	var b strings.Builder
	for _, part := range strings.SplitAfter(s, "\u0130") {
		if rest, found := strings.CutSuffix(part, "\u0130"); found {
			b.WriteString(strings.ToLower(rest))
			b.WriteString("i\u0307")
			continue
		}
		b.WriteString(strings.ToLower(part))
	}
	return b.String()
}

// JSON is JSON.stringify(v): undefined is the empty text.
func (v Value) JSON() string {
	switch v.kind {
	case kindUndefined:
		return ""
	case kindNull:
		return "null"
	case kindBool:
		return v.ToString()
	case kindNumber:
		if math.IsNaN(v.num) || math.IsInf(v.num, 0) {
			return "null"
		}
		return NumberString(v.num)
	case kindString:
		out, _ := store.NullString{String: v.str, Valid: true}.MarshalJSON()
		return string(out)
	case kindArray:
		parts := make([]string, len(v.items))
		for i, item := range v.items {
			if parts[i] = item.JSON(); parts[i] == "" {
				parts[i] = "null"
			}
		}
		return "[" + strings.Join(parts, ",") + "]"
	}
	if v.complex {
		return "{}"
	}
	raw := string(v.object.Raw())
	var parsed store.JSONParsed
	if err := parsed.Scan(raw); err != nil {
		return raw
	}
	out, err := parsed.MarshalJSON()
	if err != nil {
		return raw
	}
	return string(out)
}

// IsNumber is typeof v === 'number'.
func (v Value) IsNumber() bool { return v.kind == kindNumber }
