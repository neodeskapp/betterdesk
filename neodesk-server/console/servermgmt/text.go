package servermgmt

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

// decodeUTF8 is Buffer.toString('utf8'): bytes that are no UTF-8 become
// U+FFFD, one for each broken sequence as far as it went (the WHATWG
// decoder), not one per byte.
func decodeUTF8(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	var out strings.Builder
	out.Grow(len(b) + 8)
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

// collator orders names as JavaScript's a.localeCompare(b) without a
// locale did, in an English locale: letters first by their base, then
// accents, then case with lower case first; punctuation before digits,
// digits before letters, digits one by one. A collator is for one
// goroutine.
type collator struct {
	c *collate.Collator
}

func newCollator() *collator { return &collator{c: collate.New(language.English)} }

func (c *collator) compare(a, b string) int {
	return c.c.CompareString(asciiDigits(a), asciiDigits(b))
}

// asciiDigits replaces the decimal digits of other scripts by 0 to 9:
// JavaScript's order does not tell them apart, the collation tables do.
// Fullwidth and mathematical digits stay, both orders put them after the
// ASCII digit.
func asciiDigits(s string) string {
	if strings.IndexFunc(s, foldedDigit) < 0 {
		return s
	}
	return strings.Map(func(r rune) rune {
		if !foldedDigit(r) {
			return r
		}
		zero := r
		for unicode.IsDigit(zero - 1) {
			zero--
		}
		return '0' + (r-zero)%10
	}, s)
}

func foldedDigit(r rune) bool {
	if r <= unicode.MaxASCII || !unicode.IsDigit(r) {
		return false
	}
	fullwidth := r >= 0xFF10 && r <= 0xFF19
	mathematical := r >= 0x1D7CE && r <= 0x1D7FF
	return !fullwidth && !mathematical
}
