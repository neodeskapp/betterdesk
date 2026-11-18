package billingclock

import (
	"strconv"
	"strings"
	"unicode"

	"github.com/unitronix/betterdesk-server/console/goapipath"
)

// jsTrim is String.prototype.trim: without the white space and line ends
// JavaScript removes.
func jsTrim(s string) string {
	return strings.TrimFunc(s, isJSSpace)
}

func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return r >= 0x2000 && r <= 0x200A
}

// numberText is String(n) for a JavaScript number.
func numberText(n float64) string { return goapipath.NumberText(n) }

// parseInt10 is parseInt(text, 10): white space in front is skipped, a
// sign is read, and the decimal digits up to the first other character
// are the number. ok is false where JavaScript gives NaN. Long runs of
// digits round as every JavaScript number does.
func parseInt10(text string) (value float64, ok bool) {
	s := strings.TrimLeftFunc(text, isJSSpace)
	negative := false
	if s != "" && (s[0] == '+' || s[0] == '-') {
		negative = s[0] == '-'
		s = s[1:]
	}
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0, false
	}
	value, _ = strconv.ParseFloat(s[:end], 64)
	if negative {
		value = -value
	}
	return value, true
}

// jsUpper is String.prototype.toUpperCase for what the flag words need:
// the simple mappings of Unicode, and the ligature ﬀ, which JavaScript
// writes as FF.
func jsUpper(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == 0xFB00 {
			b.WriteString("FF")
			continue
		}
		b.WriteRune(unicode.ToUpper(r))
	}
	return b.String()
}
