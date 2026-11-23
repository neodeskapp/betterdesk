package jsreq

import (
	"math"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
)

// Stringify is JSON.stringify(v) for a value from Body or goapi.Parse: no
// white space, the keys of an object in JavaScript's order, numbers in
// their shortest form, and only the characters escaped that JSON requires.
// A number that is not finite is written as null.
func Stringify(v any) string {
	var b strings.Builder
	stringify(&b, v)
	return b.String()
}

func stringify(b *strings.Builder, v any) {
	switch t := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		if t {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) {
			b.WriteString("null")
			return
		}
		b.WriteString(numberString(t))
	case string:
		quote(b, t)
	case []any:
		b.WriteByte('[')
		for i, el := range t {
			if i > 0 {
				b.WriteByte(',')
			}
			stringify(b, el)
		}
		b.WriteByte(']')
	case *goapi.Object:
		if t == nil {
			b.WriteString("null")
			return
		}
		b.WriteByte('{')
		for i, key := range t.Keys() {
			if i > 0 {
				b.WriteByte(',')
			}
			quote(b, key)
			b.WriteByte(':')
			stringify(b, t.Get(key))
		}
		b.WriteByte('}')
	default:
		b.WriteString("null")
	}
}

// quote writes s as a JSON string the way JavaScript does: quotes,
// backslashes and control characters are escaped, nothing else.
func quote(b *strings.Builder, s string) {
	const hex = "0123456789abcdef"
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			b.WriteString(`\"`)
		case c == '\\':
			b.WriteString(`\\`)
		case c == '\b':
			b.WriteString(`\b`)
		case c == '\f':
			b.WriteString(`\f`)
		case c == '\n':
			b.WriteString(`\n`)
		case c == '\r':
			b.WriteString(`\r`)
		case c == '\t':
			b.WriteString(`\t`)
		case c < 0x20:
			b.WriteString(`\u00`)
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&15])
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
}

// Length is s.length in JavaScript: the number of UTF-16 code units.
func Length(s string) int {
	n := 0
	for _, r := range s {
		if r == utf8.RuneError {
			n++
			continue
		}
		n += utf16.RuneLen(r)
	}
	return n
}
