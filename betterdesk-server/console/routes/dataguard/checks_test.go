package dataguard_test

import (
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/routes/internal/replay"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// policyRow reads the columns a request writes of a policy, as JSON rows.
func policyRow(w *replay.World, id string) string {
	return w.Rows("SELECT name, description, policy_type, action, scope, CASE WHEN enabled THEN 1 ELSE 0 END AS enabled FROM dlp_policies WHERE id = ?", []any{id})
}

// Every member of a policy has its type, on create and in a change, and
// enabled is read the same way on both databases (BUGS DG1).
func TestPolicyMembersOfTheWrongType(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		post := func(body string) replay.Answer {
			return w.Do(replay.Request{Role: "admin", Method: "POST", Path: "/api/dataguard/policies", Body: replay.Text(body)})
		}
		patch := func(body string) replay.Answer {
			return w.Do(replay.Request{Role: "admin", Method: "PATCH", Path: "/api/dataguard/policies/1", Body: replay.Text(body)})
		}
		for body, want := range map[string]string{
			`{"name":"x","description":5}`:                    `{"error":"Invalid description"}`,
			`{"name":"x","policy_type":true}`:                 `{"error":"Invalid policy_type"}`,
			`{"name":"x","action":["block"]}`:                 `{"error":"Invalid action"}`,
			`{"name":"x","scope":{}}`:                         `{"error":"Invalid scope"}`,
			`{"name":"x","enabled":"no"}`:                     `{"error":"Invalid enabled"}`,
			`{"name":"x","enabled":2}`:                        `{"error":"Invalid enabled"}`,
			`{"name":"x","enabled":"abc"}`:                    `{"error":"Invalid enabled"}`,
			`{"name":"x","enabled":null}`:                     `{"error":"Invalid enabled"}`,
			`{"name":"x","description":{},"scope":["a","b"]}`: `{"error":"Invalid description"}`,
		} {
			if got := post(body); got.Status != 400 || got.Body != want {
				t.Errorf("POST %s: %d %s, want 400 %s", body, got.Status, got.Body, want)
			}
		}
		if rows := w.Rows("SELECT COUNT(*) AS n FROM dlp_policies", nil); rows != `[{"n":0}]` {
			t.Fatalf("policies stored: %s", rows)
		}
		if got := post(`{"name":"Kept","description":null,"scope":null,"enabled":0,"rules":[]}`); got.Status != 201 {
			t.Fatalf("create: %d %s", got.Status, got.Body)
		}
		before := policyRow(w, "1")
		if before != `[{"name":"Kept","description":"","policy_type":"","action":"log","scope":"","enabled":0}]` {
			t.Errorf("the policy as created: %s", before)
		}
		for body, want := range map[string]string{
			`{"name":null}`:        `{"error":"Policy name is required"}`,
			`{"name":" "}`:         `{"error":"Policy name is required"}`,
			`{"name":5}`:           `{"error":"Policy name is required"}`,
			`{"description":null}`: `{"error":"Invalid description"}`,
			`{"action":null}`:      `{"error":"Invalid action"}`,
			`{"policy_type":7}`:    `{"error":"Invalid policy_type"}`,
			`{"enabled":"no"}`:     `{"error":"Invalid enabled"}`,
			`{"enabled":"false"}`:  `{"error":"Invalid enabled"}`,
		} {
			if got := patch(body); got.Status != 400 || got.Body != want {
				t.Errorf("PATCH %s: %d %s, want 400 %s", body, got.Status, got.Body, want)
			}
		}
		if after := policyRow(w, "1"); after != before {
			t.Errorf("the policy after the refused changes: %s", after)
		}
		if got := patch(`{"name":" Renamed ","scope":null,"enabled":true}`); got.Status != 200 || !strings.Contains(got.Body, `"name":"Renamed"`) {
			t.Errorf("a change: %d %s", got.Status, got.Body)
		}
		if row := policyRow(w, "1"); row != `[{"name":"Renamed","description":"","policy_type":"","action":"log","scope":null,"enabled":1}]` {
			t.Errorf("the policy after a change: %s", row)
		}
	})
}

// A change that names no column answers the policy as it is, where it
// answered null (BUGS DG2).
func TestUpdateWithoutColumnsAnswersThePolicy(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		created := w.Do(replay.Request{Role: "admin", Method: "POST", Path: "/api/bd/policies", Body: replay.Text(`{"name":"Kept"}`)})
		if created.Status != 201 {
			t.Fatalf("create: %d %s", created.Status, created.Body)
		}
		for _, body := range []string{`{}`, `{"unknown":1}`, `[{"name":"In a list"}]`} {
			got := w.Do(replay.Request{Role: "admin", Method: "PATCH", Path: "/api/dataguard/policies/1", Body: replay.Text(body)})
			if got.Status != 200 || replay.Normalize(got.Body) != replay.Normalize(created.Body) {
				t.Errorf("PATCH %s: %d %s, want the policy %s", body, got.Status, got.Body, created.Body)
			}
		}
	})
}

