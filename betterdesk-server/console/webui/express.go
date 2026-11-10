package webui

import (
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/unitronix/betterdesk-server/console/httpx"
)

// request is the address of a request as the console's routing reads it.
type request struct {
	original string // path and query as sent, not decoded
	path     string // the path, not decoded
	query    string // what follows the "?"
}

func parseRequest(r *http.Request) request {
	original := httpx.OriginalURL(r)
	target := original
	if !strings.HasPrefix(target, "/") && r.URL != nil {
		target = httpx.Latin1(r.URL.RequestURI())
	}
	if i := strings.IndexByte(target, '#'); i >= 0 {
		target = target[:i]
	}
	path, query, _ := strings.Cut(target, "?")
	if path == "" {
		path = "/"
	}
	return request{original: original, path: path, query: query}
}

// uriReserved are the characters decodeURI leaves percent-encoded.
const uriReserved = ";/?:@&=+$,#"

// decodeURIComponent decodes percent escapes as JavaScript does. It fails
// on a "%" without two hex digits and on escapes that are not UTF-8.
func decodeURIComponent(s string) (string, bool) {
	return decodeEscapes(s, "")
}

// decodeURI is decodeURIComponent, but an escape of a reserved character
// stays as written.
func decodeURI(s string) (string, bool) {
	return decodeEscapes(s, uriReserved)
}

func decodeEscapes(s, keep string) (string, bool) {
	if !strings.Contains(s, "%") {
		return s, true
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] != '%' {
			b.WriteByte(s[i])
			i++
			continue
		}
		first, ok := escapedByte(s, i)
		if !ok {
			return "", false
		}
		if first < utf8.RuneSelf {
			if strings.IndexByte(keep, first) >= 0 {
				b.WriteString(s[i : i+3])
			} else {
				b.WriteByte(first)
			}
			i += 3
			continue
		}
		size := 0
		switch {
		case first&0xE0 == 0xC0:
			size = 2
		case first&0xF0 == 0xE0:
			size = 3
		case first&0xF8 == 0xF0:
			size = 4
		default:
			return "", false
		}
		var buf [4]byte
		buf[0] = first
		for n := 1; n < size; n++ {
			next, ok := escapedByte(s, i+3*n)
			if !ok || next&0xC0 != 0x80 {
				return "", false
			}
			buf[n] = next
		}
		if !utf8.Valid(buf[:size]) {
			return "", false
		}
		b.Write(buf[:size])
		i += 3 * size
	}
	return b.String(), true
}

