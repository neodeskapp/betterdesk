package message

import (
	"strings"
	"unicode/utf8"
)

// The message is built by rules that were written for JavaScript strings,
// which are sequences of UTF-16 code units: lengths count units, so a
// character outside the Basic Multilingual Plane counts as two. The helpers
// here give Go strings the same view.

// utf16Units converts a string to UTF-16 code units. Bytes that are no valid
// UTF-8 become U+FFFD.
func utf16Units(s string) []uint16 {
	units := make([]uint16, 0, len(s))
	for _, r := range s {
		if r >= 0x10000 {
			r -= 0x10000
			units = append(units, uint16(0xd800+(r>>10)), uint16(0xdc00+(r&0x3ff)))
		} else {
			units = append(units, uint16(r))
		}
	}
	return units
}

// unitsString converts UTF-16 code units back to a string. Half of a
// surrogate pair on its own becomes U+FFFD.
func unitsString(units []uint16) string {
	var out strings.Builder
	out.Grow(len(units))
	for i := 0; i < len(units); i++ {
		u := rune(units[i])
		switch {
		case u >= 0xd800 && u < 0xdc00 && i+1 < len(units) && units[i+1] >= 0xdc00 && units[i+1] < 0xe000:
			out.WriteRune(0x10000 + (u-0xd800)<<10 + rune(units[i+1]) - 0xdc00)
			i++
		case u >= 0xd800 && u < 0xe000:
			out.WriteRune(utf8.RuneError)
		default:
			out.WriteRune(u)
		}
	}
	return out.String()
}

// isJSSpace reports whether a character is white space for JavaScript: what
// \s matches in a regular expression and what String.prototype.trim removes.
func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// jsTrim removes white space from both ends, as String.prototype.trim does.
func jsTrim(s string) string {
	return strings.TrimFunc(s, isJSSpace)
}

// jsToLower lowers a string as String.prototype.toLowerCase does: by the
// Unicode lower-case mapping without a language, where a capital sigma
// becomes the final form of the small one at the end of a word.
func jsToLower(t *unicodeTables, s string) string {
	if isASCII(s) {
		return asciiLower(s)
	}
	runes := []rune(s)
	var out strings.Builder
	out.Grow(len(s))
	for i, r := range runes {
		switch mapped, changes := t.lower[r]; {
		case r == 0x3a3 && isFinalSigma(t, runes, i):
			out.WriteRune(0x3c2)
		case changes:
			for _, m := range mapped {
				out.WriteRune(m)
			}
		default:
			out.WriteRune(r)
		}
	}
	return out.String()
}

const (
	caseClassCased   = 1
	caseClassIgnored = 2
)

// isFinalSigma reports whether the sigma at position i ends a word: a cased
// letter stands before it and none after it, where characters such as
// accents and apostrophes in between are skipped.
func isFinalSigma(t *unicodeTables, runes []rune, i int) bool {
	before := false
	for j := i - 1; j >= 0; j-- {
		class := rangeValue(t.caseClass, uint32(runes[j]))
		if class == caseClassIgnored {
			continue
		}
		before = class == caseClassCased
		break
	}
	if !before {
		return false
	}
	for j := i + 1; j < len(runes); j++ {
		class := rangeValue(t.caseClass, uint32(runes[j]))
		if class == caseClassIgnored {
			continue
		}
		return class != caseClassCased
	}
	return true
}
