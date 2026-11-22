package rustdeskapi_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/unitronix/betterdesk-server/console/routes/internal/replay"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// adminToken is an access token of e2e_admin that strategyWorld stores.
const adminToken = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"

func strategyWorld(t *testing.T, db *store.DB) *replay.World {
	t.Helper()
	w := world(t, db, nil)
	tokenFor(w, db, 1, adminToken)
	return w
}

// createStrategy stores a strategy through the API and returns its guid.
func createStrategy(t *testing.T, w *replay.World, body string) string {
	t.Helper()
	got := w.Do(replay.Request{Method: "POST", Path: "/api/strategies", Headers: bearer(adminToken), Body: replay.Text(body)})
	var created struct {
		GUID string `json:"guid"`
	}
	if err := json.Unmarshal([]byte(got.Body), &created); err != nil || got.Status != http.StatusOK || created.GUID == "" {
		t.Fatalf("create a strategy: %d %s", got.Status, got.Body)
	}
	return created.GUID
}

// enabledOf reads the switch of a strategy from GET /api/strategies/:guid.
func enabledOf(t *testing.T, w *replay.World, guid string) bool {
	t.Helper()
	got := w.Do(replay.Request{Path: "/api/strategies/" + guid, Headers: bearer(adminToken)})
	var s struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.Unmarshal([]byte(got.Body), &s); err != nil || got.Status != http.StatusOK {
		t.Fatalf("read the strategy: %d %s", got.Status, got.Body)
	}
	return s.Enabled
}

// PUT /api/strategies/:guid/status sets the switch from {"enabled": true}
// or {"enabled": false}, on and off again. A body without a boolean
// enabled is refused and changes nothing.
func TestStrategyStatusSwitchesOn(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := strategyWorld(t, db)
		guid := createStrategy(t, w, `{"name":"rd-status","enabled":false}`)
		status := func(path, body string) replay.Answer {
			return w.Do(replay.Request{Method: "PUT", Path: path, Headers: bearer(adminToken), Body: replay.Text(body)})
		}
		for _, on := range []bool{true, false, true} {
			body, _ := json.Marshal(map[string]bool{"enabled": on})
			if got := status("/api/strategies/"+guid+"/status", string(body)); got.Status != http.StatusOK || got.Body != `{"status":"ok"}` {
				t.Fatalf("switch to %t: %d %s", on, got.Status, got.Body)
			}
			if enabledOf(t, w, guid) != on {
				t.Errorf("after switching to %t the strategy is not", on)
			}
		}
		for _, body := range []string{`{}`, `{"enabled":"false"}`, `{"enabled":0}`, `{"enabled":null}`, `[false]`} {
			if got := status("/api/strategies/"+guid+"/status", body); got.Status != http.StatusBadRequest || got.Body != `{"error":"enabled must be true or false"}` {
				t.Errorf("%s: %d %s", body, got.Status, got.Body)
			}
		}
		if !enabledOf(t, w, guid) {
			t.Error("a refused body switched the strategy off")
		}
		if got := status("/api/strategies/rd-none/status", `{"enabled":true}`); got.Status != http.StatusNotFound || got.Body != `{"error":"strategy not found"}` {
			t.Errorf("an unknown strategy: %d %s", got.Status, got.Body)
		}
	})
}

// GET /api/strategies reports a strategy that is switched on as enabled
// on both databases: a stored 1 on SQLite, a stored true on PostgreSQL.
func TestStrategyListReadsTheSwitch(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := strategyWorld(t, db)
		on := createStrategy(t, w, `{"name":"rd-on","enabled":true}`)
		off := createStrategy(t, w, `{"name":"rd-off","enabled":false}`)
		got := w.Do(replay.Request{Path: "/api/strategies", Headers: bearer(adminToken)})
		var list struct {
			Data []struct {
				GUID    string `json:"guid"`
				Enabled bool   `json:"enabled"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(got.Body), &list); err != nil || got.Status != http.StatusOK {
			t.Fatalf("GET /api/strategies: %d %s", got.Status, got.Body)
		}
		enabled := map[string]bool{}
		for _, s := range list.Data {
			enabled[s.GUID] = s.Enabled
		}
		if !enabled[on] || enabled[off] || len(enabled) != 2 {
			t.Errorf("the switches in the list: %s", got.Body)
		}
	})
}

// POST /api/strategies/assign names what a request got wrong (a reference
// to nothing, a list that is none) and hides every other failure behind
// "Assign failed": the database's own text stays in the log.
func TestAssignHidesDatabaseErrors(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := strategyWorld(t, db)
		guid := createStrategy(t, w, `{"name":"rd-assign"}`)
		assign := func(body string) replay.Answer {
			return w.Do(replay.Request{Method: "POST", Path: "/api/strategies/assign", Headers: bearer(adminToken), Body: replay.Text(body)})
		}
		// Without the signal server's device table a device reference fails
		// in the database
		if got := assign(`{"strategy":"` + guid + `","peers":["RDNONE"]}`); got.Status != http.StatusBadRequest || got.Body != `{"error":"Assign failed"}` {
			t.Errorf("without the device table: %d %s", got.Status, got.Body)
		}
		w.Exec("CREATE TABLE peers (id TEXT PRIMARY KEY, uuid TEXT, guid TEXT)", nil)
		for body, want := range map[string]string{
			`{"strategy":"` + guid + `","peers":["RDNONE"]}`:       "peer not found",
			`{"strategy":"` + guid + `","peers":[" "]}`:            "empty peer reference",
			`{"strategy":"` + guid + `","users":["rd-nobody"]}`:    "user not found",
			`{"strategy":"` + guid + `","groups":["rd-no-group"]}`: "device group not found",
			`{"strategy":"rd-none","users":["e2e_viewer"]}`:        "strategy not found",
			`{"strategy":"` + guid + `","peers":5,"users":["x"]}`:  "number 5 is not iterable (cannot read property Symbol(Symbol.iterator))",
			`{"strategy":"` + guid + `","users":[{"toString":1}]}`: "Cannot convert object to primitive value",
		} {
			if got := assign(body); got.Status != http.StatusBadRequest || got.Body != `{"error":"`+want+`"}` {
				t.Errorf("%s: %d %s, want the text %q", body, got.Status, got.Body, want)
			}
		}
		if got := assign(`{"strategy":"` + guid + `","users":["e2e_viewer"]}`); got.Status != http.StatusOK {
			t.Fatalf("a good assignment: %d %s", got.Status, got.Body)
		}
		w.Exec("DROP TABLE strategy_assignments", nil)
		if got := assign(`{"strategy":"` + guid + `","users":["e2e_viewer"]}`); got.Status != http.StatusBadRequest || got.Body != `{"error":"Assign failed"}` {
			t.Errorf("a failing database: %d %s", got.Status, got.Body)
		}
	})
}
