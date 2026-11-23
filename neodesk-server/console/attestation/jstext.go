package attestation

import (
	"strings"
	"unicode/utf8"

	"github.com/neodeskapp/neodesk/neodesk-server/console/branding"
)

// Texts here are JavaScript strings as package branding keeps them: UTF-8,
// with a lone surrogate (half of a character outside the basic plane) kept
// as its three WTF-8 bytes, so it is written back as \udXXX in JSON.

// normalizeBadgeText cleans a badge text as normalizeBadgeText did:
// String(value), null and a missing value as "", without the control
// characters U+0000 to U+001F and U+007F, trimmed, cut to limit UTF-16
// units. It fails where String() threw.
func normalizeBadgeText(value any, limit int) (string, error) {
	text := ""
	if value != nil && value != branding.Undefined {
		s, err := branding.ToString(value)
		if err != nil {
			return "", err
		}
		text = s
	}
	return cutUnits(branding.Trim(dropControls(text)), limit), nil
}

// dropControls removes the bytes 0x00 to 0x1F and 0x7F. They are ASCII,
// so removing them byte by byte leaves every other character whole.
func dropControls(s string) string {
	clean := true
	for i := 0; i < len(s); i++ {
		if s[i] <= 0x1f || s[i] == 0x7f {
			clean = false
			break
		}
	}
	if clean {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if c := s[i]; c > 0x1f && c != 0x7f {
			b.WriteByte(c)
		}
	}
	return b.String()
}

// cutUnits is s.slice(0, n): the first n UTF-16 code units. A cut through
// a character outside the basic plane keeps its first half as a lone
// surrogate.
func cutUnits(s string, n int) string {
	units := 0
	for i := 0; i < len(s); {
		if units == n {
			return s[:i]
		}
		c := s[i]
		switch {
		case c < utf8.RuneSelf:
			i++
			units++
		case isSurrogateAt(s, i):
			i += 3
			units++
		default:
			r, size := utf8.DecodeRuneInString(s[i:])
			if r >= 0x10000 {
				if units+1 == n {
					// the high half of the pair, as WTF-8
					high := 0xD800 + (r-0x10000)>>10
					return s[:i] + string([]byte{0xED, byte(0x80 | (high>>6)&0x3F), byte(0x80 | high&0x3F)})
				}
				units += 2
			} else {
				units++
			}
			i += size
		}
	}
	return s
}

// isSurrogateAt reports whether the three bytes at s[i:] are a lone
// surrogate in WTF-8.
func isSurrogateAt(s string, i int) bool {
	return s[i] == 0xED && i+2 < len(s) && s[i+1] >= 0xA0 && s[i+1] <= 0xBF && s[i+2] >= 0x80 && s[i+2] <= 0xBF
}
