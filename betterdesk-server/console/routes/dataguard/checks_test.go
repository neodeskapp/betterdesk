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
