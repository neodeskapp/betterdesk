package chat

import (
	"strings"
	"unicode/utf16"
)

// extname is the extension of a file name as the old console cut it:
// from the last dot to the end, and nothing for a name that starts with
// its only dot or has none. A slash starts a new name.
func extname(name string) string {
	startDot, startPart, end := -1, 0, -1
	matchedSlash := true
	// preDot is 0 before a character other than a dot was seen in front
	// of the last dot, 1 after a second dot, -1 after any other character
	preDot := 0
	for i := len(name) - 1; i >= 0; i-- {
		c := name[i]
		if c == '/' {
			if !matchedSlash {
				startPart = i + 1
				break
			}
			continue
		}
		if end == -1 {
			matchedSlash = false
			end = i + 1
		}
		if c == '.' {
			if startDot == -1 {
				startDot = i
			} else if preDot != 1 {
				preDot = 1
			}
		} else if startDot != -1 {
			preDot = -1
		}
	}
	if startDot == -1 || end == -1 || preDot == 0 || (preDot == 1 && startDot == end-1 && startDot == startPart+1) {
		return ""
	}
	return name[startDot:end]
}

// storedExtension is the extension a file is stored with: the first ten
// characters of the uploaded name's extension, counted the way JavaScript
// counts them (a character outside the basic plane is two).
func storedExtension(original string) string {
	units := utf16.Encode([]rune(extname(original)))
	if len(units) > 10 {
		units = units[:10]
	}
	return string(utf16.Decode(units))
}

// contentDisposition is the Content-Disposition header for a download
// under name: attachment; filename="..." for a name of printable Latin-1
// characters, with a filename* parameter added (or in its place) for other
// names and for names with a %XX in them. The header's bytes are Latin-1.
func contentDisposition(name string) string {
	units := utf16.Encode([]rune(name))
	quotable := len(units) > 0
	fallback := make([]byte, len(units))
	hasFallback := false
	for i, u := range units {
		if !(u >= 0x20 && u <= 0x7e || u >= 0x80 && u <= 0xff) {
			quotable = false
		}
		if u >= 0x20 && u <= 0x7e || u >= 0xa0 && u <= 0xff {
			fallback[i] = byte(u)
		} else {
			fallback[i], hasFallback = '?', true
		}
	}

	var b strings.Builder
	b.WriteString("attachment")
	if quotable || hasFallback {
		b.WriteString(`; filename="`)
		for _, c := range fallback {
			if c == '\\' || c == '"' {
				b.WriteByte('\\')
			}
			b.WriteByte(c)
		}
		b.WriteByte('"')
	}
	if hasFallback || !quotable || hasHexEscape(name) {
		b.WriteString("; filename*=UTF-8''")
		const upperhex = "0123456789ABCDEF"
		for i := 0; i < len(name); i++ {
			c := name[i]
			if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("-_.!~", c) >= 0 {
				b.WriteByte(c)
				continue
			}
			b.WriteByte('%')
			b.WriteByte(upperhex[c>>4])
			b.WriteByte(upperhex[c&15])
		}
	}
	return b.String()
}

// hasHexEscape reports whether s has a % followed by two hex digits.
func hasHexEscape(s string) bool {
	for i := 0; i+2 < len(s); i++ {
		if s[i] == '%' && isHex(s[i+1]) && isHex(s[i+2]) {
			return true
		}
	}
	return false
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}
