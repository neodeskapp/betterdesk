package ws

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/unitronix/betterdesk-server/console/jsbind"
)

// OriginPolicy decides which pages may open a socket (the old console's
// middleware/wsOrigin.js). A browser sends Origin on every upgrade and
// cookies with it, so a page of another site could otherwise use a
// signed-in person's session.
type OriginPolicy struct {
	// Allowed are origins accepted besides the request's own host
	// (WS_ALLOWED_ORIGINS, comma-separated, compared exactly). Default
	// none.
	Allowed []string
	// AllowMissing accepts an upgrade without Origin even when it carries
	// a session cookie (BD_ALLOW_MISSING_ORIGIN=true). Default false: only
	// a request without a session cookie may leave Origin out.
	AllowMissing bool
}

// ParseAllowedOrigins reads WS_ALLOWED_ORIGINS: split at commas, white
// space around each removed, empty ones dropped.
func ParseAllowedOrigins(value string) []string {
	var out []string
	for _, part := range strings.Split(value, ",") {
		if origin := jsbind.Trim(part); origin != "" {
			out = append(out, origin)
		}
	}
	return out
}

// sessionCookie finds a session cookie anywhere in the Cookie header, as
// the old check did with /bd\.sid=|betterdesk\.sid=/.
var sessionCookie = regexp.MustCompile(`bd\.sid=|betterdesk\.sid=`)

// Allow reports whether an upgrade may go on, and when not, why (for the
// log line).
//
//	no Origin, no session cookie        yes
//	no Origin, a session cookie         only with AllowMissing
//	Origin http://<Host> or https://<Host>   yes
//	Origin in Allowed                   yes
//	any other Origin                    no
func (p OriginPolicy) Allow(r *http.Request) (bool, string) {
	origin := headerText(r, "Origin")
	if origin == "" {
		if !sessionCookie.MatchString(cookieText(r)) || p.AllowMissing {
			return true, ""
		}
		return false, "missing Origin with session cookie present"
	}
	if host := r.Host; host != "" && (origin == "http://"+host || origin == "https://"+host) {
		return true, ""
	}
	for _, allowed := range p.Allowed {
		if origin == allowed {
			return true, ""
		}
	}
	return false, "Origin not in allow-list: " + origin
}

// headerText is a request header as Node's req.headers held it: every
// value of the header joined with ", ".
func headerText(r *http.Request, name string) string {
	return strings.Join(r.Header.Values(name), ", ")
}

// cookieText is req.headers.cookie: the Cookie headers joined with "; ".
func cookieText(r *http.Request) string {
	return strings.Join(r.Header.Values("Cookie"), "; ")
}
