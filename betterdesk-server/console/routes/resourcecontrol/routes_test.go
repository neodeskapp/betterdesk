package resourcecontrol_test

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/goapipath/proxytest"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/routes/resourcecontrol"
)

// register mounts the group the way console/routes.go does.
func register(d *kit.Deps) {
	resourcecontrol.Register(d)
	resourcecontrol.Register(d.At("/api/bd"))
}

// TestVectors replays what routes/resource-control.routes.js of the Node console
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
		"GET /api/panel/resource-control/devices",
		"GET /api/panel/resource-control/policies/{deviceId}",
		"POST /api/panel/resource-control/policies/{deviceId}/usb",
		"POST /api/panel/resource-control/policies/{deviceId}/optical",
		"POST /api/panel/resource-control/policies/{deviceId}/monitors",
		"POST /api/panel/resource-control/policies/{deviceId}/disks",
		"POST /api/panel/resource-control/policies/{deviceId}/quotas",
		"GET /api/panel/resource-control/compliance",
		"GET /api/panel/resource-control/org-policy",
		"PUT /api/panel/resource-control/org-policy",
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
