package fleet_test

import (
	"path/filepath"
	"slices"
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

// fixes are the vectors of vectors.json whose answer a bug fix of
// BUGS.md changed on purpose (proxytest.Replay).
var fixes = slices.Concat(proxytest.Fixes("FL4",
	// An id with two dots in it is refused with 400 before any call, also
	// the device filter of the task list.
	921, 940, 960, 961, 998, 999, 1035, 1054, 1073, 1092, 1111, 1130, 1149, 1168,
	1187, 1206, 1225, 1244, 1263, 1344,
), proxytest.Fixes("FL2",
	// A filter of the lists that is no single text is refused with 400
	// before any call.
	1306, 1307, 1310, 1311, 1312, 1313, 1316, 1317, 1318, 1319, 1322, 1324, 1326, 1328,
	1342, 1368, 1369, 1370, 1450, 1570, 1571, 1680, 1722, 1723, 1774, 1996, 2030, 2256,
	2316, 2370, 2371, 2632, 2726,
), proxytest.Fixes("FL6",
	// The body goes to the API server with every member, also __proto__,
	// constructor and prototype.
	2796, 2797, 2798, 2799,
))

// TestVectors replays what routes/fleet.routes.js of the Node console
// answered and asked the API server (testdata/vectors.json).
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
		"POST /api/bd/fleet/task-result",
		"POST /api/bd/fleet/software",
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

// TestListFiltersThatAreNoText: a filter of the task and compliance lists
// is taken only as one text; a list or an object is refused with 400
// "Invalid <param>" before any call (BUGS.md FL2).
func TestListFiltersThatAreNoText(t *testing.T) {
	w := proxytest.NewWorld(t, register)
	operator := &proxytest.Account{UserID: 5}
	operator.User.Username, operator.User.Role = "e2e_operator", "operator"
	empty := &proxytest.Answer{Status: 200, Type: "application/json", Body: `{"ok":true,"items":[]}`}
	for target, want := range map[string]string{
		"/api/panel/fleet/tasks?orgId[toString]=x":             "Invalid orgId",
		"/api/panel/fleet/tasks?orgId[]=acme":                  "Invalid orgId",
		"/api/panel/fleet/tasks?orgId=a&orgId=b":               "Invalid orgId",
		"/api/panel/fleet/tasks?status[toString]=x":            "Invalid status",
		"/api/panel/fleet/tasks?status[]=a&status[]=b":         "Invalid status",
		"/api/panel/fleet/tasks?deviceId[toString]=x":          "Invalid deviceId",
		"/api/panel/fleet/tasks?deviceId[x]=d1":                "Invalid deviceId",
		"/api/panel/fleet/tasks?orgId=acme&status[x]=1":        "Invalid status",
		"/api/panel/fleet/compliance?orgId[toString]=x":        "Invalid orgId",
		"/api/panel/fleet/compliance?orgId[]=acme":             "Invalid orgId",
		"/api/bd/api/panel/fleet/compliance?orgId[][]=acme":    "Invalid orgId",
		"/api/bd/api/panel/fleet/tasks?status=x&orgId[x]=acme": "Invalid orgId",
	} {
		rec, calls := w.Do(proxytest.Request{Path: target, Session: operator}, empty)
		if rec.Code != 400 || rec.Body.String() != `{"error":"`+want+`"}` || len(calls) != 0 {
			t.Errorf("GET %s: %d %s after %+v, want 400 %s", target, rec.Code, rec.Body, calls, want)
		}
	}
	for target, sent := range map[string]string{
		"/api/panel/fleet/tasks?orgId=acme&status=done&deviceId=d1": "/api/fleet/tasks?org_id=acme&status=done&device_id=d1",
		"/api/panel/fleet/tasks?orgId=&status=":                     "/api/fleet/tasks",
		"/api/panel/fleet/compliance?orgId=acme&status[]=x":         "/api/fleet/compliance?org_id=acme",
	} {
		rec, calls := w.Do(proxytest.Request{Path: target, Session: operator}, empty)
		if rec.Code != 200 || len(calls) != 1 || calls[0].URL != sent {
			t.Errorf("GET %s: %d after %+v, want a call to %s", target, rec.Code, calls, sent)
		}
	}
}
