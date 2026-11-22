// Package cookie reads the Cookie header and writes Set-Cookie headers the
// way the console always has, so a browser and the Node console see the
// same bytes from Go.
//
// Reading: the header is a list of name=value pairs separated by ";". Spaces
// and tabs around a name and a value are dropped, one pair of double quotes
// around a value is removed, and a value that contains "%" is percent-decoded
// (when that fails the value is taken as it is). When a name appears twice
// the first one counts.
//
// Writing: the value is percent-encoded like JavaScript's
// encodeURIComponent, and the attributes come in a fixed order and
// spelling: Max-Age, Path, Expires, HttpOnly, Secure, SameSite.
package cookie

import (
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/unitronix/betterdesk-server/console/httpx"
)

// Get returns the value of the request's cookie called name, decoded. ok is
// false when the request has no such cookie.
func Get(r *http.Request, name string) (value string, ok bool) {
	return Lookup(strings.Join(r.Header.Values("Cookie"), "; "), name)
}

// Lookup is Get for the text of a Cookie header.
func Lookup(header, name string) (value string, ok bool) {
	// A byte above 0x7F counts as one Latin-1 character
	str := httpx.Latin1(header)
	if len(str) < 2 {
		return "", false
	}
	index := 0
	for index < len(str) {
		eq := indexFrom(str, '=', index)
		if eq < 0 {
			break
		}
		end := indexFrom(str, ';', index)
		if end < 0 {
			end = len(str)
		} else if eq > end {
			// A part without "=": go on after the last ";" before the "="
			index = strings.LastIndexByte(str[:eq], ';') + 1
			continue
		}
		if trimBlank(str[index:eq]) == name {
			val := trimBlank(str[eq+1 : end])
			if len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"' {
				val = val[1 : len(val)-1]
			} else if val == `"` {
				val = ""
			}
			return decode(val), true
		}
		index = end + 1
	}
	return "", false
}

func indexFrom(s string, c byte, from int) int {
	i := strings.IndexByte(s[from:], c)
	if i < 0 {
		return -1
	}
	return from + i
}

func trimBlank(s string) string {
	return strings.Trim(s, " \t")
}

// decode percent-decodes a value that contains "%". A value that is not
// valid percent-encoding is returned unchanged.
func decode(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	if out, ok := decodeURIComponent(s); ok {
		return out
	}
	return s
}

// decodeURIComponent works like the JavaScript function of that name: each
// %XX run must spell out one whole UTF-8 character.
func decodeURIComponent(s string) (string, bool) {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] != '%' {
			b.WriteByte(s[i])
			i++
			continue
		}
		first, ok := hexByte(s, i)
		if !ok {
			return "", false
		}
		i += 3
		if first < utf8.RuneSelf {
			b.WriteByte(first)
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
		seq := [4]byte{first}
		for k := 1; k < size; k++ {
			next, ok := hexByte(s, i)
			if !ok || next&0xC0 != 0x80 {
				return "", false
			}
			seq[k] = next
			i += 3
		}
		if !utf8.Valid(seq[:size]) {
			return "", false
		}
		b.Write(seq[:size])
	}
	return b.String(), true
}

// hexByte reads the %XX at s[i:].
func hexByte(s string, i int) (byte, bool) {
	if i+2 >= len(s) || s[i] != '%' {
		return 0, false
	}
	hi, ok1 := unhex(s[i+1])
	lo, ok2 := unhex(s[i+2])
	return hi<<4 | lo, ok1 && ok2
}

func unhex(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// Attributes are the attributes of a cookie the console sets.
type Attributes struct {
	// MaxAge is the cookie's lifetime, written as Max-Age in whole seconds
	// (rounded down). It is left out when zero and does not set Expires.
	MaxAge time.Duration
	// Path is left out when empty.
	Path string
	// Expires is left out when zero.
	Expires time.Time
	// HTTPOnly and Secure add the flags.
	HTTPOnly bool
	Secure   bool
	// SameSite is "lax", "strict" or "none" in any letter case; anything
	// else leaves the attribute out.
	SameSite string
}

// Format returns the text of a Set-Cookie header.
func Format(name, value string, a Attributes) string {
	var b strings.Builder
	b.WriteString(name)
	b.WriteByte('=')
	b.WriteString(httpx.EncodeURIComponent(value))
	if a.MaxAge != 0 {
		seconds := int64(a.MaxAge / time.Second)
		if a.MaxAge < 0 && a.MaxAge%time.Second != 0 {
			seconds--
		}
		b.WriteString("; Max-Age=")
		b.WriteString(strconv.FormatInt(seconds, 10))
	}
	if a.Path != "" {
		b.WriteString("; Path=")
		b.WriteString(a.Path)
	}
	if !a.Expires.IsZero() {
		b.WriteString("; Expires=")
		b.WriteString(a.Expires.UTC().Format(http.TimeFormat))
	}
	if a.HTTPOnly {
		b.WriteString("; HttpOnly")
	}
	if a.Secure {
		b.WriteString("; Secure")
	}
	switch strings.ToLower(a.SameSite) {
	case "lax":
		b.WriteString("; SameSite=Lax")
	case "strict":
		b.WriteString("; SameSite=Strict")
	case "none":
		b.WriteString("; SameSite=None")
	}
	return b.String()
}

// Set adds a Set-Cookie header to the answer.
func Set(w http.ResponseWriter, name, value string, a Attributes) {
	w.Header().Add("Set-Cookie", Format(name, value, a))
}

// Clear adds a Set-Cookie header that makes the browser forget the cookie:
// an empty value that expired in 1970. A browser only matches it to the
// cookie when the attributes in a are the ones the cookie was set with.
func Clear(w http.ResponseWriter, name string, a Attributes) {
	a.Expires = time.Unix(0, 0)
	if a.Path == "" {
		a.Path = "/"
	}
	Set(w, name, "", a)
}
