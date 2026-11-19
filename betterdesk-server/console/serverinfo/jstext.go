package serverinfo

import (
	"strings"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// The formats of this package are defined by JavaScript code. The helpers
// here give the results JavaScript gives for trimming, for lengths counted in UTF-16 code
// units, for white space in patterns and for text read from a file.

// isJSSpace reports whether r is white space or a line terminator in
// JavaScript: what String.prototype.trim removes and what \s matches.
func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return r >= 0x2000 && r <= 0x200A
}

// jsSpaceClass is \s of a JavaScript pattern, written for the regexp
// package, whose own \s knows ASCII only.
const jsSpaceClass = `[\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]`

// jsTrim is String.prototype.trim.
func jsTrim(s string) string {
	return strings.TrimFunc(s, isJSSpace)
}

// hasJSSpace reports whether /\s/ matches s.
func hasJSSpace(s string) bool {
	return strings.IndexFunc(s, isJSSpace) >= 0
}

// leadingJSSpace returns the number of white space characters s starts
// with.
func leadingJSSpace(s string) int {
	count := 0
	for _, r := range s {
		if !isJSSpace(r) {
			break
		}
		count++
	}
	return count
}

// utf16Len is the length of s as JavaScript counts it.
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// sliceUnits is s.slice(from, to) for 0 <= from <= to, counted in UTF-16
// code units. Half of a character outside the Basic Multilingual Plane
// cannot stand in a Go string: where a cut falls inside one, that half
// becomes U+FFFD.
func sliceUnits(s string, from, to int) string {
	if from == 0 && len(s) <= to {
		return s
	}
	var b strings.Builder
	pos := 0
	for _, r := range s {
		if pos >= to {
			break
		}
		if r < 0x10000 {
			if pos >= from {
				b.WriteRune(r)
			}
			pos++
			continue
		}
		highIn := pos >= from
		lowIn := pos+1 >= from && pos+1 < to
		switch {
		case highIn && lowIn:
			b.WriteRune(r)
		case highIn || lowIn:
			b.WriteRune(utf8.RuneError)
		}
		pos += 2
	}
	return b.String()
}

// DecodeUTF8 turns the bytes of a file into text the way Node does for
// fs.readFileSync(path, 'utf8'): each broken sequence becomes one U+FFFD,
// and a byte order mark stays in the text.
func DecodeUTF8(b []byte) string { return decodeUTF8(b) }

func decodeUTF8(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	var out strings.Builder
	out.Grow(len(b))
	needed, seen := 0, 0
	var cp rune
	lower, upper := byte(0x80), byte(0xBF)
	for i := 0; i < len(b); i++ {
		c := b[i]
		if needed == 0 {
			switch {
			case c < 0x80:
				out.WriteByte(c)
			case c >= 0xC2 && c <= 0xDF:
				needed, cp = 1, rune(c&0x1F)
			case c >= 0xE0 && c <= 0xEF:
				if c == 0xE0 {
					lower = 0xA0
				}
				if c == 0xED {
					upper = 0x9F
				}
				needed, cp = 2, rune(c&0x0F)
			case c >= 0xF0 && c <= 0xF4:
				if c == 0xF0 {
					lower = 0x90
				}
				if c == 0xF4 {
					upper = 0x8F
				}
				needed, cp = 3, rune(c&0x07)
			default:
				out.WriteRune(utf8.RuneError)
			}
			continue
		}
		if c < lower || c > upper {
			needed, seen, cp = 0, 0, 0
			lower, upper = 0x80, 0xBF
			out.WriteRune(utf8.RuneError)
			i--
			continue
		}
		lower, upper = 0x80, 0xBF
		cp = cp<<6 | rune(c&0x3F)
		seen++
		if seen == needed {
			out.WriteRune(cp)
			needed, seen, cp = 0, 0, 0
		}
	}
	if needed != 0 {
		out.WriteRune(utf8.RuneError)
	}
	return out.String()
}

// validText returns s with every broken UTF-8 sequence replaced by U+FFFD,
// which is what a JavaScript string holds after such bytes were decoded.
func validText(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	return decodeUTF8([]byte(s))
}

// jsUpper is String.prototype.toUpperCase: the full Unicode mapping, in
// which one letter can become several ("ß" is "SS").
func jsUpper(s string) string {
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			ascii = false
			break
		}
	}
	if ascii {
		return strings.ToUpper(s)
	}
	return cases.Upper(language.Und).String(s)
}

// lowerASCII lowers the letters A to Z and leaves everything else.
func lowerASCII(s string) string {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c >= 'A' && c <= 'Z' {
			b := []byte(s)
			for j := i; j < len(b); j++ {
				if b[j] >= 'A' && b[j] <= 'Z' {
					b[j] += 'a' - 'A'
				}
			}
			return string(b)
		}
	}
	return s
}

// jsonString writes s as JSON.stringify writes a string: quotes, the
// backslash and control characters escaped, everything else as it is.
func jsonString(b *strings.Builder, s string) {
	const hex = "0123456789abcdef"
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				b.WriteString(`\u00`)
				b.WriteByte(hex[r>>4])
				b.WriteByte(hex[r&0x0F])
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
}
