package main

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIntoPostgresCountsRowsThatWereThere: rust2go and nodejs2go into
// PostgreSQL leave a peer or user that is there already as it was, and
// count it apart from the rows they copied, in each table's line and in
// the total (BUGS L137).
func TestIntoPostgresCountsRowsThatWereThere(t *testing.T) {
	var logs bytes.Buffer
	log.SetOutput(&logs)
	defer log.SetOutput(os.Stderr)
	dir := t.TempDir()
	peers := []string{
		`INSERT INTO peer (guid, id, uuid, pk, info) VALUES (x'01', '111111111', x'02', x'03', '{}')`,
		`INSERT INTO peer (guid, id, uuid, pk, info) VALUES (x'04', '222222222', x'05', x'06', '{}')`,
	}

	rust := filepath.Join(dir, "rust.sqlite3")
	execSQLite(t, rust, append([]string{rustPeerTable}, peers...)...)
	dsn := pgDSN(t, `INSERT INTO peers (id) VALUES ('111111111')`)
	if err := runRust2Go(rust, dsn, false); err != nil {
		t.Fatal(err)
	}
	if want := "Migration complete: 1 peers copied to PostgreSQL, 1 already there and left as they were"; !strings.Contains(logs.String(), want) {
		t.Errorf("rust2go's log has no line %q:\n%s", want, logs.String())
	}

	logs.Reset()
	node := filepath.Join(dir, "db_v2.sqlite3")
	auth := filepath.Join(dir, "auth.sqlite3")
	execSQLite(t, node, append([]string{nodePeerTable}, peers...)...)
	execSQLite(t, auth, "CREATE TABLE users (username TEXT, password_hash TEXT, role TEXT, created_at TEXT, last_login TEXT)",
		"INSERT INTO users VALUES ('admin', 'hash', 'admin', NULL, NULL)",
		"INSERT INTO users VALUES ('anna', 'hash', 'viewer', NULL, NULL)")
	dsn = pgDSN(t, `INSERT INTO peers (id) VALUES ('222222222')`, `INSERT INTO users (username, password_hash, role) VALUES ('admin', 'kept', 'admin')`)
	if err := runNodeJS2Go(node, auth, dsn, false); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"  peers: 1 rows copied (from Node.js peer table), 1 already in PostgreSQL and left as they were",
		"  users: 1 rows copied (from Node.js auth), 1 already in PostgreSQL and left as they were",
		"Migration complete: 2 rows copied to PostgreSQL, 2 already there and left as they were",
	} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("nodejs2go's log has no line %q:\n%s", want, logs.String())
		}
	}
}
