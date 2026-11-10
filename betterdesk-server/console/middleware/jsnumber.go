package middleware

import (
	"math"
	"strconv"
	"strings"
	"unicode"
)

// maxSafeInteger is JavaScript's Number.MAX_SAFE_INTEGER.
const maxSafeInteger = 1<<53 - 1

// jsNumber reads a JSON number the way JavaScript does: as a float64. A
// literal too large for one is infinite.
func jsNumber(literal string) float64 {
	f, err := strconv.ParseFloat(literal, 64)
	if err != nil && !math.IsInf(f, 0) {
		return math.NaN()
	}
	return f
}

// jsNumberString is String(n) for a finite number: the shortest digits
// that read back as n, plain up to 21 digits and down to 1e-7, with an
// exponent beyond.
func jsNumberString(f float64) string {
	if f == 0 {
		return "0"
	}
	sign := ""
	if f < 0 {
		sign, f = "-", -f
	}
	// digits and the decimal exponent n: 0.d1d2...dk x 10^n
	sci := strconv.FormatFloat(f, 'e', -1, 64)
	mantissa, expText, _ := strings.Cut(sci, "e")
	exp, _ := strconv.Atoi(expText)
	digits := strings.Replace(mantissa, ".", "", 1)
	k, n := len(digits), exp+1
	switch {
	case k <= n && n <= 21:
		return sign + digits + strings.Repeat("0", n-k)
	case 0 < n && n <= 21:
		return sign + digits[:n] + "." + digits[n:]
	case -6 < n && n <= 0:
		return sign + "0." + strings.Repeat("0", -n) + digits
	}
	e := strconv.Itoa(n - 1)
	if n-1 > 0 {
		e = "+" + e
	}
	if k == 1 {
		return sign + digits + "e" + e
	}
	return sign + digits[:1] + "." + digits[1:] + "e" + e
}

// isJSSpace is true for what JavaScript's trim and parseInt skip.
func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a || unicode.Is(unicode.Zs, r)
}

// jsParseInt is parseInt(s, 10): white space, an optional sign, then the
// digits up to the first other letter. ok is false when there is no digit.
func jsParseInt(s string) (value float64, ok bool) {
	s = strings.TrimLeftFunc(s, isJSSpace)
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
