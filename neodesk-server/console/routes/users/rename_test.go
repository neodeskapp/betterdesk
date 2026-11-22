package users

import (
	"context"
	"strconv"
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

// Where the Node console created address_books on SQLite, the table is
// keyed by the account's id: a rename leaves the books where they are and
// goes through. Where it is keyed by name, the books follow the name
// (BUGS UM23).
func TestRenameWithAddressBooksKeyedByID(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		db = storetest.Prepare(t, db, storetest.NodeFirst)
		ctx := context.Background()
		id, err := db.Insert(ctx, `INSERT INTO users (username, password_hash, role) VALUES ('old_name', 'x', 'operator')`)
		if err != nil {
			t.Fatal(err)
		}
		byName := db.Shape().Table("address_books").Has("username")
		if byName == (db.Dialect() == store.SQLite) {
			t.Fatalf("address_books keyed by name: %v", byName)
		}
		book, read := `INSERT INTO address_books (user_id, ab_type, data) VALUES (?, 'legacy', '{}')`, `SELECT CAST(user_id AS TEXT) FROM address_books`
		args, want := []any{id}, strconv.FormatInt(id, 10)
		if byName {
			book, read = `INSERT INTO address_books (username, ab_type, data) VALUES (?, 'legacy', '{}')`, `SELECT username FROM address_books`
			args, want = []any{"old_name"}, "new_name"
		}
		if _, err := db.Exec(ctx, book, args...); err != nil {
			t.Fatal(err)
		}

		if outcome, err := renameAccount(ctx, db, id, "new_name"); err != nil || outcome != renamed {
			t.Fatalf("renameAccount = %v, %v", outcome, err)
		}
		var name, owner string
		if err := db.QueryRow(ctx, `SELECT username FROM users WHERE id = ?`, id).Scan(&name); err != nil || name != "new_name" {
			t.Errorf("the account's name: %q, %v", name, err)
		}
		if err := db.QueryRow(ctx, read).Scan(&owner); err != nil || owner != want {
			t.Errorf("the address book's owner: %q, %v; want %q", owner, err, want)
		}
	})
}
