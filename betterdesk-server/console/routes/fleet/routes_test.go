package fleet_test

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/goapipath/proxytest"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/routes/fleet"
)

// register mounts the group the way console/routes.go does.
func register(d *kit.Deps) {
	fleet.Register(d)
	fleet.Register(d.At("/api/bd"))
}

// TestVectors replays what routes/fleet.routes.js of the Node console
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
		"GET /api/panel/fleet/availability",
		"GET /api/panel/fleet/resources/{orgId}",
		"POST /api/panel/fleet/resources/{orgId}",
		"PUT /api/panel/fleet/resources/{orgId}/{resourceId}",
		"DELETE /api/panel/fleet/resources/{orgId}/{resourceId}",
		"GET /api/panel/fleet/tasks",
		"GET /api/panel/fleet/tasks/{taskId}",
		"POST /api/panel/fleet/tasks",
		"PUT /api/panel/fleet/tasks/{taskId}",
		"DELETE /api/panel/fleet/tasks/{taskId}",
		"GET /api/panel/fleet/tasks/{taskId}/output",
		"POST /api/panel/fleet/tasks/{taskId}/retry",
		"GET /api/panel/fleet/workflows",
		"GET /api/panel/fleet/workflows/{workflowId}",
		"POST /api/panel/fleet/workflows",
		"PUT /api/panel/fleet/workflows/{workflowId}",
		"DELETE /api/panel/fleet/workflows/{workflowId}",
		"POST /api/panel/fleet/workflows/{workflowId}/execute",
		"GET /api/panel/fleet/workflows/{workflowId}/history",
		"GET /api/panel/fleet/compliance",
		"GET /api/panel/fleet/compliance/{deviceId}",
		"POST /api/panel/fleet/compliance/{deviceId}/scan",
		"POST /api/panel/fleet/compliance/{deviceId}/remediate",
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
