// Package jsjson reads JSON the way the old console's JavaScript did and
// does to the values what its routes did: look up a member, test for
// truth, make text or a number of whatever is there, and write the value
// out again.
//
// The routes that use it took documents a device or an older version
// wrote (a device's hardware report, the info of a peer) and worked on
// them without checking a type. So every operation here is defined for
// every JSON type, with JavaScript's result:
//
//	v.Get("cpu").Get("brand")   v?.cpu?.brand
//	v.Truthy()                  !!v
//	v.Text()                    String(v), `${v}`
//	v.Num()                     Number(v), and what - * / make of v
//	v.MarshalJSON()             JSON.stringify(v)
//
// A text is a Go string in UTF-8. Half of a surrogate pair, which a
// JavaScript string can hold and UTF-8 cannot, is kept as the three bytes
// its code point would have, and written back as the escape it came from.
package jsjson

import (
	"errors"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
)

// Kind is the type of a Value.
type Kind uint8

// The types a value can have. The zero Kind is Undefined.
const (
	Undefined Kind = iota
	Null
	Bool
	Number
	String
	Array
	Object
)

// Value is a JavaScript value that JSON can hold, or undefined. The zero
// Value is undefined.
type Value struct {
	kind  Kind
	flag  bool
	num   float64
	text  string
	items []Value
	// keys are an object's keys in the order JavaScript lists them
	keys    []string
	members map[string]Value
}

// NullValue is null.
var NullValue = Value{kind: Null}

// OfText is the string s.
func OfText(s string) Value { return Value{kind: String, text: s} }

// OfNumber is the number f.
func OfNumber(f float64) Value { return Value{kind: Number, num: f} }

// Kind returns the type of v.
func (v Value) Kind() Kind { return v.kind }

// IsNullish is v == null: null or undefined, the two values ?. and ??
// stop at.
func (v Value) IsNullish() bool { return v.kind == Undefined || v.kind == Null }

// IsObjectLike is typeof v === 'object' && v !== null: an object or an
// array.
func (v Value) IsObjectLike() bool { return v.kind == Object || v.kind == Array }

// Str is the text of a string, "" for every other type.
func (v Value) Str() string { return v.text }

// Float is the number of a number, 0 for every other type.
func (v Value) Float() float64 { return v.num }

// Items is the elements of an array, nil for every other type.
func (v Value) Items() []Value { return v.items }

// Keys is Object.keys(v) for an object: its keys in the order JavaScript
// lists them, array indexes first. It is nil for every other type.
func (v Value) Keys() []string {
	if v.kind != Object {
		return nil
	}
	return append([]string(nil), v.keys...)
}

