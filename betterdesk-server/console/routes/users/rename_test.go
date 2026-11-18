package users

import (
	"context"
	"testing"

	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// A table an older database does not have is skipped, and the rest of the
// cascade still runs; on PostgreSQL the failed statement does not end the
// transaction. The recordings cover the missing table on PostgreSQL only:
// on SQLite the Node console's own shape fails earlier, at the address
// books (BUGS.md UM23).
func TestRenameSkipsAMissingTable(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		db = storetest.Prepare(t, db, storetest.GoServerFirst)
		ctx := context.Background()
		exec := func(query string, args ...any) {
			t.Helper()
			if _, err := db.Exec(ctx, query, args...); err != nil {
				t.Fatalf("%s: %v", query, err)
			}
		}
		exec(`DROP TABLE org_users`)
		id, err := db.Insert(ctx, `INSERT INTO users (username, password_hash, role) VALUES ('old_name', 'x', 'operator')`)
		if err != nil {
			t.Fatal(err)
		}
		exec(`INSERT INTO login_attempts (username, ip_address, success) VALUES ('old_name', '10.0.0.1', ?)`, db.Pick("0", "false"))
		exec(`INSERT INTO address_books (username, ab_type, data) VALUES ('old_name', 'legacy', '{}')`)

		outcome, err := renameAccount(ctx, db, id, "new_name")
		if err != nil || outcome != renamed {
			t.Fatalf("renameAccount = %v, %v", outcome, err)
		}
		for _, read := range []struct {
			query string
			args  []any
		}{
			{`SELECT username FROM users WHERE id = ?`, []any{id}},
			{`SELECT username FROM login_attempts`, nil},
			{`SELECT username FROM address_books`, nil},
		} {
			var got string
			if err := db.QueryRow(ctx, read.query, read.args...).Scan(&got); err != nil || got != "new_name" {
				t.Errorf("%s: %q, %v", read.query, got, err)
			}
		}
	})
}
