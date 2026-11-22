package goapipath

import (
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf16"
)

// ParseInt is JavaScript's parseInt(text) without a radix: white space in
// front is skipped, a sign is read, "0x" starts a hexadecimal number, and
// the digits up to the first other character are the number. Text without
// a digit there is NaN. A number with more digits than a float64 holds is
// rounded like every JavaScript number, and one beyond its range is
// infinite.
func ParseInt(text string) float64 { return parseInt(text, true) }

// parseInt is parseInt(text) when hex is true and parseInt(text, 10)
// otherwise, which reads "0x10" as 0.
func parseInt(text string, hex bool) float64 {
	s := strings.TrimLeftFunc(text, isJSSpace)
	negative := false
	if s != "" && (s[0] == '+' || s[0] == '-') {
		negative = s[0] == '-'
		s = s[1:]
	}
	base := 10
	if hex && len(s) >= 2 && s[0] == '0' && (s[1] == 'x' || s[1] == 'X') {
		base, s = 16, s[2:]
	}
	end := 0
	for end < len(s) && isDigit(s[end], base) {
		end++
	}
	if end == 0 {
		return math.NaN()
	}
	var value float64
	if base == 10 {
		value, _ = strconv.ParseFloat(s[:end], 64)
	} else {
		whole, _ := new(big.Int).SetString(s[:end], base)
		value, _ = new(big.Float).SetInt(whole).Float64()
	}
	if negative {
		value = -value
	}
	return value
}

func isDigit(c byte, base int) bool {
	switch {
	case c >= '0' && c <= '9':
		return true
	case base == 16:
		return c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
	}
	return false
}

// NumberText is String(n) for a JavaScript number: whole numbers below
// 1e21 with all their digits, larger ones as 1.5e+21, and NaN, Infinity
// and -Infinity by name. Negative zero is "0".
func NumberText(n float64) string {
	switch {
	case math.IsNaN(n):
		return "NaN"
	case math.IsInf(n, 1):
		return "Infinity"
	case math.IsInf(n, -1):
		return "-Infinity"
	case n == 0:
		return "0"
	}
	magnitude := math.Abs(n)
	if magnitude >= 1e21 || magnitude < 1e-6 {
		text := strconv.FormatFloat(n, 'e', -1, 64)
		mantissa, exponent, _ := strings.Cut(text, "e")
		sign := exponent[:1]
		digits := strings.TrimLeft(exponent[1:], "0")
		return mantissa + "e" + sign + digits
	}
	return strconv.FormatFloat(n, 'f', -1, 64)
}

// Slice is text.slice(0, n): the first n UTF-16 code units of text. A
// character outside the basic plane counts as two; when the cut falls
// between them, the half that is left becomes U+FFFD, which is what
// such a half turns into when the text goes into a URL or into JSON.
func Slice(text string, n int) string {
	units := utf16.Encode([]rune(text))
	if len(units) <= n {
		return text
	}
	return string(utf16.Decode(units[:n]))
}

// FormEncode is new URLSearchParams with one set(key, value) per pair,
// then toString(): key=value joined by "&", with a space as "+", letters,
// digits and * - . _ as they are, and every other byte as %XX.
func FormEncode(pairs ...string) string {
	var b strings.Builder
	for i := 0; i+1 < len(pairs); i += 2 {
		if i > 0 {
			b.WriteByte('&')
		}
		formEscape(&b, pairs[i])
		b.WriteByte('=')
		formEscape(&b, pairs[i+1])
	}
	return b.String()
}

func formEscape(b *strings.Builder, s string) {
	const upperhex = "0123456789ABCDEF"
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '*', c == '-', c == '.', c == '_':
			b.WriteByte(c)
		case c == ' ':
			b.WriteByte('+')
		default:
			b.WriteByte('%')
			b.WriteByte(upperhex[c>>4])
			b.WriteByte(upperhex[c&15])
		}
	}
}
