package db

import (
	"testing"
)

// TestPeerChangesOnANodeAccessTokensTable: on a database whose
// access_tokens the console has not migrated yet (the Node console's
// table, without token_kind, so without device tokens) a delete, a ban and
// an id change go through and leave its rows alone (BUGS RV231, RV232).
func TestPeerChangesOnANodeAccessTokensTable(t *testing.T) {
	freshStores(t, func(t *testing.T, store Database) {
		rawExec(t, store, `CREATE TABLE access_tokens (
			id INTEGER PRIMARY KEY,
			token TEXT UNIQUE NOT NULL,
			user_id INTEGER NOT NULL,
			client_id TEXT DEFAULT '',
			client_uuid TEXT DEFAULT '',
			expires_at TEXT NOT NULL,
			revoked INTEGER DEFAULT 0
		)`)
		rawExec(t, store, `INSERT INTO access_tokens (id, token, user_id, client_id, expires_at) VALUES (1, 'node-token', 1, 'NODE1', '2099-01-01')`)
		for _, id := range []string{"NODE1", "NODE2", "NODE3"} {
			if err := store.UpsertPeer(&Peer{ID: id}); err != nil {
				t.Fatal(err)
			}
		}
		for _, step := range []struct {
			name string
			run  func() error
		}{
			{"ban", func() error { return store.BanPeer("NODE1", "test") }},
			{"soft delete", func() error { return store.DeletePeer("NODE2") }},
			{"hard delete", func() error { return store.HardDeletePeer("NODE2") }},
			{"change id", func() error { return store.ChangePeerID("NODE3", "NODE4", "test") }},
		} {
			if err := step.run(); err != nil {
				t.Errorf("%s: %v", step.name, err)
			}
		}
		var revoked int
		var err error
		switch d := store.(type) {
		case *SQLiteDB:
			err = d.db.QueryRow(`SELECT revoked FROM access_tokens WHERE id = 1`).Scan(&revoked)
		case *PostgresDB:
			err = d.pool.QueryRow(d.ctx, `SELECT revoked FROM access_tokens WHERE id = 1`).Scan(&revoked)
		}
		if err != nil || revoked != 0 {
			t.Errorf("the Node table's token: revoked %d, %v; want it left alone", revoked, err)
		}
	})
}
