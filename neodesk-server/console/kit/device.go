package kit

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/authn"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapipath"
	"github.com/neodeskapp/neodesk/neodesk-server/console/httpx"
	"github.com/neodeskapp/neodesk/neodesk-server/console/middleware"
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
// device a request names is the first of these that names one: the body's
// device_id, the query's device_id, the header X-Device-Id; the empty text
// and null name none. When none does, the header counts if the request has
// it. Only text names a device: a number, true or false, a list or an
// object in the body, and a list or an object in the query
// (device_id[]=a, device_id[x]=a), is refused like another device. A
// request that names no device passes.
//
//	another device, or a value that is no text   403 {"error":"Device ID mismatch"}
func (d *Deps) RequireTokenDeviceMatch(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := matchDevice(Device(r), deviceClaims(r)); err != nil {
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
// (a list or an object when the address has device_id[]= or
// device_id[x]=), and X-Device-Id, whose repeats are joined with ", ".
func deviceClaims(r *http.Request) []authn.DeviceClaim {
	return []authn.DeviceClaim{
		bodyClaim(Body(r).Get("device_id")),
		queryClaim(goapipath.Query(r, "device_id")),
		authn.ClaimFromText(DeviceIDHeader(r)),
	}
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

// bodyClaim is the claim of the body's device_id.
func bodyClaim(v middleware.Value) authn.DeviceClaim {
	if !v.Exists() {
		return authn.DeviceClaim{}
	}
	parsed, err := goapi.Parse(v.Raw())
	if err != nil {
		return authn.ClaimWithoutText()
	}
	return authn.ClaimFromJSON(parsed, true)
}

// queryClaim is the claim of the query's device_id: its text when the
// query gives it once as text, a claim without text for a list or an
// object.
func queryClaim(v goapipath.QueryValue) authn.DeviceClaim {
	if !v.Exists() {
		return authn.DeviceClaim{}
	}
	if text, ok := v.OneText(); ok {
		return authn.ClaimFromText(text, true)
	}
	return authn.ClaimWithoutText()
}
