package guest

import (
	"slices"

	"github.com/unitronix/betterdesk-server/console/goapipath/proxytest"
)

// fixedVectors are the Node vectors whose answer a bug fix of BUGS.md
// changed on purpose, by bug: the vector's request (vectorKey) and how
// the recording changes into what the console does now.
var fixedVectors = map[string]map[string]func(v *vector){
	// MC3: the desktop route sends the token it checked on as guest, also
	// where the query has an empty or blank guest beside it.
	"MC3": {
		`POST /api/guest/mesh/devices/dev-1/desktop?guest=&t=tok {}`:               sentAs(1, "/api/mesh/devices/dev-1/desktop?guest=tok"),
		`POST /api/guest/mesh/devices/dev-1/desktop?guest=&t=tok {"sent":"on"}`:    sentAs(1, "/api/mesh/devices/dev-1/desktop?guest=tok&view_only=1"),
		`POST /api/guest/mesh/devices/dev-1/desktop?guest=%20tok%20 {}`:            sentAs(1, "/api/mesh/devices/dev-1/desktop?guest=tok"),
		`POST /api/guest/mesh/devices/dev-1/desktop?guest=%20tok%20 {"sent":"on"}`: sentAs(1, "/api/mesh/devices/dev-1/desktop?guest=tok&view_only=1"),
	},
}

// vectorKey names a vector's request: method, path and body, and the
// cookie when it sends one.
func vectorKey(v vector) string {
	key := v.Request.Method + " " + v.Request.Path
	if v.Request.Body != nil {
		key += " " + *v.Request.Body
	}
	if cookie := v.Request.Headers["Cookie"]; cookie != "" {
		key += " cookie " + cookie
	}
	return key
}

// sentAs changes the address of the n-th call to the API server.
func sentAs(n int, url string) func(v *vector) {
	return func(v *vector) {
		v.Calls = slices.Clone(v.Calls)
		v.Calls[n] = proxytest.Call{Method: v.Calls[n].Method, URL: url, Type: v.Calls[n].Type, Body: v.Calls[n].Body}
	}
}
