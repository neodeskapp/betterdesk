package db

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// hbbsSchema is the table RustDesk's hbbs creates in db_v2.sqlite3
// (rustdesk-server, src/database.rs).
const hbbsSchema = `
create table if not exists peer (
    guid blob primary key not null,
    id varchar(100) not null,
    uuid blob not null,
    pk blob not null,
    created_at datetime not null default(current_timestamp),
    user blob,
    status tinyint,
    note varchar(300),
    info text not null
) without rowid;
create unique index if not exists index_peer_id on peer (id);
create index if not exists index_peer_user on peer (user);
create index if not exists index_peer_created_at on peer (created_at);
create index if not exists index_peer_status on peer (status);
insert into peer (guid, id, uuid, pk, info) values (x'01', 'HBBS1', x'02', x'03', '{"ip":"192.0.2.1"}');`

// writeSQLite makes a SQLite file at path from the statements.
func writeSQLite(t *testing.T, path, statements string) {
	t.Helper()
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Exec(statements); err != nil {
		t.Fatal(err)
	}
}

// A database hbbs made is moved aside with its -wal and -shm files, so the
// server starts with a database of its own; its devices stay in the file
// it was moved to (L223).
func TestRustDeskDatabaseIsMovedAside(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db_v2.sqlite3")
	writeSQLite(t, path, hbbsSchema)
	for _, side := range []string{"-wal", "-shm"} {
		if err := os.WriteFile(path+side, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	moved, err := MoveAsideRustDeskDatabase(path, time.Date(2026, 11, 22, 18, 30, 5, 0, time.UTC))
	if err != nil {
		t.Fatalf("MoveAsideRustDeskDatabase: %v", err)
	}
	if want := path + ".rustdesk-20261122-183005"; moved != want {
		t.Fatalf("moved to %q, want %q", moved, want)
	}
	for _, name := range []string{path, path + "-wal", path + "-shm"} {
		if _, err := os.Stat(name); !os.IsNotExist(err) {
			t.Errorf("%s is still there: %v", filepath.Base(name), err)
		}
	}
	for _, side := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(moved + side); err != nil {
			t.Errorf("the %s file did not go with the database: %v", side, err)
		}
	}
	conn, err := sql.Open("sqlite", moved)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var id string
	if err := conn.QueryRow(`SELECT id FROM peer`).Scan(&id); err != nil || id != "HBBS1" {
		t.Errorf("the device in the moved file: %q, %v", id, err)
	}

	// The server's own database takes the place
	database, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.Migrate(); err != nil {
		t.Fatalf("Migrate on the new database: %v", err)
	}
}

// Databases of BetterDesk stay where they are: one the server made (it has
// peers, also next to an older peer table), one with only the console's
// peer table, an empty file, a missing file and a PostgreSQL address.
func TestOtherDatabasesStay(t *testing.T) {
	dir := t.TempDir()
	server := filepath.Join(dir, "server.sqlite3")
	database, err := OpenSQLite(server)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(); err != nil {
		t.Fatal(err)
	}
	database.Close()
	upgraded := filepath.Join(dir, "upgraded.sqlite3")
	writeSQLite(t, upgraded, hbbsSchema+`; create table peers (id text primary key);`)
	console := filepath.Join(dir, "console.sqlite3")
	writeSQLite(t, console, `create table peer (id text primary key, uuid text default '', ip text default '')`)
	empty := filepath.Join(dir, "empty.sqlite3")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{server, upgraded, console, empty, filepath.Join(dir, "missing.sqlite3"), "postgres://u:p@db/betterdesk"} {
		moved, err := MoveAsideRustDeskDatabase(path, time.Now())
		if err != nil || moved != "" {
			t.Errorf("%s: moved to %q, %v; want it left alone", filepath.Base(path), moved, err)
		}
	}
	for _, path := range []string{server, upgraded, console, empty} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s is gone: %v", filepath.Base(path), err)
		}
	}
}
