package bdapi

import (
	"errors"
	"net/http"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/routes/internal/jsreq"
)

// What the handlers read from a request is the value JSON.parse made of
// it (nil, bool, float64, string, []any, *goapi.Object), so the
// conversions below can be those of JavaScript.

// errToPrimitive is the TypeError of String(v) for an object with a member
// named toString, which JSON can only fill with something that is no
// function.
var errToPrimitive = errors.New("Cannot convert object to primitive value")

// text is String(v), and a template literal's ${v}. It fails where
// JavaScript throws.
func text(v any) (string, error) {
	switch t := v.(type) {
	case *goapi.Object:
		if t != nil && t.Has("toString") {
			return "", errToPrimitive
		}
	case []any:
		parts := make([]string, len(t))
		for i, item := range t {
			if item == nil {
				continue
			}
			s, err := text(item)
			if err != nil {
				return "", err
			}
			parts[i] = s
		}
		return strings.Join(parts, ","), nil
	}
	return jsreq.String(v), nil
}

// textOr is String(v || fallback).
func textOr(v any, fallback string) (string, error) {
	if !goapi.Truthy(v) {
		return fallback, nil
	}
	return text(v)
}

// orValue is a || b.
func orValue(a, b any) any {
	if goapi.Truthy(a) {
		return a
	}
	return b
}

// cut is s.substring(0, n) and s.slice(0, n): the first n UTF-16 units. A
// pair cut in half keeps its first half, which reaches the database, the
// API server and every answer as U+FFFD.
func cut(s string, n int) string {
	units := 0
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		width := 1
		if r > 0xFFFF {
			width = 2
		}
		if units+width > n {
			if units < n {
				return s[:i] + string(utf8.RuneError)
			}
			return s[:i]
		}
		units += width
		i += size
	}
	return s
}

// length is s.length: UTF-16 units.
func length(s string) int {
	n := 0
	for _, r := range s {
		n += utf16.RuneLen(r)
	}
	return n
}

// trim is s.trim().
func trim(s string) string { return jsbind.Trim(s) }

// headerText is req.headers[name] as Node read it: the values of a header
// sent more than once joined with ", ", each byte a character (Latin-1).
func headerText(r *http.Request, name string) string {
	return httpx.Latin1(strings.Join(r.Header.Values(name), ", "))
}

// clientIP is getClientIp of the route file: the first entry of
// X-Forwarded-For as the client sent it, else X-Real-IP, else the
// connection's address, else "unknown". The headers are trusted whoever
// sent them (BUGS BD3).
func clientIP(r *http.Request) string {
	if forwarded := headerText(r, "X-Forwarded-For"); forwarded != "" {
		first, _, _ := strings.Cut(forwarded, ",")
		if first = trim(first); first != "" {
			return first
		}
	}
	if real := headerText(r, "X-Real-Ip"); real != "" {
		return real
	}
	if socket := httpx.SocketIP(r); socket != "" {
		return socket
	}
	return "unknown"
}

// bearerToken is extractBearerToken of the route file: the rest of an
// Authorization header that starts with "Bearer ", trimmed; ok is false
// without such a header and for an empty token. The header is read once,
// as Node keeps only the first Authorization header of a request.
func bearerToken(r *http.Request) (token string, ok bool) {
	auth := httpx.Latin1(r.Header.Get("Authorization"))
	rest, found := strings.CutPrefix(auth, "Bearer ")
	if !found {
		return "", false
	}
	token = trim(rest)
	return token, token != ""
}

// nodeBase64 is Buffer.from(text, 'base64'): both alphabets (+/ and -_),
// characters outside them skipped, the first "=" ends the text, two
// characters left at the end give one byte and three give two. Node looks
// at the low byte of each UTF-16 unit only, so a character above U+00FF
// counts as the Latin-1 character with its low byte.
func nodeBase64(s string) []byte {
	out := make([]byte, 0, len(s)*3/4)
	var bits uint32
	n := 0
	for _, unit := range utf16.Encode([]rune(s)) {
		c := byte(unit)
		var v uint32
		switch {
		case c >= 'A' && c <= 'Z':
			v = uint32(c - 'A')
		case c >= 'a' && c <= 'z':
			v = uint32(c-'a') + 26
		case c >= '0' && c <= '9':
			v = uint32(c-'0') + 52
		case c == '+' || c == '-':
			v = 62
		case c == '/' || c == '_':
			v = 63
		case c == '=':
			return finishBase64(out, bits, n)
		default:
			continue
		}
		bits = bits<<6 | v
		n++
		if n == 4 {
			out = append(out, byte(bits>>16), byte(bits>>8), byte(bits))
			bits, n = 0, 0
		}
	}
	return finishBase64(out, bits, n)
}

func finishBase64(out []byte, bits uint32, n int) []byte {
	switch n {
	case 2:
		out = append(out, byte(bits>>4))
	case 3:
		out = append(out, byte(bits>>10), byte(bits>>2))
	}
	return out
}
