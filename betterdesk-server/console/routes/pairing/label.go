package pairing

import (
	"math"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/unitronix/betterdesk-server/console/middleware"
)

// maxLabel is the length of a label in UTF-16 units, which is how the
// console has always counted characters.
const maxLabel = 64

// label is the label of a code in UTF-16 units. A label cut at 64 units
// can end with half of a character outside the basic plane.
type label []uint16

// labelOf reads the label from the request body: whatever was sent, as
// the text JavaScript's String() makes of it, without the white space
// around it, cut to 64 units. Nothing, null, false, 0 and "" are no
// label.
func labelOf(v middleware.Value) label {
	if !v.Truthy() {
		return nil
	}
	units := utf16.Encode([]rune(trim(text(v))))
	if len(units) > maxLabel {
		units = units[:maxLabel]
	}
	return units
}

// String returns the label as text. Half a character at the end becomes
// U+FFFD, as it does when the label goes to the API server and into the
// audit log.
func (l label) String() string {
	return string(utf16.Decode(l))
}

// MarshalJSON writes the label as JSON.stringify writes a string: half a
// character as a \u escape, quotes, backslashes and control characters
// escaped, everything else as it is.
func (l label) MarshalJSON() ([]byte, error) {
	const hex = "0123456789abcdef"
	out := make([]byte, 0, len(l)+2)
	out = append(out, '"')
	for i := 0; i < len(l); i++ {
		unit := rune(l[i])
		if utf16.IsSurrogate(unit) {
			if i+1 < len(l) {
				if r := utf16.DecodeRune(unit, rune(l[i+1])); r != utf8.RuneError {
					out = utf8.AppendRune(out, r)
					i++
					continue
				}
			}
			out = append(out, '\\', 'u', hex[unit>>12], hex[unit>>8&0xF], hex[unit>>4&0xF], hex[unit&0xF])
			continue
		}
		switch unit {
		case '"':
			out = append(out, '\\', '"')
		case '\\':
			out = append(out, '\\', '\\')
		case '\b':
			out = append(out, '\\', 'b')
		case '\f':
			out = append(out, '\\', 'f')
		case '\n':
			out = append(out, '\\', 'n')
		case '\r':
			out = append(out, '\\', 'r')
		case '\t':
			out = append(out, '\\', 't')
		default:
			if unit < 0x20 {
				out = append(out, '\\', 'u', '0', '0', hex[unit>>4], hex[unit&0xF])
				continue
			}
			out = utf8.AppendRune(out, unit)
		}
	}
	return append(out, '"'), nil
}

// text is String(v) for a value of a JSON body: a string as it is, a
// number and a boolean as JavaScript writes them, null as "null", a list
// as its elements joined with commas (null as nothing), an object as
// "[object Object]".
func text(v middleware.Value) string {
	switch v.Kind() {
	case middleware.KindString:
		s, _ := v.Str()
		return s
	case middleware.KindNumber:
		f, _ := v.Float()
		switch {
		case math.IsInf(f, 1):
			return "Infinity"
		case math.IsInf(f, -1):
			return "-Infinity"
		}
		return v.Text("")
	case middleware.KindBool:
		return v.Text("")
	case middleware.KindArray:
		items, _ := v.Array()
		parts := make([]string, len(items))
		for i, item := range items {
			if item.Kind() != middleware.KindNull {
				parts[i] = text(item)
			}
		}
		return strings.Join(parts, ",")
	case middleware.KindObject:
		return "[object Object]"
	}
	return "null"
}

// trim is String.prototype.trim: it removes the white space and line ends
// JavaScript knows from both ends.
func trim(s string) string {
	return strings.TrimFunc(s, isSpace)
}

func isSpace(r rune) bool {
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
