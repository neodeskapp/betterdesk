package middleware

import (
	"net/http"
	"strings"
)

// hasBody reports whether a request announces a body: a Content-Length or
// a transfer coding. A POST with neither has none, whatever its type says.
func hasBody(r *http.Request) bool {
	if len(r.TransferEncoding) > 0 || r.ContentLength != 0 {
		return true
	}
	_, ok := r.Header["Content-Length"]
	return ok
}

// headerValue is a request header as one value: the first line for the
// headers that may come once, all lines joined for the others.
func headerValue(h http.Header, name string) string {
	switch name {
	case "Content-Type", "Content-Length":
		return h.Get(name)
	}
	return strings.Join(h.Values(name), ", ")
}

func isTokenByte(c byte) bool {
	switch {
	case c >= '0' && c <= '9', c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		return true
	}
	return strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0
}

// isNameByte is true for the bytes of a media type name. first is the
// first byte of a name, which has to be a letter or digit.
func isNameByte(c byte, first, dot, plus bool) bool {
	switch {
	case c >= '0' && c <= '9', c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		return true
	case first:
		return false
	case c == '.':
		return dot
	case c == '+':
		return plus
	}
	return strings.IndexByte("!#$&^_-", c) >= 0
}

func validName(s string, dot, plus bool) bool {
	if s == "" || len(s) > 127 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isNameByte(s[i], i == 0, dot, plus) {
			return false
		}
	}
	return true
}

// mediaType is a parsed Content-Type.
type mediaType struct {
	// full is type/subtype, with +suffix when there is one, lower case
	full   string
	major  string
	params map[string]string
}

// parseMediaType reads a Content-Type header with the rules of the
// console's body parsers: the type may have spaces around it,
// every parameter has to be well-formed (name=token or name="quoted"),
// and nothing may follow the last one. ok is false for anything else, and
// such a request counts as having no type.
func parseMediaType(header string) (mediaType, bool) {
	if header == "" {
		return mediaType{}, false
	}
	typePart, rest := header, ""
	if i := strings.IndexByte(header, ';'); i >= 0 {
		typePart, rest = header[:i], header[i:]
	}
	typePart = strings.ToLower(strings.Trim(typePart, " "))
	major, minor, found := strings.Cut(typePart, "/")
	if !found || !validName(major, false, false) || !validName(minor, true, true) {
		return mediaType{}, false
	}
	// What follows the last plus sign is the suffix
	subtype, suffix := minor, ""
	if i := strings.LastIndexByte(minor, '+'); i >= 0 {
		subtype, suffix = minor[:i], minor[i+1:]
	}
	if !validName(subtype, true, false) || (suffix != "" && !validName(suffix, false, false)) {
		return mediaType{}, false
	}
	mt := mediaType{full: major + "/" + subtype, major: major, params: map[string]string{}}
	if suffix != "" {
		mt.full += "+" + suffix
	}

	// *( ";" *SP name *SP "=" *SP value *SP )
	i := 0
	for i < len(rest) {
		if rest[i] != ';' {
			return mediaType{}, false
		}
		i++
		for i < len(rest) && rest[i] == ' ' {
			i++
		}
		start := i
		for i < len(rest) && isTokenByte(rest[i]) {
			i++
		}
		name := strings.ToLower(rest[start:i])
		if name == "" {
			return mediaType{}, false
		}
		for i < len(rest) && rest[i] == ' ' {
			i++
		}
		if i >= len(rest) || rest[i] != '=' {
			return mediaType{}, false
		}
		i++
		for i < len(rest) && rest[i] == ' ' {
			i++
		}
		var value string
		if i < len(rest) && rest[i] == '"' {
			i++
			var b strings.Builder
			closed := false
			for i < len(rest) {
				c := rest[i]
				if c == '"' {
					closed = true
					i++
					break
				}
				if c == '\\' {
					if i+1 >= len(rest) || rest[i+1] < 0x20 || rest[i+1] > 0x7e {
						return mediaType{}, false
					}
					b.WriteByte(rest[i+1])
					i += 2
					continue
				}
				if c != ' ' && c != '!' && !(c >= 0x23 && c <= 0x5b) && !(c >= 0x5d && c <= 0x7e) && c < 0x80 {
					return mediaType{}, false
				}
				b.WriteByte(c)
				i++
			}
			if !closed {
				return mediaType{}, false
			}
			value = b.String()
		} else {
			start = i
			for i < len(rest) && isTokenByte(rest[i]) {
				i++
			}
			if i == start {
				return mediaType{}, false
			}
			value = rest[start:i]
		}
		for i < len(rest) && rest[i] == ' ' {
			i++
		}
		mt.params[name] = value
	}
	return mt, true
}

// requestMediaType is the media type of a request that has a body.
func requestMediaType(r *http.Request) (mediaType, bool) {
	if !hasBody(r) {
		return mediaType{}, false
	}
	return parseMediaType(headerValue(r.Header, "Content-Type"))
}

// IsJSON reports whether the request carries a body of type
// application/json. Parameters are allowed, application/vnd.api+json and
// other +json types are not JSON here.
func IsJSON(r *http.Request) bool {
	mt, ok := requestMediaType(r)
	return ok && mt.full == "application/json"
}

// latin1 turns header bytes into text, one letter per byte.
func latin1(s string) string {
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			ascii = false
			break
		}
	}
	if ascii {
		return s
	}
	runes := make([]rune, len(s))
	for i := 0; i < len(s); i++ {
		runes[i] = rune(s[i])
	}
	return string(runes)
}
