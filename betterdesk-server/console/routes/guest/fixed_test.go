package guest

import (
	"slices"

	"github.com/unitronix/betterdesk-server/console/goapipath/proxytest"
)

// The links the API server's stand-in answers with in the vectors below.
const (
	linkG1   = `{"valid":true,"id":"g1","peer_ids":["dev-1","dev-2"],"view_only":false,"expires_at":"2099-01-01T00:00:00Z","label":"L","devices":[{"id":"dev-1"}]}`
	linkG2   = `{"valid":true,"id":"g2","peer_ids":["dev-1"],"view_only":true,"expires_at":"2099-01-01T00:00:00Z"}`
	linkText = `{"valid":true,"peer_ids":"xx dev-1 yy","view_only":"no"}`
)

// fixedVectors are the Node vectors whose answer a bug fix of BUGS.md
// changed on purpose, by bug: the vector (vectorKey) and how its
// recording changes into what the console does now.
var fixedVectors = map[string]map[string]func(v *vector){
	// MC3: the desktop route sends the token it checked on as guest, also
	// where the query has an empty or blank guest beside it.
	"MC3": {
		`POST /api/guest/mesh/devices/dev-1/desktop?guest=&t=tok {} -> ` + linkG1:               sentAs(1, "/api/mesh/devices/dev-1/desktop?guest=tok"),
		`POST /api/guest/mesh/devices/dev-1/desktop?guest=&t=tok {"sent":"on"} -> ` + linkG2:    sentAs(1, "/api/mesh/devices/dev-1/desktop?guest=tok&view_only=1"),
		`POST /api/guest/mesh/devices/dev-1/desktop?guest=%20tok%20 {} -> ` + linkG1:            sentAs(1, "/api/mesh/devices/dev-1/desktop?guest=tok"),
		`POST /api/guest/mesh/devices/dev-1/desktop?guest=%20tok%20 {"sent":"on"} -> ` + linkG2: sentAs(1, "/api/mesh/devices/dev-1/desktop?guest=tok&view_only=1"),
	},
	// GA2: a link whose list of devices is a text reaches no device.
	"GA2": {
		`POST /api/guest/mesh/devices/dev-1/desktop?guest=tok {} -> ` + linkText: refused(403, `{"error":"Invalid or expired guest link"}`),
	},
}

// vectorKey names a vector: the request's method, path and body, its
// cookie when it sends one, and what the API server's stand-in answers.
func vectorKey(v vector) string {
	key := v.Request.Method + " " + v.Request.Path
	if v.Request.Body != nil {
		key += " " + *v.Request.Body
	}
	if cookie := v.Request.Headers["Cookie"]; cookie != "" {
		key += " cookie " + cookie
	}
	if v.API != nil {
		key += " -> " + v.API.Body
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

// refused makes the answer a refusal with the status and JSON body, after
// the first call to the API server, which checks the link.
func refused(status int, body string) func(v *vector) {
	return func(v *vector) {
		v.Status, v.Type, v.Body, v.Disposition = status, "application/json; charset=utf-8", body, ""
		v.Calls = v.Calls[:1:1]
	}
}
