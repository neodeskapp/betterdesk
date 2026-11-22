package middleware

import (
	"bytes"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

const replacement = "�"

// decodeUTF8 turns body bytes into text. Bytes that are not UTF-8 become
// U+FFFD, one for each ill-formed sequence (the WHATWG decoder's rule, so
// a truncated three-byte letter is one U+FFFD and not two).
func decodeUTF8(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	var out strings.Builder
	out.Grow(len(b))
	var (
		needed, seen int
		codepoint    rune
		lower, upper byte = 0x80, 0xbf
	)
	for i := 0; i < len(b); i++ {
		c := b[i]
		if needed == 0 {
			switch {
			case c <= 0x7f:
				out.WriteByte(c)
			case c >= 0xc2 && c <= 0xdf:
				needed, codepoint = 1, rune(c&0x1f)
			case c >= 0xe0 && c <= 0xef:
				if c == 0xe0 {
					lower = 0xa0
				}
				if c == 0xed {
					upper = 0x9f
				}
				needed, codepoint = 2, rune(c&0x0f)
			case c >= 0xf0 && c <= 0xf4:
				if c == 0xf0 {
					lower = 0x90
				}
				if c == 0xf4 {
					upper = 0x8f
				}
				needed, codepoint = 3, rune(c&0x07)
			default:
				out.WriteString(replacement)
			}
			continue
		}
		if c < lower || c > upper {
			// The sequence ends here; this byte starts over
			needed, seen, codepoint, lower, upper = 0, 0, 0, 0x80, 0xbf
			out.WriteString(replacement)
			i--
			continue
		}
		lower, upper = 0x80, 0xbf
		codepoint = codepoint<<6 | rune(c&0x3f)
		seen++
		if seen == needed {
			out.WriteRune(codepoint)
			needed, seen, codepoint = 0, 0, 0
		}
	}
	if needed != 0 {
		out.WriteString(replacement)
	}
	return out.String()
}

// decodeUTF16 turns UTF-16 bytes into text. A single byte at the end is
// dropped, a surrogate without its partner becomes U+FFFD.
func decodeUTF16(b []byte, bigEndian bool) string {
	units := make([]uint16, len(b)/2)
	for i := range units {
		if bigEndian {
			units[i] = uint16(b[2*i])<<8 | uint16(b[2*i+1])
		} else {
			units[i] = uint16(b[2*i+1])<<8 | uint16(b[2*i])
		}
	}
	return string(utf16.Decode(units))
}

// utf16BigEndian decides the byte order of a body sent as "utf-16": by
// its byte order mark, else by where the zero bytes of ASCII letters are
// in the first 64 bytes, else little endian.
func utf16BigEndian(b []byte) bool {
	if len(b) < 2 {
		return false
	}
	if b[0] == 0xfe && b[1] == 0xff {
		return true
	}
	if b[0] == 0xff && b[1] == 0xfe {
		return false
	}
	n := len(b) - len(b)%2
	if n > 64 {
		n = 64
	}
	le, be := 0, 0
	for i := 0; i < n; i += 2 {
		if b[i] == 0 && b[i+1] != 0 {
			be++
		}
		if b[i] != 0 && b[i+1] == 0 {
			le++
		}
	}
	return be > le
}

type charset int

const (
	charsetUnsupported charset = iota
	charsetUTF8
	charsetUTF16
	charsetUTF16LE
	charsetUTF16BE
)

// lookupCharset finds the decoder for a charset parameter. Names are
// compared without regard to case and punctuation, so utf-8, UTF_8 and
// "utf 8" are one.
func lookupCharset(name string) charset {
	var b strings.Builder
	lowered := strings.ToLower(name)
	// A trailing year such as ":2004" is not part of the name
	if n := len(lowered); n >= 5 && lowered[n-5] == ':' && strings.Trim(lowered[n-4:], "0123456789") == "" {
		lowered = lowered[:n-5]
	}
	for i := 0; i < len(lowered); i++ {
		if c := lowered[i]; (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') {
			b.WriteByte(c)
		}
	}
	switch b.String() {
	case "utf8":
		return charsetUTF8
	case "utf16":
		return charsetUTF16
	case "utf16le":
		return charsetUTF16LE
	case "utf16be":
		return charsetUTF16BE
	}
	return charsetUnsupported
}

// decodeBody turns body bytes into UTF-8 text and drops one byte order
// mark at the start. A body that is UTF-8 already is not copied.
func decodeBody(b []byte, cs charset) []byte {
	switch cs {
	case charsetUTF16:
		b = []byte(decodeUTF16(b, utf16BigEndian(b)))
	case charsetUTF16LE:
		b = []byte(decodeUTF16(b, false))
	case charsetUTF16BE:
		b = []byte(decodeUTF16(b, true))
	default:
		if !utf8.Valid(b) {
			b = []byte(decodeUTF8(b))
		}
	}
	return bytes.TrimPrefix(b, []byte(byteOrderMark))
}

// byteOrderMark is U+FEFF in UTF-8.
const byteOrderMark = "\xEF\xBB\xBF"
