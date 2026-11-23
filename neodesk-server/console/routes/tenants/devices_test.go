package tenants_test

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/replay"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// knownDevices stands in for the API server. It knows the devices the
// recordings' console has in its table (D-ON to D-REAL) and the devices D1
// to D3 of the tests, and no other. Online are D-ON and D-NULLDEL, which
// the recordings' console had online, and D1.
var knownDevices = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodGet && r.URL.Path == "/api/peers" {
		var list []string
		for _, id := range slices.Sorted(maps.Keys(known)) {
			list = append(list, peerJSON(id))
		}
		_, _ = w.Write([]byte("[" + strings.Join(list, ",") + "]"))
		return
	}
	id, ok := strings.CutPrefix(r.URL.Path, "/api/peers/")
	if ok && r.Method == http.MethodGet && known[id] {
		_, _ = w.Write([]byte(peerJSON(id)))
		return
	}
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(`{"error":"Peer not found"}`))
})

var known = map[string]bool{
	"D-ON": true, "D-OFF": true, "D-GONE": true, "D-NULLDEL": true, "D-TWO": true, "D-TEXT": true, "D-REAL": true,
	"D1": true, "D2": true, "D3": true,
}

// peerJSON is a known device as the API server sends it.
func peerJSON(id string) string {
	online := id == "D-ON" || id == "D-NULLDEL" || id == "D1"
	return fmt.Sprintf(`{"id":%q,"hostname":"pc-%s","live_online":%v}`, id, id, online)
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
		// In this order: the count depends on what the tenant has already
		for _, c := range []struct{ body, want string }{
			{`{"device_ids":["D1","D2","D1"]}`, `{"ok":true,"assigned":2}`},
			{`{"device_id":"D1"}`, `{"ok":true,"assigned":0}`},
			{`{"device_ids":0,"device_id":"D3"}`, `{"ok":true,"assigned":1}`},
		} {
			if got := assign(c.body); got.Status != 200 || got.Body != c.want {
				t.Errorf("POST %s: %d %s, want 200 %s", c.body, got.Status, got.Body, c.want)
			}
		}
		if rows := w.Rows("SELECT device_id FROM tenant_devices ORDER BY device_id", nil); rows != `[{"device_id":"D1"},{"device_id":"D2"},{"device_id":"D3"}]` {
			t.Errorf("the tenant's devices: %s", rows)
		}
	})
}

// A tenant gets users that have an account, named by their id, a whole
// number from 1. A tenant that does not exist is 404, and an id that is
// no such number or names no account is 400 before anything is written,
// where the route answered 200 {"ok":false} or stored the id as it came:
// 1.5 or true on SQLite (BUGS TN5).
func TestAssignUserChecksTenantAndUser(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := newWorld(t, db)
		if got := w.Do(replay.Request{Role: "admin", Method: "POST", Path: "/api/tenants", Body: replay.Text(`{"name":"One"}`)}); got.Status != 201 {
			t.Fatalf("create: %d %s", got.Status, got.Body)
		}
		assign := func(tenant, body string) replay.Answer {
			return w.Do(replay.Request{Role: "admin", Method: "POST", Path: "/api/tenants/" + tenant + "/users", Body: replay.Text(body)})
		}
		if got := assign("2", `{"user_id":2}`); got.Status != 404 || got.Body != `{"error":"Tenant not found"}` {
			t.Errorf("a tenant that does not exist: %d %s", got.Status, got.Body)
		}
		for body, want := range map[string]string{
			`{"user_id":"2"}`: "Invalid user_id", `{"user_id":" 3 "}`: "Invalid user_id", `{"user_id":1.5}`: "Invalid user_id",
			`{"user_id":true}`: "Invalid user_id", `{"user_id":[2]}`: "Invalid user_id", `{"user_id":{"id":2}}`: "Invalid user_id",
			`{"user_id":-1}`: "Invalid user_id", `{"user_id":3000000000}`: "Invalid user_id", `{"user_id":1e400}`: "Invalid user_id",
			`{"user_id":999}`: "Unknown user", `{"user_id":0}`: "user_id required", `{}`: "user_id required",
		} {
			if got := assign("1", body); got.Status != 400 || got.Body != `{"error":"`+want+`"}` {
				t.Errorf("POST %s: %d %s, want 400 %s", body, got.Status, got.Body, want)
			}
		}
		if rows := w.Rows("SELECT user_id FROM tenant_users", nil); rows != "[]" {
			t.Fatalf("the refused requests assigned %s", rows)
		}
		for range 2 {
			if got := assign("1", `{"user_id":2}`); got.Status != 200 || got.Body != `{"ok":true}` {
				t.Errorf("assign user 2: %d %s", got.Status, got.Body)
			}
		}
		if rows := w.Rows("SELECT user_id FROM tenant_users", nil); rows != `[{"user_id":2}]` {
			t.Errorf("the tenant's users: %s", rows)
		}
	})
}