// Truthy is !!v.
func (v Value) Truthy() bool {
	switch v.kind {
	case Undefined, Null:
		return false
	case Bool:
		return v.flag
	case Number:
		return v.num != 0 && !math.IsNaN(v.num)
	case String:
		return v.text != ""
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

// Get is v?.[name] for the names the routes read: a member of an object,
// and the length of an array or a string. Everything else has no members
// and gives undefined, null and undefined included.
//
// Numbers as names are not looked up in arrays and strings, and neither
// is what JavaScript's prototypes add (constructor, toString).
func (v Value) Get(name string) Value {
	switch v.kind {
	case Object:
		return v.members[name]
	case Array:
		if name == "length" {
			return OfNumber(float64(len(v.items)))
		}
	case String:
		if name == "length" {
			return OfNumber(float64(Length(v.text)))
		}
	}
	return Value{}
}

// ErrNotPrimitive is the TypeError JavaScript throws when it has to make
// text or a number of an object that has a member named toString, which
// hides the function every object has by that name.
var ErrNotPrimitive = errors.New("Cannot convert object to primitive value")

// Text is String(v): a text itself, a number as JavaScript prints it,
// the elements of an array joined by commas with nothing for null, and
// "[object Object]" for an object.
func (v Value) Text() (string, error) {
	switch v.kind {
	case Undefined:
		return "undefined", nil
	case Null:
		return "null", nil
	case Bool:
		if v.flag {
			return "true", nil
		}
		return "false", nil
	case Number:
		return jsbind.NumberString(v.num), nil
	case String:
		return v.text, nil
	case Array:
		parts := make([]string, len(v.items))
		for i, item := range v.items {
			if item.IsNullish() {
				continue
			}
			text, err := item.Text()
			if err != nil {
				return "", err
			}
			parts[i] = text
		}
		return strings.Join(parts, ","), nil
	}
	if _, hidden := v.members["toString"]; hidden {
		return "", ErrNotPrimitive
	}
	return "[object Object]", nil
}

// Num is Number(v): null is 0, true is 1, a text is the number it spells
// (the empty text is 0, anything that is no number is NaN), and an array
// or an object is the number its text spells.
func (v Value) Num() (float64, error) {
	switch v.kind {
	case Undefined:
		return math.NaN(), nil
	case Null:
		return 0, nil
	case Bool:
		if v.flag {
			return 1, nil
		}
		return 0, nil
	case Number:
		return v.num, nil
	}
	text, err := v.Text()
	if err != nil {
		return 0, err
	}
	return jsbind.ParseNumber(text), nil
}

// Length is s.length: the number of UTF-16 units of s.
func Length(s string) int {
	n := 0
	for i := 0; i < len(s); {
		_, size, units := decode(s[i:])
		n += units
		i += size
	}
	return n
}

// Slice is s.slice(0, n): the first n UTF-16 units of s. A cut between
// the two halves of a pair leaves the first half.
func Slice(s string, n int) string {
	units := 0
	for i := 0; i < len(s); {
		r, size, width := decode(s[i:])
		if units+width > n {
			if units < n {
				// One unit of room and a pair next: its first half
				high := 0xD800 + (r-0x10000)>>10
				return s[:i] + string([]byte{0xED, 0x80 | byte(high>>6&0x3F), 0x80 | byte(high&0x3F)})
			}
			return s[:i]
		}
		units += width
		i += size
	}
	return s
}

// Units is s split into its UTF-16 units, each a text of its own, the way
// {...s} spreads a text: a character outside the first plane gives its
// two halves, each kept as half of a pair.
func Units(s string) []string {
	var out []string
	for i := 0; i < len(s); {
		r, size, units := decode(s[i:])
		switch {
		case units == 2:
			out = append(out, halfText(0xD800+(r-0x10000)>>10), halfText(0xDC00+(r-0x10000)&0x3FF))
		case r == utf8.RuneError && size == 1:
			out = append(out, string(utf8.RuneError))
		default:
			out = append(out, s[i:i+size])
		}
		i += size
	}
	return out
}

// WellFormed is s with every half of a surrogate pair replaced by U+FFFD:
// the text better-sqlite3 and node-postgres sent to the database for a
// JavaScript string.
func WellFormed(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if _, ok := halfPair(s[i:]); ok {
			b.WriteRune(utf8.RuneError)
			i += 3
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// halfText is half of a surrogate pair in the three bytes this package
// keeps it in.
func halfText(u rune) string {
	return string([]byte{0xED, 0x80 | byte(u>>6&0x3F), 0x80 | byte(u&0x3F)})
}

// decode reads the first character of s: its code point, its bytes and
// its UTF-16 units. Half of a pair (three bytes that UTF-8 does not
// allow) is one unit, and so is a byte that is no UTF-8 at all.
func decode(s string) (r rune, size, units int) {
	if half, ok := halfPair(s); ok {
		return half, 3, 1
	}
	c := s[0]
	switch {
	case c < 0x80:
		return rune(c), 1, 1
	case c >= 0xC2 && c < 0xE0 && len(s) >= 2 && cont(s[1]):
		return rune(c&0x1F)<<6 | rune(s[1]&0x3F), 2, 1
	case c >= 0xE0 && c < 0xF0 && len(s) >= 3 && cont(s[1]) && cont(s[2]):
		if r = rune(c&0x0F)<<12 | rune(s[1]&0x3F)<<6 | rune(s[2]&0x3F); r >= 0x800 {
			return r, 3, 1
		}
	case c >= 0xF0 && c < 0xF5 && len(s) >= 4 && cont(s[1]) && cont(s[2]) && cont(s[3]):
		if r = rune(c&0x07)<<18 | rune(s[1]&0x3F)<<12 | rune(s[2]&0x3F)<<6 | rune(s[3]&0x3F); r >= 0x10000 && r <= 0x10FFFF {
			return r, 4, 2
		}
	}
	return 0xFFFD, 1, 1
}

func cont(b byte) bool { return b&0xC0 == 0x80 }

// halfPair reports whether s starts with half of a surrogate pair in the
// three bytes this package keeps it in, and which half.
func halfPair(s string) (rune, bool) {
	if len(s) >= 3 && s[0] == 0xED && s[1] >= 0xA0 && s[1] <= 0xBF && cont(s[2]) {
		return 0xD000 | rune(s[1]&0x3F)<<6 | rune(s[2]&0x3F), true
	}
	return 0, false
}
