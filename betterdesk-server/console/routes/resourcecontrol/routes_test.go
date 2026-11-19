package resourcecontrol_test

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
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

// fixes are the vectors of vectors.json whose answer a bug fix of
// BUGS.md changed on purpose (proxytest.Replay).
var fixes = slices.Concat(proxytest.Fixes("FL4",
	// A device id with two dots in it is refused with 400 before any call.
	421, 441, 461, 481, 501, 521,
), proxytest.Fixes("FL7",
	// The device list asks the API server for every device, and a failed
	// call is 502 instead of an empty list.
	5, 6, 7, 13, 14, 15, 160, 161, 162, 163, 164, 165, 166, 167, 168, 169, 170,
	171, 172, 173, 174, 175, 176, 177, 178, 179, 180, 181, 182, 183, 184, 185,
	559, 560, 561, 562, 563, 564, 565, 566, 567, 568, 569, 570, 571, 572, 573,
	574, 575, 576, 577, 578, 579, 580, 581, 582, 583, 584, 585, 586, 587, 588,
	589, 590, 591, 592, 593, 594, 595, 596, 598,
), proxytest.Fixes("FL6",
	// The body goes to the API server with every member, also __proto__,
	// constructor and prototype.
	550, 551,
))

// TestVectors replays what routes/resource-control.routes.js of the Node console
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
		"GET /api/bd/resource-policy",
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

// configAccount may change the server configuration.
func configAccount() *proxytest.Account {
	a := &proxytest.Account{UserID: 1}
	a.User.Username, a.User.Role = "e2e_admin", "admin"
	return a
}

// TestDevicesListsMoreThan200: the device list is every device the API
// server has, and total is their number (BUGS.md FL7).
func TestDevicesListsMoreThan200(t *testing.T) {
	w := proxytest.NewWorld(t, register)
	var peers []string
	for i := range 250 {
		peers = append(peers, fmt.Sprintf(`{"id":"d%d","hostname":"h%d","status":"online"}`, i, i))
	}
	all := &proxytest.Answer{Status: 200, Type: "application/json", Body: `{"peers":[` + strings.Join(peers, ",") + `]}`}
	rec, calls := w.Do(proxytest.Request{Path: "/api/panel/resource-control/devices", Session: configAccount()}, all)
	var got struct {
		Devices []struct {
			ID string `json:"id"`
		} `json:"devices"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != 200 {
		t.Fatalf("%d %s: %v", rec.Code, rec.Body, err)
	}
	if len(calls) != 1 || calls[0].URL != "/api/peers" {
		t.Errorf("calls %+v, want one to /api/peers", calls)
	}
	if len(got.Devices) != 250 || got.Total != 250 || got.Devices[249].ID != "d249" {
		t.Errorf("%d devices, total %d", len(got.Devices), got.Total)
	}
}

// TestDevicesWhenTheAPIFails: a failed call is 502, not an empty list
// that looks like an install without devices (BUGS.md FL7).
func TestDevicesWhenTheAPIFails(t *testing.T) {
	w := proxytest.NewWorld(t, register)
	for _, failure := range []*proxytest.Answer{
		{Status: 500, Type: "application/json", Body: `{"error":"database down"}`},
		{Status: 403, Type: "application/json", Body: `{"error":"forbidden"}`},
	} {
		rec, _ := w.Do(proxytest.Request{Path: "/api/panel/resource-control/devices", Session: configAccount()}, failure)
		if rec.Code != 502 || rec.Body.String() != `{"error":"Failed to reach Go server"}` {
			t.Errorf("API answers %+v: %d %s, want 502", failure, rec.Code, rec.Body)
		}
	}
}
