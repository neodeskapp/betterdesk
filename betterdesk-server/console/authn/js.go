package authn

import (
	"math"
	"strings"
	"unicode/utf8"
)

// The helpers in this file give text the meaning it has in JavaScript,
// where the stored formats and the checks on them were defined: lengths
// count UTF-16 units, white space is JavaScript's, and numbers and hex
// text are read leniently.

// isJSSpace reports whether r is white space for String.prototype.trim and
// for \s in a regular expression.
func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return r >= 0x2000 && r <= 0x200A
}

// jsTrim is String.prototype.trim.
func jsTrim(s string) string { return strings.TrimFunc(s, isJSSpace) }

// utf16Len is the length property of a string: the number of UTF-16 units.
// A byte that is not UTF-8 counts as one unit.
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

// asciiLower lowers A to Z and leaves every other byte alone.
func asciiLower(s string) string {
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

func hexValue(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// lenientHex is Buffer.from(text, 'hex'): it decodes pairs of hex digits
// and stops without an error at the first pair that is not hex. A single
// digit at the end is dropped.
func lenientHex(s string) []byte {
	out := make([]byte, 0, len(s)/2)
	for i := 0; i+1 < len(s); i += 2 {
		hi, ok1 := hexValue(s[i])
		lo, ok2 := hexValue(s[i+1])
		if !ok1 || !ok2 {
			break
		}
		out = append(out, hi<<4|lo)
	}
	return out
}

// isHexRun reports whether s is exactly n hex digits.
func isHexRun(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for i := 0; i < len(s); i++ {
		if _, ok := hexValue(s[i]); !ok {
			return false
		}
	}
	return true
}

// parseInt is parseInt(text, 10): white space, an optional sign and then
// as many digits as there are. ok is false for NaN.
func parseInt(s string) (n float64, ok bool) {
	s = strings.TrimLeftFunc(s, isJSSpace)
	negative := false
	if s != "" && (s[0] == '+' || s[0] == '-') {
		negative = s[0] == '-'
		s = s[1:]
	}
	digits := 0
	for digits < len(s) && s[digits] >= '0' && s[digits] <= '9' {
		n = n*10 + float64(s[digits]-'0')
		digits++
	}
	if digits == 0 {
		return math.NaN(), false
	}
	if negative {
		n = -n
	}
	return n, true
}

// intOr is "parseInt(text, 10) || fallback": the fallback stands in for
// text that is not a number and for 0.
func intOr(s string, fallback int) int {
	n, ok := parseInt(s)
	if !ok || n == 0 || math.IsInf(n, 0) {
		return fallback
	}
	if n > math.MaxInt32 {
		return math.MaxInt32
	}
	if n < math.MinInt32 {
		return math.MinInt32
	}
	return int(n)
}

// toWellFormed returns s with every byte that is not UTF-8 replaced by
// U+FFFD, the text JavaScript holds after decoding such bytes.
func toWellFormed(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	return string([]rune(s))
}
