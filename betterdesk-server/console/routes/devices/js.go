package devices

import (
	"errors"
	"math"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/routes/internal/jsreq"
)

// What the handlers read from a request is the value JSON.parse made of
// it (nil, bool, float64, string, []any, *goapi.Object), so the
// conversions below can be those of JavaScript.

// field is req.body[name]: the member's value and whether the body has it.
// A body that is a list has no members.
func field(r *http.Request, name string) (any, bool) {
	v := kit.Body(r).Get(name)
	if !v.Exists() {
		return nil, false
	}
	parsed, err := goapi.Parse(v.Raw())
	if err != nil {
		return nil, true
	}
	return parsed, true
}

// value is req.body[name], nil when the body does not have it.
func value(r *http.Request, name string) any {
	v, _ := field(r, name)
	return v
}

// plainBody is plainBodyObject(req.body): the body when it is an object,
// else an empty one.
func plainBody(r *http.Request) *goapi.Object {
	if o, ok := jsreq.Body(r).(*goapi.Object); ok && o != nil {
		return o
	}
	return goapi.NewObject()
}

// errToPrimitive is the TypeError of String(v) for an object with a
// member named toString, which JSON can only fill with something that is
// no function.
var errToPrimitive = errors.New("Cannot convert object to primitive value")

// text is String(v). It fails where JavaScript throws.
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

// jsTrim is String.prototype.trim.
func jsTrim(s string) string { return strings.TrimFunc(s, isJSSpace) }

func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// slice is s.slice(0, n): the first n UTF-16 code units. A cut through a
// character outside the Basic Multilingual Plane leaves its first half,
// which becomes U+FFFD.
func slice(s string, n int) string {
	units := 0
	for i, r := range s {
		size := 1
		if r > 0xffff {
			size = 2
		}
		if units+size > n {
			if units < n {
				return s[:i] + "�"
			}
			return s[:i]
		}
		units += size
	}
	return s
}

// length is s.length in UTF-16 code units.
func length(s string) int { return jsreq.Length(s) }

// idText is String(device.id) of a device as the device layer lists it.
func idText(device *goapi.Object) string {
	if device == nil {
		return ""
	}
	return jsreq.String(device.Get("id"))
}

// clientAPIRequest passes a request with a bearer token on: the RustDesk
// client API answers it (fallthroughUnlessBearer of
// routes/rustdesk-api.routes.js).
func clientAPIRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hasBearerToken(r.Header.Get("Authorization")) {
			kit.PassOn(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// hasBearerToken is the client API's test of the header: it starts with
// "Bearer " and a token is left after JavaScript trimmed the rest, which
// Node read as Latin-1, so the white space is the ASCII one and U+00A0.
func hasBearerToken(header string) bool {
	rest, ok := strings.CutPrefix(header, "Bearer ")
	if !ok {
		return false
	}
	for i := 0; i < len(rest); i++ {
		if c := rest[i]; c != ' ' && (c < '\t' || c > '\r') && c != 0xa0 {
			return true
		}
	}
	return false
}

// storedClock is a time as the database or the API server writes it
// without a zone: read as UTC.
var storedClock = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}(:\d{2}(\.\d+)?)?$`)

// parseStoredTime is the time of a stored timestamp in milliseconds, NaN
// when there is none. Text without a zone is UTC; other text is read in
// the ISO forms Date.parse takes.
func parseStoredTime(v any) float64 {
	if !goapi.Truthy(v) {
		return math.NaN()
	}
	if t, ok := v.(interface{ Instant() (time.Time, bool) }); ok {
		instant, ok := t.Instant()
		if !ok {
			return math.NaN()
		}
		return float64(instant.UnixMilli())
	}
	s := jsTrim(jsreq.String(v))
	if storedClock.MatchString(s) {
		s = strings.Replace(s, " ", "T", 1) + "Z"
	}
	return isoMillis(s)
}

// isoMillis is Date.parse for the ISO forms: a date, or a date and time
// with or without seconds and fraction and with a zone (Z or ±hh:mm); a
// date and time without a zone is local time.
func isoMillis(s string) float64 {
	layouts := []string{
		"2006-01-02T15:04:05.999999999Z07:00",
		"2006-01-02T15:04Z07:00",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return float64(t.UnixMilli())
		}
	}
	if t, err := time.ParseInLocation("2006-01-02", s, time.UTC); err == nil {
		return float64(t.UnixMilli())
	}
	for _, layout := range []string{"2006-01-02T15:04:05.999999999", "2006-01-02T15:04"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return float64(t.UnixMilli())
		}
	}
	return math.NaN()
}

// auditEntryMatchesBan reports whether a console audit entry belongs to
// the current ban: both were written within five minutes, or the ban
// time is not known.
func auditEntryMatchesBan(auditTime, bannedAt any) bool {
	b := parseStoredTime(bannedAt)
	if math.IsNaN(b) {
		return true
	}
	a := parseStoredTime(auditTime)
	return !math.IsNaN(a) && math.Abs(a-b) <= 5*60*1000
}

// spread is {...v} for a value of the API server.
func spread(v any) *goapi.Object {
	switch t := v.(type) {
	case *goapi.Object:
		if t != nil {
			return t.Clone()
		}
	case []any:
		o := goapi.NewObject()
		for i, item := range t {
			o.Set(jsreq.String(float64(i)), item)
		}
		return o
	case string:
		o := goapi.NewObject()
		i := 0
		for _, unit := range utf16Units(t) {
			o.Set(jsreq.String(float64(i)), unit)
			i++
		}
		return o
	}
	return goapi.NewObject()
}

// utf16Units splits text into what JavaScript indexes: characters, and
// the two halves of one outside the Basic Multilingual Plane, each as
// U+FFFD.
func utf16Units(s string) []string {
	var out []string
	for _, r := range s {
		if r > 0xffff {
			out = append(out, "�", "�")
			continue
		}
		out = append(out, string(r))
	}
	return out
}

// dig is a?.b?.c for values of the API server.
func dig(v any, path ...string) any {
	for _, name := range path {
		o, ok := v.(*goapi.Object)
		if !ok || o == nil {
			return nil
		}
		v = o.Get(name)
	}
	return v
}

// or is a || b.
func or(a, b any) any {
	if goapi.Truthy(a) {
		return a
	}
	return b
}
