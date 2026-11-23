package kit

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/session/cookie"
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
	a := cookie.Attributes{MaxAge: o.MaxAge, Path: o.Path, HTTPOnly: o.HTTPOnly, Secure: o.Secure, SameSite: o.SameSite}
	if a.Path == "" {
		a.Path = "/"
	}
	if o.MaxAge != 0 {
		now := o.Now
		if now.IsZero() {
			now = time.Now()
		}
		a.Expires = now.Add(o.MaxAge)
	}
	cookie.Set(w, name, value, a)
}
