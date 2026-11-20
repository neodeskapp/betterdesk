package tenants_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/routes/internal/replay"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// knownDevices stands in for the API server. It knows the devices the
// recordings' console has in its table (D-ON to D-REAL) and the devices D1
// to D3 of the tests, and no other.
var knownDevices = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	id, ok := strings.CutPrefix(r.URL.Path, "/api/peers/")
	if ok && r.Method == http.MethodGet && known[id] {
		_, _ = w.Write([]byte(`{"id":"` + id + `","hostname":"pc-` + id + `"}`))
		return
	}
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(`{"error":"Peer not found"}`))
})

var known = map[string]bool{
	"D-ON": true, "D-OFF": true, "D-GONE": true, "D-NULLDEL": true, "D-TWO": true, "D-TEXT": true, "D-REAL": true,
	"D1": true, "D2": true, "D3": true,
}

// newWorld is the console of the tests, with the devices of knownDevices.
func newWorld(t *testing.T, db *store.DB) *replay.World {
	t.Helper()
	return replay.NewWorldWithAPI(t, db, nil, nil, knownDevices)
}

// A tenant gets devices the API server knows, named by their ids as text:
// a list of them, or one. Anything else is 400 and assigns nothing, where
// the route assigned one device per character of a text, failed with 500
// for a number and assigned ids of no device. The answer counts the
// devices the tenant did not have (BUGS TN4).
func TestAssignDevicesNeedsAListOfKnownDevices(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := newWorld(t, db)
		if got := w.Do(replay.Request{Role: "admin", Method: "POST", Path: "/api/tenants", Body: replay.Text(`{"name":"One"}`)}); got.Status != 201 {
			t.Fatalf("create: %d %s", got.Status, got.Body)
		}
		assign := func(body string) replay.Answer {
			return w.Do(replay.Request{Role: "admin", Method: "POST", Path: "/api/tenants/1/devices", Body: replay.Text(body)})
		}
		for body, want := range map[string]string{
			`{"device_ids":"D1"}`:                     "Invalid device_ids",
			`{"device_ids":5}`:                        "Invalid device_ids",
			`{"device_ids":true}`:                     "Invalid device_ids",
			`{"device_ids":{"length":2,"0":"D1"}}`:    "Invalid device_ids",
			`{"device_ids":{"length":0}}`:             "Invalid device_ids",
			`{"device_ids":["D1",7]}`:                 "Invalid device_ids",
			`{"device_ids":["D1",""]}`:                "Invalid device_ids",
			`{"device_ids":["D1",["D2"]]}`:            "Invalid device_ids",
			`{"device_id":5}`:                         "Invalid device_id",
			`{"device_id":["D1"]}`:                    "Invalid device_id",
			`{"device_id":{"id":"D1"}}`:               "Invalid device_id",
			`{"device_ids":["D1","unknown"]}`:         "Unknown device",
			`{"device_id":"d1"}`:                      "Unknown device",
			`{"device_ids":null,"device_id":"ghost"}`: "Unknown device",
			`{}`:                "device_id or device_ids required",
			`{"device_ids":[]}`: "device_id or device_ids required",
			`{"device_id":""}`:  "device_id or device_ids required",
		} {
			if got := assign(body); got.Status != 400 || got.Body != `{"error":"`+want+`"}` {
				t.Errorf("POST %s: %d %s, want 400 %s", body, got.Status, got.Body, want)
			}
		}
		if rows := w.Rows("SELECT device_id FROM tenant_devices", nil); rows != "[]" {
			t.Fatalf("the refused requests assigned %s", rows)
		}
		for body, want := range map[string]string{
			`{"device_ids":["D1","D2","D1"]}`:   `{"ok":true,"assigned":2}`,
			`{"device_id":"D1"}`:                `{"ok":true,"assigned":0}`,
			`{"device_ids":0,"device_id":"D3"}`: `{"ok":true,"assigned":1}`,
		} {
			if got := assign(body); got.Status != 200 || got.Body != want {
				t.Errorf("POST %s: %d %s, want 200 %s", body, got.Status, got.Body, want)
			}
		}
		if rows := w.Rows("SELECT device_id FROM tenant_devices ORDER BY device_id", nil); rows != `[{"device_id":"D1"},{"device_id":"D2"},{"device_id":"D3"}]` {
			t.Errorf("the tenant's devices: %s", rows)
		}
	})
}
