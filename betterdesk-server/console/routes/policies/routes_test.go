package policies_test

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/goapipath/proxytest"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/routes/policies"
)

// register mounts the group the way console/routes.go does.
func register(d *kit.Deps) {
	policies.Register(d)
	policies.Register(d.At("/api/bd"))
}

// fixes are the vectors of vectors.json whose answer a bug fix of
// BUGS.md changed on purpose (proxytest.Replay).
var fixes = proxytest.Fixes("FL4",
	// An id with two dots in it is refused by the route's own check, before
	// any call.
	422, 423, 466, 467, 510, 511, 554, 555, 598, 599, 642, 643, 688, 689, 690, 691,
	774, 775, 818, 819,
)

// TestVectors replays what routes/policies.routes.js of the Node console
// answered and asked the API server (testdata/nodeoracle/run.js).
func TestVectors(t *testing.T) {
	proxytest.Replay(t, filepath.Join("testdata", "vectors.json"), register, fixes...)
}

func TestRegisteredRoutes(t *testing.T) {
	d := &kit.Deps{Router: kit.NewRouter(), Log: kit.NewLogger("error", true)}
	register(d)
	var got []string
	for _, route := range d.Router.Routes() {
		got = append(got, route.Method+" "+route.Path)
	}
	sort.Strings(got)
	routes := []string{
		"GET /api/panel/policies/{orgId}",
		"PUT /api/panel/policies/{orgId}/connection",
		"PUT /api/panel/policies/{orgId}/features",
		"PUT /api/panel/policies/{orgId}/security",
		"PUT /api/panel/policies/{orgId}/network",
		"PUT /api/panel/policies/{orgId}/update",
		"GET /api/panel/policies/{orgId}/effective/{deviceId}",
		"GET /api/panel/policies/{orgId}/audit",
		"GET /api/panel/attestation",
		"GET /api/panel/attestation/{deviceId}",
		"GET /api/bd/device-policy",
		"POST /api/bd/attestation",
	}
	var want []string
	for _, route := range routes {
		method, pattern, _ := strings.Cut(route, " ")
		want = append(want, route, method+" /api/bd"+pattern)
	}
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("registered routes:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
