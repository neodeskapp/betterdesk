package registration

import (
	"errors"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/routes/internal/jsjson"
	"github.com/unitronix/betterdesk-server/console/routes/internal/jsreq"
)

// What the handlers read from a request is the value JSON.parse made of
// it (nil, bool, float64, string, []any, *goapi.Object), so the
// conversions below can be those of JavaScript.

// errToPrimitive is the TypeError of String(v) for an object with a member
// named toString, which JSON can only fill with something that is no
// function.
var errToPrimitive = errors.New("Cannot convert object to primitive value")

// errNotText is the TypeError of calling a text method (trim, substring)
// on a value that is no text.
var errNotText = errors.New("value is not a string")

// text is String(v), and a template literal's ${v}. It fails where
// JavaScript throws.
func text(v any) (string, error) {
	switch t := v.(type) {
	case *goapi.Object:
		if t != nil && t.Has("toString") {
			return "", errToPrimitive
		}
	case []any:
		return join(t, ",")
	}
	return jsreq.String(v), nil
}

// join is list.join(sep): null elements are empty, the others their text.
func join(list []any, sep string) (string, error) {
	parts := make([]string, len(list))
	for i, item := range list {
		if item == nil {
			continue
		}
		s, err := text(item)
		if err != nil {
			return "", err
		}
		parts[i] = s
	}
	return strings.Join(parts, sep), nil
}

// textOr is (v || fallback) for a value a route calls a text method on:
// the fallback when v is not truthy, v when it is text, and the
// TypeError otherwise.
func textOr(v any, fallback string) (string, error) {
	if !goapi.Truthy(v) {
		return fallback, nil
	}
	s, ok := v.(string)
	if !ok {
		return "", errNotText
	}
	return s, nil
}

// cut is s.substring(0, n): the first n UTF-16 units. A pair cut in half
// keeps its first half, which reaches the database and every answer as
// U+FFFD.
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

// length is s.length.
func length(s string) int { return jsjson.Length(s) }

// orValue is a || b.
func orValue(a, b any) any {
	if goapi.Truthy(a) {
		return a
	}
	return b
}

// countOf is Number(v) || 0 for a count of the API server: a number as it
// is, a flag as 0 or 1, text and a list as the number their text reads,
// anything that is no number 0.
func countOf(v any) float64 {
	n := math.NaN()
	switch t := v.(type) {
	case nil:
		n = 0
	case bool:
		if n = 0; t {
			n = 1
		}
	case float64:
		n = t
	case string:
		n = jsbind.ParseNumber(t)
	case []any:
		if s, err := text(t); err == nil {
			n = jsbind.ParseNumber(s)
		}
	}
	if math.IsNaN(n) {
		return 0
	}
	return n
}
