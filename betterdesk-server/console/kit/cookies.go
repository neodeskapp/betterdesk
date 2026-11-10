package kit

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/session/cookie"
)

// Cookie is req.cookies[name] for a cookie that holds text: the first
// cookie of that name in the Cookie header, without quotes around the
// value and with percent escapes decoded when they decode. A value
// written as j:<JSON> counts when the JSON is a string. ok is false when
// there is no such cookie or its value is not text.
func Cookie(r *http.Request, name string) (value string, ok bool) {
	raw, found := cookie.Get(r, name)
	if !found {
		return "", false
	}
	if strings.HasPrefix(raw, "j:") {
		var parsed any
		if err := json.Unmarshal([]byte(raw[2:]), &parsed); err == nil && truthy(parsed) {
			text, isText := parsed.(string)
			return text, isText
		}
	}
	return raw, true
}

// truthy is JavaScript's truth of a decoded JSON value.
func truthy(v any) bool {
	switch value := v.(type) {
	case nil:
		return false
	case bool:
		return value
	case float64:
		return value != 0 && value == value
	case string:
		return value != ""
	}
	return true
}

// CookieOptions are the attributes of a cookie the console sets, with
// the names res.cookie took.
type CookieOptions struct {
	// MaxAge is the cookie's lifetime; zero makes a cookie that lasts
	// until the browser closes. It sets both Max-Age and Expires.
	MaxAge time.Duration
	// Path is "/" when empty.
	Path     string
	HTTPOnly bool
	Secure   bool
	// SameSite is "lax", "strict" or "none"; empty leaves it out.
	SameSite string
	// Now is the time Expires is counted from; the zero value means
	// time.Now().
	Now time.Time
}

// SetCookie adds a Set-Cookie header the way res.cookie(name, value,
// options) wrote it: the value percent-encoded and the attributes in the
// order Max-Age, Path, Expires, HttpOnly, Secure, SameSite.
func SetCookie(w http.ResponseWriter, name, value string, o CookieOptions) {
	var b strings.Builder
	b.WriteString(name)
	b.WriteByte('=')
	b.WriteString(httpx.EncodeURIComponent(value))
	if o.MaxAge != 0 {
		b.WriteString("; Max-Age=")
		b.WriteString(strconv.FormatInt(int64(o.MaxAge/time.Second), 10))
	}
	b.WriteString("; Path=")
	if o.Path == "" {
		b.WriteString("/")
	} else {
		b.WriteString(o.Path)
	}
	if o.MaxAge != 0 {
		now := o.Now
		if now.IsZero() {
			now = time.Now()
		}
		b.WriteString("; Expires=")
		b.WriteString(now.Add(o.MaxAge).UTC().Format(http.TimeFormat))
	}
	if o.HTTPOnly {
		b.WriteString("; HttpOnly")
	}
	if o.Secure {
		b.WriteString("; Secure")
	}
	switch strings.ToLower(o.SameSite) {
	case "lax":
		b.WriteString("; SameSite=Lax")
	case "strict":
		b.WriteString("; SameSite=Strict")
	case "none":
		b.WriteString("; SameSite=None")
	}
	w.Header().Add("Set-Cookie", b.String())
}
