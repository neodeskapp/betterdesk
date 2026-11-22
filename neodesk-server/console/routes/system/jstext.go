package system

import (
	"math"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The routes of this group cut command output into fields the way the
// JavaScript they replace did. These are the few rules that takes.

// isJSSpace reports the white space and line ends of JavaScript's trim and
// of \s in its patterns.
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

// fields is s.split(/\s+/): unlike strings.Fields it keeps an empty first
// or last field when the text starts or ends with white space.
func fields(s string) []string {
	var out []string
	start, inSpace := 0, false
	for i, r := range s {
		if isJSSpace(r) {
			if !inSpace {
				out = append(out, s[start:i])
				inSpace = true
			}
			start = i + utf8.RuneLen(r)
			continue
		}
		inSpace = false
	}
	return append(out, s[start:])
}

// parseFloat is parseFloat(s) || 0: the number at the start of the text,
// 0 when there is none.
func parseFloat(s string) float64 {
	s = strings.TrimLeftFunc(s, isJSSpace)
	end, digits := 0, 0
	if end < len(s) && (s[end] == '+' || s[end] == '-') {
		end++
	}
	if strings.HasPrefix(s[end:], "Infinity") {
		if s[0] == '-' {
			return math.Inf(-1)
		}
		return math.Inf(1)
	}
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
		digits++
	}
	if end < len(s) && s[end] == '.' {
		end++
		for end < len(s) && s[end] >= '0' && s[end] <= '9' {
			end++
			digits++
		}
	}
	if digits == 0 {
		return 0
	}
	if end < len(s) && (s[end] == 'e' || s[end] == 'E') {
		exp := end + 1
		if exp < len(s) && (s[exp] == '+' || s[exp] == '-') {
			exp++
		}
		if exp < len(s) && s[exp] >= '0' && s[exp] <= '9' {
			for exp < len(s) && s[exp] >= '0' && s[exp] <= '9' {
				exp++
			}
			end = exp
		}
	}
	f, _ := strconv.ParseFloat(strings.TrimSuffix(s[:end], "."), 64)
	if math.IsNaN(f) {
		return 0
	}
	return f
}

// parseInt is parseInt(s, 10): white space, a sign and digits from the
// start of the text. ok is false when there are no digits (NaN).
func parseInt(s string) (n float64, ok bool) {
	s = strings.TrimLeftFunc(s, isJSSpace)
	sign := 1.0
	if s != "" && (s[0] == '+' || s[0] == '-') {
		if s[0] == '-' {
			sign = -1
		}
		s = s[1:]
	}
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0, false
	}
	n, _ = strconv.ParseFloat(s[:end], 64)
	return sign * n, true
}

// parseIntOr is parseInt(s, 10) || fallback.
func parseIntOr(s string, fallback float64) float64 {
	if n, ok := parseInt(s); ok && n != 0 {
		return n
	}
	return fallback
}

// numberText is String(n) for a whole number, which may be infinite: a
// text of more than 308 digits reads as one.
func numberText(n float64) string {
	switch {
	case math.IsInf(n, 1):
		return "Infinity"
	case math.IsInf(n, -1):
		return "-Infinity"
	}
	if math.Abs(n) < 1e21 {
		return strconv.FormatFloat(n, 'f', -1, 64)
	}
	text := strconv.FormatFloat(n, 'e', -1, 64)
	mantissa, exp, _ := strings.Cut(text, "e")
	return mantissa + "e" + exp[:1] + strings.TrimLeft(exp[1:], "0")
}

// substring is s.substring(0, n), counted in UTF-16 units.
func substring(s string, n int) string {
	units := 0
	for i, r := range s {
		size := 1
		if r >= 0x10000 {
			size = 2
		}
		if units+size > n {
			return s[:i]
		}
		units += size
	}
	return s
}

