package crossplatform_test

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/routes/internal/routetest"
)

// scenario is one entry of testdata/crossplatform.json: an answer of the
// API server and what the Node route made of it
// (testdata/nodeoracle/crossplatform.js).
type scenario struct {
	Name     string `json:"name"`
	Route    string `json:"route"`
	DeviceID string `json:"deviceId"`
	API      struct {
		Status int    `json:"status"`
		Body   string `json:"body"`
		Fail   string `json:"fail"`
	} `json:"api"`
	Answer struct {
		Status int    `json:"status"`
		Text   string `json:"text"`
	} `json:"answer"`
	Calls []string `json:"calls"`
}

func loadScenarios(t *testing.T) []scenario {
	t.Helper()
	raw, err := os.ReadFile("testdata/crossplatform.json")
	if err != nil {
		t.Fatal(err)
	}
	var out []scenario
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if len(out) < 80 {
		t.Fatalf("testdata/crossplatform.json holds %d scenarios", len(out))
	}
	return out
}

// target is the address of a scenario's route.
func target(sc scenario) string {
	if sc.Route != "capabilities" {
		return "/api/panel/cross-platform/" + sc.Route
	}
	id := url.PathEscape(sc.DeviceID)
	if strings.Trim(id, ".") == "" {
		// A browser would resolve dots; they reach a route only escaped
		id = strings.ReplaceAll(id, ".", "%2E")
	}
	return "/api/panel/cross-platform/capabilities/" + id
}

func TestRoutesMatchNode(t *testing.T) {
	var current scenario
	api := routetest.NewAPI()
	api.Fallback = func(routetest.Call) routetest.Reply {
		if current.API.Fail != "" {
			return routetest.Reply{}
		}
		return routetest.Reply{Status: current.API.Status, Body: current.API.Body}
	}
	admin := routetest.New(t, routetest.Options{API: api}).As("admin")

	for _, sc := range loadScenarios(t) {
		current = sc
		name := sc.Route + ", " + sc.Name
		got := admin.Do("GET", target(sc), "")
		if got.Status != sc.Answer.Status || got.Body != sc.Answer.Text {
			t.Errorf("%s:\n got %d %s\nNode %d %s", name, got.Status, got.Body, sc.Answer.Status, sc.Answer.Text)
		}
		if got.Header.Get("Content-Type") != "application/json; charset=utf-8" {
			t.Errorf("%s: Content-Type %q", name, got.Header.Get("Content-Type"))
		}
		var want []string
		for _, call := range sc.Calls {
			method, path, _ := strings.Cut(call, " ")
			want = append(want, method+" /api"+path)
		}
		if calls := api.Calls(); !reflect.DeepEqual(calls, want) {
			t.Errorf("%s: the API server was asked %q, Node asked %q", name, calls, want)
		}
	}
}

func TestGuards(t *testing.T) {
	api := routetest.NewAPI()
	api.On("GET /api/peers?page_size=9999", 200, `{"total":0,"data":[]}`)
	api.On("GET /api/peers/900100001", 200, `{"id":"900100001"}`)
	// Every role has device.view; the operator role loses it here
	w := routetest.New(t, routetest.Options{API: api, Roles: routetest.Overrides{"operator": {"device.view": false}}})
	for _, path := range []string{
		"/api/panel/cross-platform/distribution",
		"/api/panel/cross-platform/capabilities/900100001",
		"/api/panel/cross-platform/protocols",
	} {
		if got := w.SignedOut().Do("GET", path, ""); got.Status != 401 || got.Body != `{"success":false,"error":"Unauthorized. Please log in.","code":"session_required"}` {
			t.Errorf("%s signed out: %d %s", path, got.Status, got.Body)
		}
		if got := w.As("operator").Do("GET", path, ""); got.Status != 403 || got.Body != `{"success":false,"error":"Permission denied: device.view"}` {
			t.Errorf("%s without device.view: %d %s", path, got.Status, got.Body)
		}
		if calls := api.Calls(); calls != nil {
			t.Errorf("%s reached the API server for accounts that may not: %v", path, calls)
		}
		for _, role := range []string{"guest_issuer", "viewer", "admin"} {
			if got := w.As(role).Do("GET", path, ""); got.Status != 200 {
				t.Errorf("%s as %s: %d %s", path, role, got.Status, got.Body)
			}
		}
		if calls := api.Calls(); len(calls) != 3 {
			t.Errorf("%s: %d calls for three accounts that may: %v", path, len(calls), calls)
		}
	}
}

