package middleware

import (
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// formField is one key of a form body. A key sent once has one value, a
// key sent several times has them all, in order.
type formField struct {
	key    string
	values []string
}

// maxFormParameters is how many key=value pairs a form body may have.
const maxFormParameters = 1000

// tooManyParameters reports a form body with more pairs than allowed.
func tooManyParameters(body string) bool {
	return strings.Count(body, "&")+1 > maxFormParameters
}

// parseForm reads an application/x-www-form-urlencoded body with the
// rules of Node's querystring.parse: pairs split at &, key and value at
// the first =, + is a space, %XX is decoded. Keys are flat: a[b] and a[]
// are names like any other. An empty pair is skipped, a pair without = has
// the value "".
func parseForm(body string) []formField {
	var fields []formField
	index := map[string]int{}
	for _, pair := range strings.Split(body, "&") {
		if pair == "" {
			continue
		}
		key, value, _ := strings.Cut(pair, "=")
		key, value = formDecode(key), formDecode(value)
		if i, seen := index[key]; seen {
			fields[i].values = append(fields[i].values, value)
			continue
		}
		index[key] = len(fields)
		fields = append(fields, formField{key: key, values: []string{value}})
	}
	return fields
}

func isHexByte(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func hexValue(c byte) byte {
	switch {
	case c >= 'a':
		return c - 'a' + 10
	case c >= 'A':
		return c - 'A' + 10
	}
	return c - '0'
}

// formDecode decodes one key or value. Text without a valid %XX is taken
// as it is, apart from + for space.
func formDecode(s string) string {
	s = strings.ReplaceAll(s, "+", " ")
	escaped := false
	for i := 0; i+2 < len(s); i++ {
		if s[i] == '%' && isHexByte(s[i+1]) && isHexByte(s[i+2]) {
			escaped = true
			break
		}
	}
	if !escaped {
		return s
	}
	if decoded, ok := decodeURIComponent(s); ok {
		return decoded
	}
	// The lenient way for text JavaScript's decodeURIComponent refuses:
	// every valid %XX becomes its byte, a stray % stays, and each UTF-16
	// unit of the other letters is cut down to its low byte. Then the
	// bytes are read as UTF-8.
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); {
		c := s[i]
		if c == '%' && i+2 < len(s) && isHexByte(s[i+1]) && isHexByte(s[i+2]) {
			out = append(out, hexValue(s[i+1])<<4|hexValue(s[i+2]))
			i += 3
			continue
		}
		if c < utf8.RuneSelf {
			out = append(out, c)
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r1, r2 := utf16.EncodeRune(r); r1 != utf8.RuneError {
			out = append(out, byte(r1), byte(r2))
		} else {
			out = append(out, byte(r))
		}
		i += size
	}
	return decodeUTF8(out)
}

// decodeURIComponent decodes %XX the way JavaScript does and fails where
// JavaScript throws: at a % without two hex digits, and at escaped bytes
// that are not one well-formed UTF-8 sequence.
func decodeURIComponent(s string) (string, bool) {
	out := make([]byte, 0, len(s))
	escapedAt := func(i int) (byte, bool) {
		if i+2 >= len(s) || s[i] != '%' || !isHexByte(s[i+1]) || !isHexByte(s[i+2]) {
			return 0, false
		}
		return hexValue(s[i+1])<<4 | hexValue(s[i+2]), true
	}
	for i := 0; i < len(s); i++ {
		if s[i] != '%' {
			out = append(out, s[i])
			continue
		}
		first, ok := escapedAt(i)
		if !ok {
			return "", false
		}
		i += 2
		size := 1
		switch {
		case first < 0x80:
		case first >= 0xc2 && first <= 0xdf:
			size = 2
		case first >= 0xe0 && first <= 0xef:
			size = 3
		case first >= 0xf0 && first <= 0xf4:
			size = 4
		default:
			return "", false
		}
		seq := []byte{first}
		for len(seq) < size {
			next, ok := escapedAt(i + 1)
			if !ok {
				return "", false
			}
			seq = append(seq, next)
			i += 3
		}
		if !utf8.Valid(seq) {
			return "", false
		}
		out = append(out, seq...)
	}
	return string(out), true
}
