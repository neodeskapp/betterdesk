package tenants_test

import (
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
// on the same database.
func TestNodeAnswers(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		w.Run(replay.Load(t, db, "node"), nil)
	})
}