// A tenant's devices are online when the API server has them online, as
// in the device list. The console's own device table holds a status that
// nothing keeps up to date, which the routes counted (BUGS TN9).
func TestOnlineComesFromTheAPI(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := newWorld(t, db)
		if got := w.Do(replay.Request{Role: "admin", Method: "POST", Path: "/api/tenants", Body: replay.Text(`{"name":"One"}`)}); got.Status != 201 {
			t.Fatalf("create: %d %s", got.Status, got.Body)
		}
		// The console's table has D2 and D3 online, the API server only D1
		for _, id := range []string{"D2", "D3"} {
			w.Exec("INSERT INTO peer (id, note, status_online) VALUES (?, '', "+db.Pick("1", "TRUE")+")", []any{id})
		}
		if got := w.Do(replay.Request{Role: "admin", Method: "POST", Path: "/api/tenants/1/devices", Body: replay.Text(`{"device_ids":["D1","D2","D3"]}`)}); got.Status != 200 {
			t.Fatalf("assign: %d %s", got.Status, got.Body)
		}
		stats := w.Do(replay.Request{Role: "admin", Path: "/api/tenants/1/stats"})
		if !strings.Contains(stats.Body, `"total_devices":3,"online_devices":1,`) {
			t.Errorf("the stats: %d %s, want 1 of 3 devices online", stats.Status, stats.Body)
		}
		answer := w.Do(replay.Request{Role: "admin", Path: "/api/tenants/1/devices"})
		var devices []struct {
			DeviceID string `json:"device_id"`
			Online   bool   `json:"online"`
		}
		_ = json.Unmarshal([]byte(answer.Body), &devices)
		online := map[string]bool{}
		for _, d := range devices {
			online[d.DeviceID] = d.Online
		}
		if want := map[string]bool{"D1": true, "D2": false, "D3": false}; !maps.Equal(online, want) {
			t.Errorf("the devices: %d %s, want only D1 online", answer.Status, answer.Body)
		}
	})
}

// A tenant id written with an escape (%31 for 1) names no tenant, also
// when a later part of the path holds a byte Go escapes anew (a quote):
// the id is read from the path as the client sent it, as the route table
// reads it, not from Go's escaped path, which then holds the id decoded.
func TestEscapedTenantIDNamesNoTenant(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := newWorld(t, db)
		if got := w.Do(replay.Request{Role: "admin", Method: "POST", Path: "/api/tenants", Body: replay.Text(`{"name":"One"}`)}); got.Status != 201 {
			t.Fatalf("create: %d %s", got.Status, got.Body)
		}
		w.Exec(`INSERT INTO tenant_devices (tenant_id, device_id) VALUES (1, 'D"1')`, nil)
		remove := func(tenant string) replay.Answer {
			return w.Do(replay.Request{Role: "admin", Method: "DELETE", Path: "/api/tenants/" + tenant + `/devices/D"1`})
		}
		if got := remove("%31"); got.Status != 404 || got.Body != `{"error":"Assignment not found"}` {
			t.Errorf("DELETE with the tenant id %%31: %d %s, want 404", got.Status, got.Body)
		}
		if rows := w.Rows("SELECT device_id FROM tenant_devices", nil); rows != `[{"device_id":"D\"1"}]` {
			t.Fatalf("the tenant's devices after the escaped id: %s", rows)
		}
		if got := remove("1"); got.Status != 200 || got.Body != `{"ok":true}` {
			t.Errorf("DELETE with the tenant id 1: %d %s, want 200", got.Status, got.Body)
		}
	})
}
