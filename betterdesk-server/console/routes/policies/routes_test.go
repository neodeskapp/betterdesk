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

// TestVectors replays what routes/policies.routes.js of the Node console
// answered and asked the API server (testdata/nodeoracle/run.js).
func TestVectors(t *testing.T) {
	proxytest.Replay(t, filepath.Join("testdata", "vectors.json"), register)
}

func TestRegisteredRoutes(t *testing.T) {
	d := &kit.Deps{Router: kit.NewRouter(), Log: kit.NewLogger("error", true)}
	register(d)
	var got []string
	for _, route := range d.Router.Routes() {
		got = append(got, route.Method+" "+route.Path)
	}
	sort.Strings(got)
	admin := []string{
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
	}
	var want []string
	for _, route := range admin {
		method, pattern, _ := strings.Cut(route, " ")
		want = append(want, route, method+" /api/bd"+pattern)
	}
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("registered routes:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