// decodeUTF8 reads command output or a file as text: each broken
// sequence becomes one replacement character.
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
				out.WriteRune(0xFFFD)
			}
			continue
		}
		if c < lower || c > upper {
			needed, seen, cp = 0, 0, 0
			lower, upper = 0x80, 0xBF
			out.WriteRune(0xFFFD)
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
		out.WriteRune(0xFFFD)
	}
	return out.String()
}

// jsText is a text that goes into a JSON answer the way JSON.stringify
// writes it: control characters escaped, everything else as it is.
type jsText string

const hexDigits = "0123456789abcdef"

// MarshalJSON writes the text as JSON.stringify would.
func (t jsText) MarshalJSON() ([]byte, error) {
	s := string(t)
	out := make([]byte, 0, len(s)+2)
	out = append(out, '"')
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '"':
			out = append(out, '\\', '"')
		case c == '\\':
			out = append(out, '\\', '\\')
		case c == '\b':
			out = append(out, '\\', 'b')
		case c == '\f':
			out = append(out, '\\', 'f')
		case c == '\n':
			out = append(out, '\\', 'n')
		case c == '\r':
			out = append(out, '\\', 'r')
		case c == '\t':
			out = append(out, '\\', 't')
		case c < 0x20:
			out = append(out, '\\', 'u', '0', '0', hexDigits[c>>4], hexDigits[c&0xF])
		default:
			out = append(out, c)
		}
	}
	return append(out, '"'), nil
}

// queryValue is req.query[name] for the shapes a query string gives it:
//
//	absent                         nil
//	name=a                         "a"
//	name=a&name=b, name[]=a        []string
//	name[k]=a                      map of key to text, keys in their order
//
// Deeper nesting (name[a][b]=1) is read as one level with the key "a][b".
func queryValue(rawQuery, name string) any {
	var plain []string
	var keys []string
	members := map[string]string{}
	list := false
	for _, pair := range strings.Split(rawQuery, "&") {
		if pair == "" {
			continue
		}
		rawKey, rawValue, _ := strings.Cut(pair, "=")
		key, value := queryDecode(rawKey), queryDecode(rawValue)
		switch {
		case key == name:
			plain = append(plain, value)
		case key == name+"[]":
			plain = append(plain, value)
			list = true
		case strings.HasPrefix(key, name+"[") && strings.HasSuffix(key, "]"):
			member := key[len(name)+1 : len(key)-1]
			if _, seen := members[member]; !seen {
				keys = append(keys, member)
			}
			members[member] = value
		}
	}
	switch {
	case len(keys) > 0:
		return orderedObject{keys: keys, values: members}
	case len(plain) == 0:
		return nil
	case len(plain) == 1 && !list:
		return plain[0]
	}
	return plain
}

// queryText is String(req.query[name]): a list joined with commas, and
// "" for an absent value. ok is false where JavaScript cannot turn the
// value into text: an object with a member "toString" (name[toString]=1).
func queryText(rawQuery, name string) (text string, ok bool) {
	switch v := queryValue(rawQuery, name).(type) {
	case string:
		return v, true
	case []string:
		return strings.Join(v, ","), true
	case orderedObject:
		_, hidden := v.values["toString"]
		return "[object Object]", !hidden
	}
	return "", true
}

func queryDecode(s string) string {
	s = strings.ReplaceAll(s, "+", " ")
	if decoded, err := url.PathUnescape(s); err == nil {
		return decoded
	}
	return s
}

// orderedObject is an object of texts that keeps the order of its keys.
type orderedObject struct {
	keys   []string
	values map[string]string
}

// MarshalJSON writes the object with its keys in their order.
func (o orderedObject) MarshalJSON() ([]byte, error) {
	out := []byte{'{'}
	for i, key := range o.keys {
		if i > 0 {
			out = append(out, ',')
		}
		k, _ := jsText(key).MarshalJSON()
		v, _ := jsText(o.values[key]).MarshalJSON()
		out = append(append(append(out, k...), ':'), v...)
	}
	return append(out, '}'), nil
}
