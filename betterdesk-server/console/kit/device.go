package kit

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/unitronix/betterdesk-server/console/authn"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/goapipath"
	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/middleware"
)

// The guards of the routes devices call (agents, the desktop client) are
// the middleware of middleware/deviceAuth.js under their old names. A
// device proves itself with an access token bound to its id; the header
// X-Device-Id names a device and proves nothing. The checks themselves are
// in package authn.

type deviceKey struct{}

// Device is the device whose token RequireDeviceToken accepted: DeviceID
// is req.deviceId, Token the token's row (req.deviceToken). It is never
// nil behind RequireDeviceToken, and nil on a request that did not pass
// it.
func Device(r *http.Request) *authn.DeviceAuth {
	device, _ := r.Context().Value(deviceKey{}).(*authn.DeviceAuth)
	return device
}

// deviceRefusal is the answer of a refused device request: {"error":"..."},
// without success.
type deviceRefusal struct {
	Error string `json:"error"`
}

// RequireDeviceToken lets a request through whose Authorization header is
// "Bearer <token>" with a live access token bound to a device id, notes
// the use on the token's row, and puts the device into the request
// (Device). A failure to note the use changes nothing.
//
//	no Bearer token          401 {"error":"Bearer access token required"}
//	the lookup failed        401 {"error":"Invalid or expired access token"}
//	no live token, or one    401 {"error":"Invalid or unbound access token"}
//	bound to no device id
//
// It answers JSON on every path and does not look at the session: a
// signed-in account without a device token is refused too.
func (d *Deps) RequireDeviceToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// authn refuses with a *DeviceAuthError, and with nothing else
		device, err := d.Authn.RequireDeviceToken(r.Context(), httpx.Latin1(r.Header.Get("Authorization")))
		var refused *authn.DeviceAuthError
		if errors.As(err, &refused) {
			httpx.WriteJSON(w, r, refused.Status, deviceRefusal{Error: refused.Text})
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), deviceKey{}, device)))
	})
}

// RequireTokenDeviceMatch refuses a request that names another device than
// the one its token is bound to. It goes behind RequireDeviceToken. The
// device a request names is the first of these that is not empty for
// JavaScript (not "", 0, false or null): the body's device_id, the
// query's device_id, the header X-Device-Id; when none is, the header
// counts if the request has it. Values that are no text are compared as
// String() made them: 123 as "123", ["a"] as "a". A request that names no
// device passes.
//
//	another device                          403 {"error":"Device ID mismatch"}
//	a value String() cannot make text of    the console's server error answer
//	(an object with a member toString)
func (d *Deps) RequireTokenDeviceMatch(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, err := deviceClaims(r)
		if err != nil {
			d.serverError(w, r, err)
			return
		}
		if err := matchDevice(Device(r), claims); err != nil {
			refused := authn.ErrDeviceMismatch
			httpx.WriteJSON(w, r, refused.Status, deviceRefusal{Error: refused.Text})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// matchDevice checks the claims against the device of the request.
// Without a device (RequireDeviceToken did not run) the device id was
// undefined, which no claim equals: only a request that names no device
// passes. The claim that counts cannot equal two different ids, so asking
// for a second one tells a request without a claim from one whose claim is
// the empty text.
func matchDevice(device *authn.DeviceAuth, claims []authn.DeviceClaim) error {
	if device != nil {
		return authn.RequireTokenDeviceMatch(device.DeviceID, claims...)
	}
	if err := authn.RequireTokenDeviceMatch("", claims...); err != nil {
		return err
	}
	return authn.RequireTokenDeviceMatch("-", claims...)
}

// deviceClaims reads the three places where a request names its device,
// in the order of the check: the body's device_id, the query's device_id
// (req.query as Express parsed it, so a list or an object when the
// address has device_id[]= or device_id[x]=), and X-Device-Id, whose
// repeats are joined with ", " as Node joined them. It fails where
// String() threw on the claim that counts.
func deviceClaims(r *http.Request) ([]authn.DeviceClaim, error) {
	body, bodyErr := bodyClaim(Body(r).Get("device_id"))
	query, queryErr := queryClaim(goapipath.Query(r, "device_id"))
	header := authn.ClaimFromText(DeviceIDHeader(r))

	// String() runs on the first claim that is true; a list or an object
	// always is.
	switch {
	case body.Truthy && bodyErr != nil:
		return nil, bodyErr
	case !body.Truthy && query.Truthy && queryErr != nil:
		return nil, queryErr
	}
	return []authn.DeviceClaim{body, query, header}, nil
}

// DeviceIDHeader is req.headers['x-device-id']: the text of the header
// X-Device-Id, each byte one character, with its repeats joined by ", ".
// ok is false when the request has no such header.
func DeviceIDHeader(r *http.Request) (value string, ok bool) {
	values := r.Header.Values("X-Device-Id")
	if len(values) == 0 {
		return "", false
	}
	texts := make([]string, len(values))
	for i, v := range values {
		texts[i] = httpx.Latin1(v)
	}
	return strings.Join(texts, ", "), true
}

// bodyClaim is the claim of the body's device_id. err is set when String()
// cannot make text of the value.
func bodyClaim(v middleware.Value) (authn.DeviceClaim, error) {
	if !v.Exists() {
		return authn.DeviceClaim{}, nil
	}
	parsed, err := goapi.Parse(v.Raw())
	if err != nil {
		return authn.DeviceClaim{}, fmt.Errorf("read the device_id of the body: %w", err)
	}
	claim := authn.ClaimFromJSON(parsed, true)
	if withoutText(parsed) {
		return claim, goapipath.ErrNotPrimitive
	}
	return claim, nil
}

// withoutText reports whether String() throws on a parsed JSON value: on
// an object with its own member toString, which JSON can only make a value
// that cannot be called, and on a list that holds such an object at any
// depth, since a list joins the texts of its elements.
func withoutText(value any) bool {
	switch v := value.(type) {
	case *goapi.Object:
		return v.Has("toString")
	case []any:
		for _, element := range v {
			if withoutText(element) {
				return true
			}
		}
	}
	return false
}

// queryClaim is the claim of the query's device_id. err is set when
// String() cannot make text of the value.
func queryClaim(v goapipath.QueryValue) (authn.DeviceClaim, error) {
	if !v.Exists() {
		return authn.DeviceClaim{}, nil
	}
	text, err := v.Text()
	return authn.DeviceClaim{Present: true, Truthy: v.Truthy(), Text: text}, err
}

// serverError answers what the console's error handler answered for an
// error a middleware threw.
func (d *Deps) serverError(w http.ResponseWriter, r *http.Request, err error) {
	if d.WebUI != nil {
		d.WebUI.ServerError(w, r, err, http.StatusInternalServerError)
		return
	}
	httpx.WriteJSON(w, r, http.StatusInternalServerError, failure{Error: "Internal Server Error"})
}
