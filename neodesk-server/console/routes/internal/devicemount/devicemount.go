// Package devicemount holds the check that the routes people call in the
// inventory, activity and automation groups put first.
//
// The old console mounted each of these routers twice: at its own prefix
// (/api/inventory, /api/activity, /api/automation) and at /api/nd for its
// device routes. The routes for people then also sat under /api/nd, where a
// route like GET /:id would catch a device path. So they step aside there,
// in any letter case of the mount, as the mount itself matches in any
// letter case.
package devicemount

import (
	"net/http"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/httpx"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
)

// DeviceMount is the mount of the device API.
const DeviceMount = "/api/nd"

// Not passes a request on when the route's group was reached through the
// device mount, spelled in any letter case (/api/nd, /API/ND, /Api/nd), and
// lets the route answer at the group's own mount.
func Not(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.EqualFold(SpelledMount(r), DeviceMount) {
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