// TestCapabilitiesOfADeviceOutOfScope closes devices a and c to the
// viewer with a device group whose access list names the operator: the
// counts hold b alone, and the capabilities of any other device are
// refused before the API server is asked. The admin sees every device.
func TestCapabilitiesOfADeviceOutOfScope(t *testing.T) {
	devices := `[{"id":"a","platform":"Windows 11","version":"1.4","live_online":true,"supported_codecs":["vp9"]},` +
		`{"id":"b","platform":"Linux","version":"1.3","live_online":true,"supported_codecs":["h264"]},` +
		`{"id":"c","platform":"Linux","version":"1.3"}]`
	api := routetest.NewAPI()
	api.On("GET /api/peers", 200, devices)
	api.On("GET /api/peers?page_size=9999", 200, `{"total":3,"data":`+devices+`}`)
	for _, id := range []string{"a", "b"} {
		api.On("GET /api/peers/"+id, 200, `{"id":"`+id+`"}`)
	}
	w := routetest.New(t, routetest.Options{API: api})
	for _, statement := range []string{
		"INSERT INTO device_groups (id, guid, name) VALUES (90, 'closed', 'Closed')",
		"INSERT INTO device_group_members (device_group_id, peer_id) VALUES (90, 'a'), (90, 'c')",
		"INSERT INTO device_group_user_access (device_group_id, user_id) VALUES (90, 5)",
	} {
		if _, err := w.DB.Exec(context.Background(), statement); err != nil {
			t.Fatal(err)
		}
	}
	viewer, admin := w.As("viewer"), w.As("admin")
	for _, c := range []struct {
		who        *routetest.Agent
		path, want string
	}{
		{viewer, "/api/panel/cross-platform/distribution", `{"total":1,"platforms":{"linux":1},"archs":{"unknown":1},"versions":{"1.3":1}}`},
		{viewer, "/api/panel/cross-platform/protocols", `{"totalOnline":1,"codecs":{"h264":1},"features":{}}`},
		{admin, "/api/panel/cross-platform/distribution", `{"total":3,"platforms":{"windows":1,"linux":2},"archs":{"unknown":3},"versions":{"1.4":1,"1.3":2}}`},
		{admin, "/api/panel/cross-platform/protocols", `{"totalOnline":2,"codecs":{"vp9":1,"h264":1},"features":{}}`},
		{viewer, "/api/panel/cross-platform/capabilities/b", `{"id":"b"}`},
		{admin, "/api/panel/cross-platform/capabilities/a", `{"id":"a"}`},
	} {
		if got := c.who.Do("GET", c.path, ""); got.Status != 200 || got.Body != c.want {
			t.Errorf("%s: %d %s, want %s", c.path, got.Status, got.Body, c.want)
		}
	}
	api.Calls()
	for _, id := range []string{"a", "%20a%20", "zzz"} {
		got := viewer.Do("GET", "/api/panel/cross-platform/capabilities/"+id, "")
		if got.Status != 403 || !strings.HasPrefix(got.Body, `{"success":false,"error":`) {
			t.Errorf("capabilities of %s out of the viewer's scope: %d %s", id, got.Status, got.Body)
		}
	}
	for _, call := range api.Calls() {
		if strings.HasPrefix(call, "GET /api/peers/") {
			t.Errorf("the API server was asked %s for a device out of scope", call)
		}
	}
}
