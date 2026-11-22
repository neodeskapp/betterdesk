package smtpwire

import (
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The texts and decisions of a session depend on how JavaScript treats
// strings: what counts as white space, how a string is quoted, how a number
// is printed. The helpers here do the same in Go.

// isJSSpace reports whether r is white space for JavaScript's \s and trim().
func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return r >= 0x2000 && r <= 0x200A
}

// isJSLineEnd reports whether r ends a line for JavaScript's "." in a
// regular expression.
func isJSLineEnd(r rune) bool {
	return r == '\n' || r == '\r' || r == 0x2028 || r == 0x2029
}

func jsTrim(s string) string { return strings.TrimFunc(s, isJSSpace) }

// trimReplyBytes trims a server reply that is still raw bytes. JavaScript
// sees every byte as one character there, so the no-break space 0xA0 counts
// as white space next to the ASCII ones.
func trimReplyBytes(s string) string {
	isSpace := func(b byte) bool { return (b >= '\t' && b <= '\r') || b == ' ' || b == 0xA0 }
	for len(s) > 0 && isSpace(s[0]) {
		s = s[1:]
	}
	for len(s) > 0 && isSpace(s[len(s)-1]) {
		s = s[:len(s)-1]
	}
	return s
}

// decodeReply turns the bytes of a reply into text: UTF-8 when the bytes are
// valid UTF-8 without the replacement character, else one character per byte
// (Latin-1), so that an 8-bit reply of an old server stays readable.
func decodeReply(raw string) string {
	if utf8.ValidString(raw) && !strings.ContainsRune(raw, utf8.RuneError) {
		return raw
	}
	var b strings.Builder
	b.Grow(len(raw) + len(raw)/2)
	for i := 0; i < len(raw); i++ {
		b.WriteRune(rune(raw[i]))
	}
	return b.String()
}

// jsonQuote quotes s as JSON.stringify does.
func jsonQuote(s string) string {
	const hex = "0123456789abcdef"
	var b strings.Builder
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
				b.WriteByte(hex[r&0xF])
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// jsNumber prints a non-negative number as JavaScript does.
func jsNumber(f float64) string {
	if math.IsInf(f, 1) {
		return "Infinity"
	}
	if f < 1e21 {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return strconv.FormatFloat(f, 'e', -1, 64)
}

func hasNonASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return true
		}
	}
	return false
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

func isWordByte(b byte) bool {
	return isDigit(b) || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || b == '_'
}

// hasPrefixFold reports whether s starts with the upper-case ASCII word,
// in any case. Only ASCII letters match, as in a JavaScript regular
// expression with the i flag.
func hasPrefixFold(s, word string) bool {
	if len(s) < len(word) {
		return false
	}
	for i := 0; i < len(word); i++ {
		b := s[i]
		if b >= 'a' && b <= 'z' {
			b -= 'a' - 'A'
		}
		if b != word[i] {
			return false
		}
	}
	return true
}
