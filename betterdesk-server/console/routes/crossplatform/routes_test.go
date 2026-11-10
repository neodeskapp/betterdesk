package crossplatform_test

import (
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
