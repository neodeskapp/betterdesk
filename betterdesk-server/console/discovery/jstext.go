package discovery

import (
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

// decodeUTF8 is Buffer.toString('utf8'): each broken sequence becomes one
// replacement character, as the WHATWG decoder does it.
func decodeUTF8(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	var out strings.Builder
	out.Grow(len(b))
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

// isJSSpace is JavaScript's white space and line terminators, what
// String.prototype.trim removes.
func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// jsTrim is String.prototype.trim.
func jsTrim(s string) string { return strings.TrimFunc(s, isJSSpace) }

// appendJSONString appends s as JSON.stringify writes a string: quotes,
// backslashes and control characters escaped, everything else as it is.
func appendJSONString(dst []byte, s string) []byte {
	const hex = "0123456789abcdef"
	dst = append(dst, '"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			dst = append(dst, '\\', '"')
		case c == '\\':
			dst = append(dst, '\\', '\\')
		case c == '\b':
			dst = append(dst, '\\', 'b')
		case c == '\f':
			dst = append(dst, '\\', 'f')
		case c == '\n':
			dst = append(dst, '\\', 'n')
		case c == '\r':
			dst = append(dst, '\\', 'r')
		case c == '\t':
			dst = append(dst, '\\', 't')
		case c < 0x20:
			dst = append(dst, '\\', 'u', '0', '0', hex[c>>4], hex[c&0xf])
		default:
			dst = append(dst, c)
		}
	}
	return append(dst, '"')
}

// appendJSONNumber appends n as JSON.stringify writes a number: null for
// what is not finite.
func appendJSONNumber(dst []byte, n float64) []byte {
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return append(dst, "null"...)
	}
	return append(dst, numberString(n)...)
}

// numberString is String(n) for a JavaScript number.
func numberString(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	case f == 0:
		return "0"
	}
	sign := ""
	if f < 0 {
		sign, f = "-", -f
	}
	mantissa, expText, _ := strings.Cut(strconv.FormatFloat(f, 'e', -1, 64), "e")
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
	e := n - 1
	expSign := "+"
	if e < 0 {
		expSign, e = "-", -e
	}
	if k == 1 {
		return sign + digits + "e" + expSign + strconv.Itoa(e)
	}
	return sign + digits[:1] + "." + digits[1:] + "e" + expSign + strconv.Itoa(e)
}

// parseIntOr is parseInt(text, 10) || fallback: white space in front is
// skipped, a sign is read, and the decimal digits up to the first other
// character are the number, rounded like every JavaScript number. No
// digits, or a value of zero, give the fallback.
func parseIntOr(text string, fallback float64) float64 {
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
		return fallback
	}
	value, _ := strconv.ParseFloat(s[:end], 64)
	if value == 0 {
		return fallback
	}
	if negative {
		value = -value
	}
	return value
}

// toUint16 is what Node's socket bind makes of a port number: ToUint32,
// then the low 16 bits. 0 asks the system for any free port.
func toUint16(n float64) uint16 {
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return 0
	}
	n = math.Trunc(n)
	m := math.Mod(n, 1<<32)
	if m < 0 {
		m += 1 << 32
	}
	return uint16(uint64(m) & 0xffff)
}
