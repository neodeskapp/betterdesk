package main

import (
	"bytes"
	"context"
	"database/sql"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/devicegroups"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
)

// Installed over an hbbs folder, the start moves RustDesk's db_v2.sqlite3
// aside and says where it went, so the console's device table is its own
// and its copy of the server's devices works (L223). Before, the console's
// copy ran into hbbs's peer table (no column ip, then NOT NULL on guid).
func TestStartMovesRustDeskDatabaseAside(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db_v2.sqlite3")
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`create table peer (guid blob primary key not null, id varchar(100) not null,
		uuid blob not null, pk blob not null, created_at datetime not null default(current_timestamp),
		user blob, status tinyint, note varchar(300), info text not null) without rowid;
		insert into peer (guid, id, uuid, pk, info) values (x'01', 'HBBS1', x'02', x'03', '{}')`); err != nil {
		t.Fatal(err)
	}
	conn.Close()

	var out bytes.Buffer
	log.SetOutput(&out)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	moveAsideRustDeskDatabase(path)
	logged := out.String()
	if !strings.Contains(logged, "WARN:") || !strings.Contains(logged, path+".rustdesk-") {
		t.Errorf("the start did not say where RustDesk's database went: %q", logged)
	}

	server, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	if err := server.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := server.UpsertPeer(&db.Peer{ID: "GO1", UUID: "u-go1", IP: "192.0.2.7", Status: "ONLINE"}); err != nil {
		t.Fatal(err)
	}
	console, err := store.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer console.Close()
	ctx := context.Background()
	if err := console.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	devicegroups.New(devicegroups.Options{DB: console}).SyncServerPeers(ctx)
	var ip string
	if err := console.QueryRow(ctx, `SELECT ip FROM peer WHERE id = 'GO1'`).Scan(&ip); err != nil || ip != "192.0.2.7" {
		t.Errorf("the console's copy of the server's device: ip %q, %v", ip, err)
	}
}

// A start error reaches the log at every LOG_LEVEL: the start's code ends
// with logging.Fatal and logging.Fatalf, whose lines say FATAL, never with
// log.Fatal, whose lines the level filter dropped (L223).
func TestStartErrorsUseTheFatalOfLogging(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(src), "\n") {
			if strings.Contains(line, "log.Fatal") && !strings.Contains(line, "logging.Fatal") {
				t.Errorf("%s:%d calls log.Fatal: %s", file, i+1, strings.TrimSpace(line))
			}
		}
	}
}
