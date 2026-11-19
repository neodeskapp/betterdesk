package tenants_test

import (
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/routes/internal/replay"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// TestNodeAnswers replays what the Node console's tenant routes answered
// with its real database adapter, on SQLite and on PostgreSQL
// (testdata/node_sqlite.json, testdata/node_postgres.json): every route
// for every role, every field with values of every JSON type, ids in
// every spelling. The Go console has to answer the same, byte for byte,
// on the same database, except for the steps the bug fixes change, which
// tenantFixes lists (fixed_test.go).
func TestNodeAnswers(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		defer tenantFixes.Unused(t, db)
		w.Run(replay.Load(t, db, "node"), tenantFixes.Check(w, nil))
	})
}

// Someone who is not signed in gets the console's 401 on every tenant
// route, reads included, where the routes answered 403 (BUGS TN6).
func TestSignedOutGets401(t *testing.T) {
	w := replay.NewWorld(t, storetest.Migrated(t), nil)
	for _, route := range []string{"GET /api/tenants", "GET /api/tenants/1", "POST /api/tenants", "PATCH /api/tenants/1", "DELETE /api/tenants/1",
		"GET /api/tenants/1/devices", "POST /api/tenants/1/devices", "DELETE /api/tenants/1/devices/x", "GET /api/tenants/1/users",
		"POST /api/tenants/1/users", "DELETE /api/tenants/1/users/1", "GET /api/tenants/1/stats"} {
		method, path, _ := strings.Cut(route, " ")
		req := replay.Request{Method: method, Path: path}
		if method == "POST" || method == "PATCH" {
			req.Body = replay.Text(`{"name":"x"}`)
		}
		if got := w.Do(req); got.Status != 401 || got.Body != sessionRequired {
			t.Errorf("%s by nobody: %d %s", route, got.Status, got.Body)
		}
	}
	if got := w.Do(replay.Request{Role: "operator", Path: "/api/tenants"}); got.Status != 403 || got.Body != `{"error":"Admin access required"}` {
		t.Errorf("an operator: %d %s", got.Status, got.Body)
	}
}
