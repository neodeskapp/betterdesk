package ssrfguard

import (
	"sort"
	"strings"
	"unicode/utf8"
)

// isJSSpace reports the white space and line ends that JavaScript's trim
// removes.
func isJSSpace(r rune) bool {
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

// trim is String.prototype.trim.
func trim(s string) string { return strings.TrimFunc(s, isJSSpace) }

// toLowerCase is String.prototype.toLowerCase as the Node console ran
// it: the tables in casetables.go are that Node's, so a name is lowered
// here exactly as it was there. A capital sigma becomes the final sigma
// at the end of a word and the ordinary one elsewhere.
func toLowerCase(s string) string {
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			ascii = false
			break
		}
	}
	if ascii {
		return strings.ToLower(s)
	}
	runes := []rune(s)
	var b strings.Builder
	b.Grow(len(s))
	for i, r := range runes {
		switch low, mapped := lowerCase[r]; {
		case r == 0x3A3 && finalSigma(runes, i):
			b.WriteRune(0x3C2)
		case r == 0x3A3:
			b.WriteRune(0x3C3)
		case mapped:
			b.WriteString(low)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// finalSigma reports whether the sigma at runes[i] ends a word: a cased
// letter comes before it and none comes after it, not counting the
// characters the rule looks through (accents, apostrophes, full stops).
func finalSigma(runes []rune, i int) bool {
	before := false
	for j := i - 1; j >= 0; j-- {
		if inRanges(ignoredRanges, runes[j]) {
			continue
		}
		before = inRanges(casedRanges, runes[j])
		break
	}
	if !before {
		return false
	}
	for j := i + 1; j < len(runes); j++ {
		if inRanges(ignoredRanges, runes[j]) {
			continue
		}
		return !inRanges(casedRanges, runes[j])
	}
	return true
}

func inRanges(ranges [][2]rune, r rune) bool {
	i := sort.Search(len(ranges), func(i int) bool { return ranges[i][1] >= r })
	return i < len(ranges) && ranges[i][0] <= r
}
