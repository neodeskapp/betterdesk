package goapi

import (
	"strings"
	"unicode"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

// collator orders texts the way the device list has always been ordered:
// by JavaScript's a.localeCompare(b) with no locale and no options, in an
// English locale. The letters decide first, then the accents, then the
// case, lower case first. Digits are compared one by one, not as numbers.
//
// A collator is for one goroutine.
type collator struct {
	c *collate.Collator
}

func newCollator() *collator {
	return &collator{c: collate.New(language.English)}
}

// compare returns a negative number, zero or a positive number.
func (c *collator) compare(a, b string) int {
	return c.c.CompareString(asciiDigits(a), asciiDigits(b))
}

// asciiDigits replaces the decimal digits of other scripts by 0 to 9.
// JavaScript's order does not tell an Arabic or Devanagari digit from the
// ASCII one; the collation tables used here do. The fullwidth and the
// mathematical digits stay: both orders put them after the ASCII digit.
func asciiDigits(s string) string {
	if strings.IndexFunc(s, foldedDigit) < 0 {
		return s
	}
	return strings.Map(func(r rune) rune {
		if !foldedDigit(r) {
			return r
		}
		// The digits of a script are ten code points in a row, from 0 to 9.
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