func escapedByte(s string, i int) (byte, bool) {
	if i+2 >= len(s) || s[i] != '%' || !isHex(s[i+1]) || !isHex(s[i+2]) {
		return 0, false
	}
	return unhex(s[i+1])<<4 | unhex(s[i+2]), true
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func unhex(c byte) byte {
	switch {
	case c >= 'a':
		return c - 'a' + 10
	case c >= 'A':
		return c - 'A' + 10
	}
	return c - '0'
}

// extname is the extension of the last name in a slash-separated path, as
// Node's path.extname gives it: from the last dot to the end, and nothing
// when the name starts with that dot.
func extname(path string) string {
	startDot, startPart, end := -1, 0, -1
	matchedSlash := true
	preDotState := 0
	for i := len(path) - 1; i >= 0; i-- {
		c := path[i]
		if c == '/' {
			if !matchedSlash {
				startPart = i + 1
				break
			}
			continue
		}
		if end == -1 {
			matchedSlash = false
			end = i + 1
		}
		if c == '.' {
			if startDot == -1 {
				startDot = i
			} else if preDotState != 1 {
				preDotState = 1
			}
		} else if startDot != -1 {
			preDotState = -1
		}
	}
	if startDot == -1 || end == -1 || preDotState == 0 ||
		(preDotState == 1 && startDot == end-1 && startDot == startPart+1) {
		return ""
	}
	return path[startDot:end]
}

func isAlphanumeric(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// jsSpaceChars is what JavaScript's \s matches, for use inside a character
// class; jsSpace is that class.
const (
	jsSpaceChars = `\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}`
	jsSpace      = `[` + jsSpaceChars + `]`
)

func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return r >= 0x2000 && r <= 0x200A
}

func jsTrim(s string) string {
	return strings.TrimFunc(s, isJSSpace)
}

// queryParameterLimit is how many parameters of a query string count.
const queryParameterLimit = 1000

// queryString reads one parameter of a query string that must be a single
// plain string, as the console's query parser (qs) saw it. It answers false
// when the parameter is missing, given more than once, or given with
// brackets (name[]=..., name[key]=...), which make a list or an object.
//
// "[name]=x" counts as "name=x". A second spelling of the name does not
// spoil the first when its value is empty: "name=x&[name]=" is "x".
func queryString(query, name string) (string, bool) {
	query = replaceFold(replaceFold(query, "%5B", "["), "%5D", "]")
	parts := strings.Split(query, "&")
	if len(parts) > queryParameterLimit {
		parts = parts[:queryParameterLimit]
	}
	// One entry per key as written; a key given twice is a list.
	type entry struct {
		value string
		once  bool
	}
	entries := map[string]*entry{}
	var order []string
	for _, part := range parts {
		pos := strings.Index(part, "]=")
		if pos >= 0 {
			pos++
		} else {
			pos = strings.IndexByte(part, '=')
		}
		rawKey, rawValue := part, ""
		if pos >= 0 {
			rawKey, rawValue = part[:pos], part[pos+1:]
		}
		key := decodeFormValue(rawKey)
		if seen, ok := entries[key]; ok {
			seen.once = false
			continue
		}
		entries[key] = &entry{value: decodeFormValue(rawValue), once: true}
		order = append(order, key)
	}

	const (
		missing = iota
		plain
		compound
	)
	value, state := "", missing
	for _, key := range order {
		top, nested := topLevelKey(key)
		if key == "" || top != name {
			continue
		}
		e := entries[key]
		isString := e.once && !nested
		switch {
		case state == missing && isString:
			value, state = e.value, plain
		case state == missing:
			state = compound
		case isString && e.value == "":
			// an empty value merges into what is there without a trace
		default:
			state = compound
		}
	}
	return value, state == plain
}

// topLevelKey is the name a query key ends up under: "a" for "a", "a[b]"
// and "[a]", and whether the key goes deeper than that name.
func topLevelKey(key string) (top string, nested bool) {
	first := strings.IndexByte(key, '[')
	switch {
	case first < 0:
		return key, false
	case first > 0:
		return key[:first], true
	}
	level := 0
	for i := 0; i < len(key); i++ {
		switch key[i] {
		case '[':
			level++
		case ']':
			if level--; level == 0 {
				return key[1:i], strings.IndexByte(key[i+1:], '[') >= 0
			}
		}
	}
	// A bracket that never closes: the key is a name as it stands
	return key, false
}

// decodeFormValue decodes a key or a value of a query string: "+" is a
// space, and what does not decode stays as written.
func decodeFormValue(s string) string {
	s = strings.ReplaceAll(s, "+", " ")
	if decoded, ok := decodeURIComponent(s); ok {
		return decoded
	}
	return s
}

// replaceFold replaces every occurrence of an ASCII pattern in any letter
// case.
func replaceFold(s, pattern, with string) string {
	if len(s) < len(pattern) {
		return s
	}
	var b strings.Builder
	start := 0
	for i := 0; i+len(pattern) <= len(s); {
		if asciiEqualFold(s[i:i+len(pattern)], pattern) {
			b.WriteString(s[start:i])
			b.WriteString(with)
			i += len(pattern)
			start = i
			continue
		}
		i++
	}
	if start == 0 {
		return s
	}
	b.WriteString(s[start:])
	return b.String()
}

func asciiEqualFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		x, y := a[i], b[i]
		if x >= 'A' && x <= 'Z' {
			x += 'a' - 'A'
		}
		if y >= 'A' && y <= 'Z' {
			y += 'a' - 'A'
		}
		if x != y {
			return false
		}
	}
	return true
}

// hasPrefixFold reports whether s starts with an ASCII prefix in any
// letter case.
func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && asciiEqualFold(s[:len(prefix)], prefix)
}