// The statistics of no event are zeros on both databases, where SQLite
// answered null for the four sums (BUGS DG4).
func TestStatsWithoutEventsAreZero(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		for _, path := range []string{"/api/dataguard/stats", "/api/bd/stats"} {
			if got := w.Do(replay.Request{Role: "admin", Path: path}); got.Status != 200 || got.Body != `{"total":0,"blocked":0,"logged":0,"usb_events":0,"file_events":0}` {
				t.Errorf("%s: %d %s", path, got.Status, got.Body)
			}
		}
	})
}

// A row whose details or rules hold no JSON does not fail a list: the
// details go out as the text they hold, the rules as no rules (BUGS DG3).
func TestAnUnreadableRowDoesNotFailTheList(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		broken := db.Pick(`x`, `"x"`)
		w.Exec("INSERT INTO dlp_events (device_id, event_source, event_type, action, details) VALUES ('D1', 'usb', 'info', 'log', ?)", []any{broken})
		w.Exec("INSERT INTO dlp_policies (name, rules) VALUES ('broken', ?)", []any{db.Pick(`x`, `"x"`)})
		events := w.Do(replay.Request{Role: "admin", Path: "/api/dataguard/events"})
		if events.Status != 200 || !strings.Contains(events.Body, `"details":"x"`) {
			t.Errorf("events: %d %s", events.Status, events.Body)
		}
		for _, path := range []string{"/api/dataguard/policies", "/api/dataguard/policies/1"} {
			if got := w.Do(replay.Request{Role: "admin", Path: path}); got.Status != 200 || !strings.Contains(got.Body, `"name":"broken"`) || !strings.Contains(got.Body, `"rules":[]`) {
				t.Errorf("%s: %d %s", path, got.Status, got.Body)
			}
		}
	})
}

// A device gets the policies that are on, with enabled read as one flag
// and rules that are no list as no rules; a policy whose rules cannot be
// read does not fail the list (BUGS DV7).
func TestActivePoliciesSkipABadRow(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		w.Exec("INSERT INTO access_tokens (token, token_hash, user_id, client_id, expires_at) VALUES (?, ?, 5, 'device-a', '2999-01-01 00:00:00')",
			[]any{"tok-device-a", "a70dc32ded6e642ec955672a88edb7cc9d6d9e2abb6bf78146a63491adb24155"})
		w.Exec("INSERT INTO dlp_policies (id, name, rules) VALUES (1, 'list', '[1]')", nil)
		w.Exec("INSERT INTO dlp_policies (id, name, rules) VALUES (2, 'broken', ?)", []any{db.Pick(`x`, `"x"`)})
		w.Exec("INSERT INTO dlp_policies (id, name, rules) VALUES (3, 'object', '{\"a\":1}')", nil)
		if !db.IsPostgres() {
			w.Exec("INSERT INTO dlp_policies (id, name, enabled, rules) VALUES (4, 'enabled yes', 'yes', '[2]')", nil)
		}
		got := w.Do(replay.Request{Path: "/api/bd/dlp-policies", Headers: map[string]string{"Authorization": "Bearer tok-device-a"}})
		want := `[{"id":1,"name":"list","rules":[1]},{"id":2,"name":"broken","rules":[]},{"id":3,"name":"object","rules":[]}]`
		if got.Status != 200 || got.Body != want {
			t.Errorf("the policies of a device: %d %s, want %s", got.Status, got.Body, want)
		}
	})
}

// A filter of the event list given twice or as a list filters by its
// first value, and one given as an object does not filter, where the
// database refused the list or the object with 500 (BUGS DG8).
func TestEventFiltersGivenTwiceOrAsAnObject(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		w.Exec("INSERT INTO dlp_events (device_id, event_source, event_type, action, details) VALUES ('D1', 'usb', 'info', 'log', '{}')", nil)
		w.Exec("INSERT INTO dlp_events (device_id, event_source, event_type, action, details) VALUES ('D2', 'file', 'warn', 'block', '{}')", nil)
		for query, want := range map[string]string{
			"device_id=D1&device_id=D2":        "D1",
			"device_id[]=D2&device_id[]=D1":    "D2",
			"device_id[x]=D1":                  "D1 D2",
			"device_id[]=":                     "D1 D2",
			"event_source=file&event_source=x": "D2",
			"event_source[x]=file":             "D1 D2",
			"event_type[]=info":                "D1",
			"event_type[a][b]=info":            "D1 D2",
			"from[x]=2999-01-01&to[]=":         "D1 D2",
			"from=2000-01-01&from=2999-01-01":  "D1 D2",
			"to[x]=2000-01-01&device_id=D2":    "D2",
			"device_id=D2&device_id[x]=D1":     "D1 D2",
			"device_id[x]=D1&device_id=D2":     "D1 D2",
			"event_source=usb&event_type=warn": "",
		} {
			for _, mount := range []string{"/api/dataguard", "/api/bd"} {
				got := w.Do(replay.Request{Role: "admin", Path: mount + "/events?" + query})
				devices := ""
				for _, id := range []string{"D1", "D2"} {
					if strings.Contains(got.Body, `"device_id":"`+id+`"`) {
						devices += id + " "
					}
				}
				if got.Status != 200 || strings.TrimSpace(devices) != want {
					t.Errorf("%s/events?%s: %d with %q, want 200 with %q: %s", mount, query, got.Status, devices, want, got.Body)
				}
			}
		}
	})
}
