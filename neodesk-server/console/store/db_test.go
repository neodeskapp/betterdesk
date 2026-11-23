package store_test

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

func TestRebind(t *testing.T) {
	cases := []struct{ in, want string }{
		{`SELECT 1`, `SELECT 1`},
		{`SELECT * FROM t WHERE a = ? AND b = ?`, `SELECT * FROM t WHERE a = $1 AND b = $2`},
		{`SELECT '?' , "why?" FROM t WHERE a = ?`, `SELECT '?' , "why?" FROM t WHERE a = $1`},
		{`SELECT 'it''s ?' WHERE a = ?`, `SELECT 'it''s ?' WHERE a = $1`},
		{"SELECT a -- really?\nFROM t WHERE b = ?", "SELECT a -- really?\nFROM t WHERE b = $1"},
		{`UPDATE t SET a = ? WHERE b LIKE ? ESCAPE '\'`, `UPDATE t SET a = $1 WHERE b LIKE $2 ESCAPE '\'`},
		{`VALUES (?,?,?,?,?,?,?,?,?,?,?)`, `VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`},
	}
	for _, c := range cases {
		if got := store.Rebind(c.in); got != c.want {
			t.Errorf("Rebind(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}

func TestEscapeLike(t *testing.T) {
	if got, want := store.EscapeLike(`50%_off\now`), `50\%\_off\\now`; got != want {
		t.Errorf("EscapeLike = %q, want %q", got, want)
	}
}

func createItems(t *testing.T, db *store.DB) {
	t.Helper()
	ddl := db.Pick(
		`CREATE TABLE items (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, done INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE items (id SERIAL PRIMARY KEY, name TEXT NOT NULL, done BOOLEAN NOT NULL DEFAULT FALSE)`,
	)
	if _, err := db.Exec(context.Background(), ddl); err != nil {
		t.Fatalf("create table: %v", err)
	}
}

func TestInsertQueryAndTransactions(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		createItems(t, db)

		first, err := db.Insert(ctx, `INSERT INTO items (name) VALUES (?)`, "what?")
		if err != nil {
			t.Fatalf("insert: %v", err)
		}
		second, err := db.Insert(ctx, `INSERT INTO items (name) VALUES (?)`, "second")
		if err != nil {
			t.Fatalf("insert: %v", err)
		}
		if first != 1 || second != 2 {
			t.Errorf("ids = %d, %d, want 1, 2", first, second)
		}

		var name string
		var done bool
		if err := db.QueryRow(ctx, `SELECT name, done FROM items WHERE id = ?`, first).Scan(&name, &done); err != nil {
			t.Fatalf("query row: %v", err)
		}
		if name != "what?" || done {
			t.Errorf("row = %q, %v", name, done)
		}

		// A failed transaction leaves nothing behind
		boom := errors.New("boom")
		err = db.InTx(ctx, func(tx *store.Tx) error {
			if _, err := tx.Insert(ctx, `INSERT INTO items (name) VALUES (?)`, "rolled back"); err != nil {
				return err
			}
			return boom
		})
		if !errors.Is(err, boom) {
			t.Fatalf("InTx error = %v, want boom", err)
		}
		// A successful one commits
		err = db.InTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE items SET done = ? WHERE id = ?`, true, second)
			return err
		})
		if err != nil {
			t.Fatalf("InTx: %v", err)
		}

		rows, err := db.Query(ctx, `SELECT name, done FROM items ORDER BY id`)
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		defer rows.Close()
		var got []string
		for rows.Next() {
			if err := rows.Scan(&name, &done); err != nil {
				t.Fatal(err)
			}
			if done {
				name += " (done)"
			}
			got = append(got, name)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 || got[0] != "what?" || got[1] != "second (done)" {
			t.Errorf("rows = %v", got)
		}

		if err := db.QueryRow(ctx, `SELECT name FROM items WHERE id = ?`, 999).Scan(&name); !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("missing row error = %v, want sql.ErrNoRows", err)
		}
	})
}

// On SQLite, Query and QueryRow use read-only connections: a statement that
// changes data there is refused instead of slipping past the writer.
func TestSQLiteReadersCannotWrite(t *testing.T) {
	db := storetest.SQLite(t)
	ctx := context.Background()
	createItems(t, db)
	var id int64
	err := db.QueryRow(ctx, `INSERT INTO items (name) VALUES (?) RETURNING id`, "sneaky").Scan(&id)
	if err == nil {
		t.Fatal("a write through QueryRow succeeded on a read-only connection")
	}
	var n int
	if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM items`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("rows = %d, want 0", n)
	}
}

// Writers wait for each other instead of failing
// with "database is locked".
func TestConcurrentWriters(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		createItems(t, db)

		const writers, each = 8, 25
		var wg sync.WaitGroup
		errs := make(chan error, writers)
		for w := 0; w < writers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < each; i++ {
					err := db.InTx(ctx, func(tx *store.Tx) error {
						var n int
						if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM items`).Scan(&n); err != nil {
							return err
						}
						_, err := tx.Insert(ctx, `INSERT INTO items (name) VALUES (?)`, "w")
						return err
					})
					if err != nil {
						errs <- err
						return
					}
				}
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Fatalf("writer failed: %v", err)
		}
		var n int
		if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM items`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != writers*each {
			t.Errorf("rows = %d, want %d", n, writers*each)
		}
	})
}

// InSnapshot reads one state of the database: rows another connection
// writes after the first read are not seen until the snapshot ends, on
// both databases (a backup of a running server, L73).
func TestInSnapshotSeesOneState(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		if _, err := db.Exec(ctx, `CREATE TABLE snap (n INTEGER)`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, `INSERT INTO snap (n) VALUES (1)`); err != nil {
			t.Fatal(err)
		}
		count := func(q store.Querier) int {
			t.Helper()
			var n int
			if err := q.QueryRow(ctx, `SELECT COUNT(*) FROM snap`).Scan(&n); err != nil {
				t.Fatal(err)
			}
			return n
		}
		err := db.InSnapshot(ctx, func(tx *store.Tx) error {
			before := count(tx)
			if _, err := db.Exec(ctx, `INSERT INTO snap (n) VALUES (2)`); err != nil {
				return err
			}
			if after := count(tx); after != before {
				t.Errorf("the snapshot saw %d rows, then %d after another write", before, after)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if n := count(db); n != 2 {
			t.Errorf("after the snapshot: %d rows, want 2", n)
		}
	})
}
