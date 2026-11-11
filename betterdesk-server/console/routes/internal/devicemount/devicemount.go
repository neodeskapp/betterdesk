// Package devicemount holds the check that the routes people call in the
// inventory, activity and automation groups put first.
//
// The old console mounted each of these routers twice: at its own prefix
// (/api/inventory, /api/activity, /api/automation) and at /api/bd for its
// device routes. The routes for people then also sat under /api/bd, where a
// route like GET /:id would catch a device path. So they stepped aside
// there, by comparing the mount as the client had written it with
// "/api/bd", letter for letter. Mounts match in any letter case, the
// comparison did not: a request to /API/BD/x is not stepped aside for and
// is answered by the first of these routes that fits.
package devicemount

import (
	"net/http"

	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/kit"
)

// DeviceMount is the mount of the device API.
const DeviceMount = "/api/bd"

// Not passes a request on when the route's group was reached through the
// device mount spelled exactly "/api/bd", and lets the route answer
// otherwise: at its own mount in any spelling, and at the device mount in
// any other spelling (/API/BD, /Api/bd).
func Not(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if SpelledMount(r) == DeviceMount {
			kit.PassOn(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// SpelledMount is the mount of the route answering r as the client wrote
// it (req.baseUrl in the old console): the start of the request path that
// matched the mount, in the request's letter case and without a slash at
// its end. It is "" for a route at the root.
func SpelledMount(r *http.Request) string {
	mount := kit.MountPath(r)
	if mount == "" {
		return ""
	}
	if _, base, ok := httpx.StripMount(httpx.RawPath(r), mount); ok {
		return base
	}
	return mount
}
