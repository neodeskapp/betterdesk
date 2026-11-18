package branding

import (
	"math"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// jsSpaceClass lists, for a regular expression character class, what \s
// matches in JavaScript: more than Go's \s, which stops at ASCII.
const jsSpaceClass = `\t\n\x0B\f\r \x{A0}\x{1680}\x{2000}-\x{200A}\x{2028}\x{2029}\x{202F}\x{205F}\x{3000}\x{FEFF}`

// isJSSpace reports whether r is white space or a line end in JavaScript:
// what trim removes and \s matches.
func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return r >= 0x2000 && r <= 0x200A
}

// trim is String.prototype.trim.
func trim(s string) string {
	return strings.TrimFunc(s, isJSSpace)
}

// trimStart removes leading JavaScript white space.
func trimStart(s string) string {
	return strings.TrimLeftFunc(s, isJSSpace)
}

// toLower is String.prototype.toLowerCase: Unicode's lower case without a
// locale. It differs from mapping every character on its own in two
// places: İ becomes i followed by a combining dot, and a capital sigma at
// the end of a word becomes the final form ς.
func toLower(s string) string {
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
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] == 0xED && isSurrogateAt(s, i) {
			b.WriteString(s[i : i+3])
			i += 3
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			b.WriteByte(s[i])
		case r == 0x130:
			b.WriteString("i\u0307")
		case r == 0x3A3:
			if finalSigma(s, i, i+size) {
				b.WriteRune(0x3C2)
			} else {
				b.WriteRune(0x3C3)
			}
		default:
			b.WriteRune(unicode.ToLower(r))
		}
		i += size
	}
	return b.String()
}

// finalSigma reports whether the sigma at s[start:end] ends a word: a
// cased letter comes before it and none comes after it, looking past
// characters that case rules ignore.
func finalSigma(s string, start, end int) bool {
	before := false
	for i := start; i > 0; {
		r, size := utf8.DecodeLastRuneInString(s[:i])
		i -= size
		if caseIgnorable(r) {
			continue
		}
		before = cased(r)
		break
	}
	if !before {
		return false
	}
	for i := end; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		if caseIgnorable(r) {
			continue
		}
		return !cased(r)
	}
	return true
}

func cased(r rune) bool {
	return unicode.IsUpper(r) || unicode.IsLower(r) || unicode.IsTitle(r) ||
		unicode.Is(unicode.Other_Lowercase, r) || unicode.Is(unicode.Other_Uppercase, r)
}

func caseIgnorable(r rune) bool {
	switch r {
	case '\'', '.', ':', 0xB7, 0x387, 0x55F, 0x5F4, 0x2018, 0x2019, 0x2024, 0x2027, 0xFE13, 0xFE52, 0xFE55, 0xFF07, 0xFF0E, 0xFF1A:
		return true
	}
	return unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf, unicode.Lm, unicode.Sk)
}

// parseFloat is the global parseFloat: the number at the start of the
// text, after white space, and NaN when there is none.
func parseFloat(v any) float64 {
	s := trimStart(toString(v))
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	if strings.HasPrefix(s[i:], "Infinity") {
		if s[0] == '-' {
			return math.Inf(-1)
		}
		return math.Inf(1)
	}
	digits := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
		digits++
	}
	if i < len(s) && s[i] == '.' {
		j := i + 1
		fraction := 0
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
			fraction++
		}
		if digits > 0 || fraction > 0 {
			i = j
			digits += fraction
		}
	}
	if digits == 0 {
		return math.NaN()
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		j := i + 1
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			j++
		}
		k := j
		for k < len(s) && s[k] >= '0' && s[k] <= '9' {
			k++
		}
		if k > j {
			i = k
		}
	}
	f, err := strconv.ParseFloat(s[:i], 64)
	if err != nil && !math.IsInf(f, 0) {
		return math.NaN()
	}
	return f
}

// clampNumber reads a number from a setting and keeps it within min and
// max. ok is false when the setting holds no finite number.
func clampNumber(v any, min, max float64) (n float64, ok bool) {
	n = parseFloat(v)
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return 0, false
	}
	return math.Min(max, math.Max(min, n)), true
}

// encodeURIComponent is the global function of that name. A lone surrogate
// throws a URIError, as it does in JavaScript.
func encodeURIComponent(s string) string {
	const upperHex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == 0xED && isSurrogateAt(s, i) {
			panic(&Error{Name: "URIError", Message: "URI malformed"})
		}
		if c < utf8.RuneSelf && (c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("-_.!~*'()", c) >= 0) {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(upperHex[c>>4])
		b.WriteByte(upperHex[c&0xF])
	}
	return b.String()
}

// DecodeURIComponent is the global function of that name: %XX escapes are
// decoded, and a % without two hex digits after it, or escaped bytes that
// are not one well-formed UTF-8 character, fail with the URIError "URI
// malformed".
func DecodeURIComponent(s string) (string, error) {
	malformed := &Error{Name: "URIError", Message: "URI malformed"}
	escaped := func(i int) (byte, bool) {
		if i+2 >= len(s) || s[i] != '%' || !isHex(s[i+1]) || !isHex(s[i+2]) {
			return 0, false
		}
		v, _ := strconv.ParseUint(s[i+1:i+3], 16, 8)
		return byte(v), true
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '%' {
			b.WriteByte(s[i])
			continue
		}
		first, ok := escaped(i)
		if !ok {
			return "", malformed
		}
		i += 2
		size := 1
		switch {
		case first < 0x80:
		case first&0xE0 == 0xC0:
			size = 2
		case first&0xF0 == 0xE0:
			size = 3
		case first&0xF8 == 0xF0:
			size = 4
		default:
			return "", malformed
		}
		seq := []byte{first}
		for len(seq) < size {
			if i+1 >= len(s) {
				return "", malformed
			}
			next, ok := escaped(i + 1)
			if !ok || next&0xC0 != 0x80 {
				return "", malformed
			}
			seq = append(seq, next)
			i += 3
		}
		if size > 1 && !utf8.Valid(seq) {
			return "", malformed
		}
		b.Write(seq)
	}
	return b.String(), nil
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

// posixBasename is path.basename on POSIX: the last part of a path, without
// trailing slashes.
func posixBasename(p string) string {
	end := len(p)
	for end > 0 && p[end-1] == '/' {
		end--
	}
	start := strings.LastIndexByte(p[:end], '/') + 1
	return p[start:end]
}

// posixExtname is path.extname on POSIX: from the last dot of the last
// part of a path, and "" when that part has no dot or only starts with one.
func posixExtname(p string) string {
	base := posixBasename(p)
	dot := strings.LastIndexByte(base, '.')
	if dot <= 0 {
		return ""
	}
	if dot == 1 && base[0] == '.' && len(base) == 2 {
		return ""
	}
	return base[dot:]
}

// basenameWithout is path.basename(p, ext): the last part of a path with
// ext cut from its end, unless ext is all of it.
func basenameWithout(p, ext string) string {
	base := posixBasename(p)
	if ext != "" && base != ext && strings.HasSuffix(base, ext) {
		return base[:len(base)-len(ext)]
	}
	return base
}
