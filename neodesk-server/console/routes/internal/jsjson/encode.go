package jsjson

import (
	"math"

	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
)

// MarshalJSON is JSON.stringify(v): no white space, numbers as
// JavaScript prints them (null for NaN and the infinities), an object's
// keys in JavaScript's order. Undefined has no JSON; it is written as
// null, which is what an array holds in its place.
func (v Value) MarshalJSON() ([]byte, error) {
	return v.appendJSON(nil), nil
}

func (v Value) appendJSON(buf []byte) []byte {
	switch v.kind {
	case Bool:
		if v.flag {
			return append(buf, "true"...)
		}
		return append(buf, "false"...)
	case Number:
		if math.IsNaN(v.num) || math.IsInf(v.num, 0) {
			return append(buf, "null"...)
		}
		return append(buf, jsbind.NumberString(v.num)...)
	case String:
		return AppendQuoted(buf, v.text)
	case Array:
		buf = append(buf, '[')
		for i, item := range v.items {
			if i > 0 {
				buf = append(buf, ',')
			}
			buf = item.appendJSON(buf)
		}
		return append(buf, ']')
	case Object:
		buf = append(buf, '{')
		for i, key := range v.keys {
			if i > 0 {
				buf = append(buf, ',')
			}
			buf = AppendQuoted(buf, key)
			buf = append(buf, ':')
			buf = v.members[key].appendJSON(buf)
		}
		return append(buf, '}')
	}
	return append(buf, "null"...)
}

// AppendQuoted appends s as JSON.stringify writes a string: the quote,
// the backslash and control characters escaped, half of a surrogate pair
// as its \u escape, everything else as it is.
func AppendQuoted(buf []byte, s string) []byte {
	const hex = "0123456789abcdef"
	buf = append(buf, '"')
	for i := 0; i < len(s); {
		if half, ok := halfPair(s[i:]); ok {
			buf = append(buf, '\\', 'u', hex[half>>12&0xF], hex[half>>8&0xF], hex[half>>4&0xF], hex[half&0xF])
			i += 3
			continue
		}
		r, size, _ := decode(s[i:])
		switch c := s[i]; {
		case size > 1:
			buf = append(buf, s[i:i+size]...)
		case r == 0xFFFD && c >= 0x80:
			buf = append(buf, 0xEF, 0xBF, 0xBD)
		case c == '"' || c == '\\':
			buf = append(buf, '\\', c)
		case c == '\b':
			buf = append(buf, '\\', 'b')
		case c == '\f':
			buf = append(buf, '\\', 'f')
		case c == '\n':
			buf = append(buf, '\\', 'n')
		case c == '\r':
			buf = append(buf, '\\', 'r')
		case c == '\t':
			buf = append(buf, '\\', 't')
		case c < 0x20:
			buf = append(buf, '\\', 'u', '0', '0', hex[c>>4], hex[c&0xF])
		default:
			buf = append(buf, c)
		}
		i += size
	}
	return append(buf, '"')
}

// Quoted is a text that goes into JSON as JSON.stringify writes it, for
// texts that may end in half of a surrogate pair (see Slice).
type Quoted string

// MarshalJSON implements json.Marshaler.
func (q Quoted) MarshalJSON() ([]byte, error) { return AppendQuoted(nil, string(q)), nil }
