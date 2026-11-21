package main

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// rawSQLite runs statements on a SQLite file without foreign keys, as the
// signal server's connection writes.
func rawSQLite(t *testing.T, path string, stmts ...string) {
	t.Helper()
	handle, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()
	for _, stmt := range stmts {
		if _, err := handle.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
}

// pgCount runs a COUNT query in the schema sdb works in.
func pgCount(t *testing.T, sdb *store.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := sdb.QueryRow(testCtx(t), query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

// serverSQLite is a SQLite file as the server leaves it, with one user.
func serverSQLite(t *testing.T, username string) string {
	t.Helper()
	sdb, path := sqliteFile(t, "db_v2.sqlite3")
	prepareOrigin(t, sdb, goServerFirst)
	if _, err := sdb.Exec(testCtx(t), `INSERT INTO users (id, username, password_hash, role) VALUES (7, ?, 'hash', 'admin')`, username); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSQLite2PGStopsOnLeftoversUnlessTold(t *testing.T) {
	requirePostgres(t)
	src := serverSQLite(t, "admin")
	rawSQLite(t, src,
		`CREATE TABLE old_stuff (x TEXT)`, `INSERT INTO old_stuff VALUES ('kept by an old version')`,
		`ALTER TABLE users ADD COLUMN legacy_flag TEXT`, `UPDATE users SET legacy_flag = 'x'`)
	dst := storetest.Postgres(t)

	err := runSQLite2PG(src, pgAddress(t, dst), moveOptions{})
	if err == nil || !strings.Contains(err.Error(), "-skip-leftovers") {
		t.Fatalf("a source with a table and a column the server does not have: %v, want a refusal naming -skip-leftovers", err)
	}
	if n := pgCount(t, dst, `SELECT COUNT(*) FROM users`); n != 0 {
		t.Errorf("the refused run wrote %d users", n)
	}
	if n := pgCount(t, dst, `SELECT COUNT(*) FROM device_groups`); n != 0 {
		t.Errorf("the refused run left %d device groups the migrations seeded", n)
	}

	// Without -overwrite: the failed run left the database empty.
	if err := runSQLite2PG(src, pgAddress(t, dst), moveOptions{skipLeftovers: true}); err != nil {
		t.Fatalf("with -skip-leftovers: %v", err)
	}
	if n := pgCount(t, dst, `SELECT COUNT(*) FROM users WHERE id = 7 AND username = 'admin'`); n != 1 {
		t.Error("with -skip-leftovers the user did not move")
	}
	if n := pgCount(t, dst, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = 'old_stuff'`); n != 0 {
		t.Error("the leftover table went to PostgreSQL")
	}
}

func TestPG2SQLiteStopsOnLeftoversUnlessTold(t *testing.T) {
	requirePostgres(t)
	src := storetest.Postgres(t)
	prepareOrigin(t, src, goServerFirst)
	if _, err := src.Exec(testCtx(t), `CREATE TABLE old_stuff (x TEXT)`); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "db_v2.sqlite3")
	if err := runPG2SQLite(pgAddress(t, src), dst, moveOptions{}); err == nil || !strings.Contains(err.Error(), "-skip-leftovers") {
		t.Fatalf("a source with a table the server does not have: %v, want a refusal naming -skip-leftovers", err)
	}
	if exists(dst) {
		t.Error("the refused run left a destination")
	}
	if err := runPG2SQLite(pgAddress(t, src), dst, moveOptions{skipLeftovers: true}); err != nil {
		t.Fatalf("with -skip-leftovers: %v", err)
	}
	if tableExistsIn(t, dst, "old_stuff") {
		t.Error("the leftover table went to SQLite")
	}
}

func TestSQLite2PGReplacesAPostgresInstallOnlyWithOverwrite(t *testing.T) {
	requirePostgres(t)
	first, second := serverSQLite(t, "first"), serverSQLite(t, "second")
	dst := storetest.Postgres(t)
	if err := runSQLite2PG(first, pgAddress(t, dst), moveOptions{}); err != nil {
		t.Fatal(err)
	}
	err := runSQLite2PG(second, pgAddress(t, dst), moveOptions{})
	if err == nil || !strings.Contains(err.Error(), "-overwrite") {
		t.Fatalf("a PostgreSQL database with an install in it was written without -overwrite: %v", err)
	}
	if n := pgCount(t, dst, `SELECT COUNT(*) FROM users WHERE username = 'first'`); n != 1 {
		t.Fatal("the refused run changed the install in PostgreSQL")
	}
	if err := runSQLite2PG(second, pgAddress(t, dst), moveOptions{overwrite: true}); err != nil {
		t.Fatalf("with -overwrite: %v", err)
	}
	if n := pgCount(t, dst, `SELECT COUNT(*) FROM users`); n != 1 {
		t.Errorf("with -overwrite PostgreSQL has %d users, want the one of the second install", n)
	}
	if n := pgCount(t, dst, `SELECT COUNT(*) FROM users WHERE username = 'second'`); n != 1 {
		t.Error("with -overwrite the second install's user is missing")
	}
}

func TestSQLite2PGAppliesPostgresForeignKeysToRowsThatPointAtNothing(t *testing.T) {
	requirePostgres(t)
	src := serverSQLite(t, "admin")
	// The signal server's SQLite connection does not enforce foreign keys,
	// and the console's audit_log has none on SQLite.
	rawSQLite(t, src,
		`INSERT INTO audit_log (id, user_id, action) VALUES (1, 7, 'kept'), (2, 99, 'deleted user')`,
		`INSERT INTO tickets (id, title, created_by) VALUES (5, 'open', 'admin')`,
		`INSERT INTO ticket_comments (id, ticket_id, author, body) VALUES (1, 5, 'admin', 'kept'), (2, 99, 'admin', 'of a deleted ticket')`)
	dst := storetest.Postgres(t)
	if err := runSQLite2PG(src, pgAddress(t, dst), moveOptions{}); err != nil {
		t.Fatal(err)
	}
	if n := pgCount(t, dst, `SELECT COUNT(*) FROM audit_log WHERE id = 2 AND user_id IS NULL`); n != 1 {
		t.Error("the audit row of a deleted user did not keep its row with user_id NULL (ON DELETE SET NULL)")
	}
	if n := pgCount(t, dst, `SELECT COUNT(*) FROM audit_log WHERE id = 1 AND user_id = 7`); n != 1 {
		t.Error("the audit row of an existing user lost its user")
	}
	if n := pgCount(t, dst, `SELECT COUNT(*) FROM ticket_comments`); n != 1 {
		t.Errorf("PostgreSQL has %d ticket comments, want the one whose ticket exists (ON DELETE CASCADE)", n)
	}

	// A key without such a rule fails the run.
	broken := serverSQLite(t, "admin")
	rawSQLite(t, broken,
		`INSERT INTO org_users (id, org_id, username, password_hash) VALUES ('u1', 'no-such-org', 'x', 'hash')`)
	err := runSQLite2PG(broken, pgAddress(t, storetest.Postgres(t)), moveOptions{})
	if err == nil || !strings.Contains(err.Error(), "organizations") {
		t.Errorf("an organization user of a missing organization: %v, want a failure naming organizations", err)
	}
}

func TestSQLite2PGWritesTheDefaultWherePostgresTakesNoNULL(t *testing.T) {
	requirePostgres(t)
	src := serverSQLite(t, "admin")
	// SQLite's users.totp_secret takes NULL; the server's PostgreSQL one
	// does not and has the default ''.
	rawSQLite(t, src, `UPDATE users SET totp_secret = NULL`)
	dst := storetest.Postgres(t)
	if err := runSQLite2PG(src, pgAddress(t, dst), moveOptions{}); err != nil {
		t.Fatal(err)
	}
	if n := pgCount(t, dst, `SELECT COUNT(*) FROM users WHERE username = 'admin' AND totp_secret = ''`); n != 1 {
		t.Error("the user without a TOTP secret did not get the column's default ''")
	}
}

func TestPG2SQLiteWritesTheEmptyTextWhereTheServerDoes(t *testing.T) {
	requirePostgres(t)
	src := storetest.Postgres(t)
	prepareOrigin(t, src, goServerFirst)
	// The signal server's SQLite users.last_login is '' for an account
	// that never signed in; PostgreSQL has NULL there.
	if _, err := src.Exec(testCtx(t), `INSERT INTO users (id, username, password_hash, role, last_login) VALUES (7, 'admin', 'hash', 'admin', NULL)`); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "db_v2.sqlite3")
	if err := runPG2SQLite(pgAddress(t, src), dst, moveOptions{}); err != nil {
		t.Fatal(err)
	}
	handle, err := sql.Open("sqlite", "file:"+dst+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()
	var lastLogin sql.NullString
	if err := handle.QueryRow(`SELECT last_login FROM users WHERE id = 7`).Scan(&lastLogin); err != nil {
		t.Fatal(err)
	}
	if !lastLogin.Valid || lastLogin.String != "" {
		t.Errorf("last_login of an account that never signed in is %v, want '' as the server writes it", lastLogin)
	}
}

func TestSQLite2PGFailsWhenTheDestinationDropsRows(t *testing.T) {
	requirePostgres(t)
	src := serverSQLite(t, "admin")
	dst := storetest.Postgres(t)
	if err := createSchema(testCtx(t), pgAddress(t, dst), false); err != nil {
		t.Fatal(err)
	}
	// A trigger that swallows every new user: the insert succeeds and
	// writes nothing.
	for _, stmt := range []string{
		`CREATE FUNCTION swallow() RETURNS trigger AS $$ BEGIN RETURN NULL; END $$ LANGUAGE plpgsql`,
		`CREATE TRIGGER swallow BEFORE INSERT ON users FOR EACH ROW EXECUTE PROCEDURE swallow()`,
	} {
		if _, err := dst.Exec(testCtx(t), stmt); err != nil {
			t.Fatal(err)
		}
	}
	err := runSQLite2PG(src, pgAddress(t, dst), moveOptions{overwrite: true})
	if err == nil || !strings.Contains(err.Error(), "1 rows read, 0 written") {
		t.Fatalf("a user that was read and not written: %v, want a failure with the counts", err)
	}
}
