package bdapi

import (
	"context"
	"net/http"
	"regexp"

	"github.com/unitronix/betterdesk-server/console/authn"
)

// The route file's own checks of a request. They read the Authorization
// header more loosely than the device guards of package kit: it has to
// start with "Bearer ", and the rest is the token after trimming.

var deviceIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{3,64}$`)

// deviceIDPattern is /^[A-Za-z0-9_-]{3,64}$/ (DEVICE_ID_RE).
func deviceIDPattern(id string) bool { return deviceIDRe.MatchString(id) }

type callerKey struct{}

// caller is what the route file's checks put on a request: the token's
// row (req.deviceToken), its account (req.deviceUser, nil when the
// account is gone) and, for a registration, the device it speaks for
// (req.deviceId).
type caller struct {
	token    *authn.AccessToken
	user     *authn.Account
	deviceID string
}

func callerOf(r *http.Request) *caller {
	c, _ := r.Context().Value(callerKey{}).(*caller)
	if c == nil {
		return &caller{}
	}
	return c
}

func withCaller(r *http.Request, c *caller) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), callerKey{}, c))
}

// requireDeviceAuth lets a request through with any live access token,
// an account's or a device's, whoever it belongs to, and notes its use. A session does not count: the
// browser takes a token from POST /api/auth/access-token first. A lookup
// that fails is a refusal.
//
//	401 {"error":"Missing or invalid Bearer access token"}
func (h *handlers) requireDeviceAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token, ok := bearerToken(r); ok {
			row, err := h.d.Authn.LookupAccessToken(r.Context(), token)
			if err == nil && row != nil {
				var user *authn.Account
				user, err = h.d.Authn.UserByID(r.Context(), row.UserID.V)
				if err == nil {
					err = h.d.Authn.TouchAccessToken(r.Context(), token)
				}
				if err == nil {
					next.ServeHTTP(w, withCaller(r, &caller{token: row, user: user}))
					return
				}
			}
			if err != nil {
				h.log.Error("Token auth error:", err)
			}
		}
		present := "[absent]"
		if r.Header.Get("Authorization") != "" {
			present = "[present-invalid]"
		}
		h.log.Warn("Auth FAILED " + r.Method + " " + r.URL.EscapedPath() + " — Bearer=" + present + " ip=" + clientIP(r))
		fail(w, r, http.StatusUnauthorized, "Missing or invalid Bearer access token")
	})
}

// requireOperatorRole lets the accounts with the role admin or operator
// through, behind requireDeviceAuth. A device token is no account's
// sign-in, whichever account it was issued by.
//
//	403 {"error":"Insufficient permissions"}
func requireOperatorRole(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c := callerOf(r); c.token.IsDeviceToken() || !isOperator(c.user) {
			fail(w, r, http.StatusForbidden, "Insufficient permissions")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isOperator(user *authn.Account) bool {
	return user != nil && user.Role.Valid && (user.Role.String == "admin" || user.Role.String == "operator")
}

// identifyDevice names the device of a registration, the one route a
// device may call before it has a token: a live device token bound to a
// well-formed device id, else the header X-Device-Id when it is one. The
// header proves nothing, and an account's token names no device. A token's use is noted; when that or the lookup
// fails the header still counts.
//
//	401 {"error":"Missing device identification"}
func (h *handlers) identifyDevice(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := &caller{}
		if token, ok := bearerToken(r); ok {
			row, err := h.d.Authn.LookupAccessToken(r.Context(), token)
			if err == nil && row != nil {
				if bound := row.ClientID.String; row.IsDeviceToken() && deviceIDPattern(bound) {
					c.deviceID = bound
				}
				c.token = row
				if h.d.Authn.TouchAccessToken(r.Context(), token) == nil && c.deviceID != "" {
					next.ServeHTTP(w, withCaller(r, c))
					return
				}
			}
		}
		if header := headerText(r, "X-Device-Id"); header != "" && deviceIDPattern(header) {
			c.deviceID = header
			next.ServeHTTP(w, withCaller(r, c))
			return
		}
		fail(w, r, http.StatusUnauthorized, "Missing device identification")
	})
}
