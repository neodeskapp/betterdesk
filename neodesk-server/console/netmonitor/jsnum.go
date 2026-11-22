package netmonitor

import (
	"math"
	"strconv"
	"strings"
	"unicode"
)

// Number is a number as the JavaScript console held it: a float64 that can
// also be NaN or infinite. Timeouts, ports and intervals reach the checks
// as such numbers, because they come from parseInt of whatever the client
// sent.
type Number float64

// NaN is the number parseInt gives for text without digits.
func NaN() Number { return Number(math.NaN()) }

// IsNaN reports whether n is NaN.
func (n Number) IsNaN() bool { return math.IsNaN(float64(n)) }

// Truthy is !!n: false for 0 and NaN.
func (n Number) Truthy() bool { return n != 0 && !n.IsNaN() }

// Or is n || fallback.
func (n Number) Or(fallback Number) Number {
	if n.Truthy() {
		return n
	}
	return fallback
}

// String is String(n).
func (n Number) String() string {
	f := float64(n)
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	}
	return numberString(f)
}

// MarshalJSON writes the number as JSON.stringify does: null for NaN and
// the infinities.
func (n Number) MarshalJSON() ([]byte, error) {
	f := float64(n)
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return []byte("null"), nil
	}
	return []byte(numberString(f)), nil
}

// isInteger is Number.isInteger(n).
func (n Number) isInteger() bool {
	f := float64(n)
	return !math.IsNaN(f) && !math.IsInf(f, 0) && f == math.Trunc(f)
}

// inspect is util.inspect(n): String(n), and "-0" for the negative zero.
func (n Number) inspect() string {
	if f := float64(n); f == 0 && math.Signbit(f) {
		return "-0"
	}
	return n.String()
}

// received is how Node prints the number at the end of a range error:
// like inspect, but a whole number beyond 2^32 gets an underscore before
// every third character from the end of String(n), whatever the
// characters are ("-5_000_000_000", "1e_+23").
func (n Number) received() string {
	if !n.isInteger() || math.Abs(float64(n)) <= 1<<32 {
		return n.inspect()
	}
	text := n.String()
	start := 0
	if text[0] == '-' {
		start = 1
	}
	rest := ""
	i := len(text)
	for ; i >= start+4; i -= 3 {
		rest = "_" + text[i-3:i] + rest
	}
	return text[:i] + rest
}

// numberString is String(f) for a finite number: the shortest digits that
// read back as f, plain up to 21 digits and down to 1e-7, with an exponent
// beyond.
func numberString(f float64) string {
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

// ParseInt is parseInt(s, 10): white space, an optional sign, then the
// digits up to the first other character. Text without a digit there is
// NaN.
func ParseInt(s string) Number {
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
		return NaN()
	}
	f, err := strconv.ParseFloat(s[:end], 64)
	if err != nil && !math.IsInf(f, 0) {
		return NaN()
	}
	if negative {
		f = -f
	}
	return Number(f)
}
