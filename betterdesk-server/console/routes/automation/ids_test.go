package automation_test

import (
	"testing"

	"github.com/unitronix/betterdesk-server/console/routes/internal/replay"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// An id beyond 2147483647, the largest an id column holds on PostgreSQL,
// names nothing on either database (BUGS AU9). The old routes read the id
// as a JavaScript number: on SQLite more digits than it holds found the
// row of the nearest number, on PostgreSQL such an id failed with 500.
// Leading zeros still name the row.
func TestIDsBeyondTheColumnAreNotFound(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		seedOne(t, db)
		w := newWorld(t, db)
		if !db.IsPostgres() {
			w.Exec("INSERT INTO alert_rules (id, name, condition_type) VALUES (9007199254740992, 'far', 'custom')", nil)
		}
		w.Exec("INSERT INTO access_tokens (token, token_hash, user_id, client_id, expires_at, token_kind) VALUES (?, ?, 5, 'device-a', '2999-01-01 00:00:00', 'device')",
			[]any{"tok-device-a", "a70dc32ded6e642ec955672a88edb7cc9d6d9e2abb6bf78146a63491adb24155"})
		device := map[string]string{"Authorization": "Bearer tok-device-a"}
		before := state(w)
		for _, id := range []string{"2147483648", "4294967297", "9007199254740992", "9007199254740993", "99999999999999999999", "100000000000000000000000000000001"} {
			for _, req := range []struct {
				replay.Request
				want string
			}{
				{replay.Request{Path: "/api/automation/rules/" + id}, "Rule not found"},
				{replay.Request{Method: "PATCH", Path: "/api/automation/rules/" + id, Body: replay.Text(`{"description":"x"}`)}, "Rule not found"},
				{replay.Request{Method: "DELETE", Path: "/api/automation/rules/" + id}, "Rule not found"},
				{replay.Request{Method: "POST", Path: "/api/automation/alerts/" + id + "/ack", Body: replay.Text(`{}`)}, "Alert not found"},
				{replay.Request{Path: "/api/automation/commands/" + id}, "Command not found"},
				{replay.Request{Method: "POST", Path: "/api/bd/commands/" + id + "/result", Body: replay.Text(`{"status":"completed"}`), Headers: device}, "Command not found"},
			} {
				if req.Headers == nil {
					req.Role = "admin"
				}
				got := w.Do(req.Request)
				if got.Status != 404 || got.Body != `{"error":"`+req.want+`"}` {
					t.Errorf("%s %s: %d %s, want 404 %s", req.Method, req.Path, got.Status, got.Body, req.want)
				}
			}
		}
		if after := state(w); after != before {
			t.Errorf("the requests changed something:\nbefore %s\n after %s", before, after)
		}
		for _, path := range []string{"/api/automation/rules/0000000000000000000000000000001", "/api/automation/commands/001"} {
			if got := w.Do(replay.Request{Role: "admin", Path: path}); got.Status != 200 {
				t.Errorf("%s: %d %s, want 200", path, got.Status, got.Body)
			}
		}
	})
}
