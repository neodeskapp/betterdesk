//go:build nodeoracle

package authimport_test

// This test runs the Node console's own database code (web-nodejs), so it
// needs node and the console's node_modules and sits behind the build tag
// "nodeoracle". testdata/nodeoracle/README.md says how to run it.

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/authimport"
)

func readback(t *testing.T, mainPath, authPath string) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", filepath.Join("testdata", "nodeoracle", "readback.js"), mainPath, authPath)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	var data map[string]any
	if err := json.Unmarshal(out, &data); err != nil {
		t.Fatalf("readback: %v: %s", err, out)
	}
	return data
}

// copyDB copies a database file whole, with what is still in its WAL.
func copyDB(t *testing.T, from, to string) {
	t.Helper()
	handle, err := sql.Open("sqlite", "file:"+from+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()
	if _, err := handle.Exec(`VACUUM INTO ?`, to); err != nil {
		t.Fatal(err)
	}
}

func dig(m map[string]any, keys ...string) map[string]any {
	for _, k := range keys {
		m = m[k].(map[string]any)
	}
	return m
}

// TestOldConsoleReadsTheSameAfterTheImport is the rollback case: the old
// Node console, started on the main file after the import (it finds the
// marker and leaves auth.db alone), must read every account, group,
// folder, strategy, setting and audit entry as it read them from the
// split install, apart from what the import rules decide differently.
func TestOldConsoleReadsTheSameAfterTheImport(t *testing.T) {
	sdb, mainPath, authPath := splitInstall(t, serverSide)
	beforeDir := t.TempDir()
	copyDB(t, mainPath, filepath.Join(beforeDir, "db_v2.sqlite3"))
	copyDB(t, authPath, filepath.Join(beforeDir, "auth.db"))
	before := readback(t, filepath.Join(beforeDir, "db_v2.sqlite3"), filepath.Join(beforeDir, "auth.db"))

	if _, err := authimport.Run(context.Background(), sdb, options(authPath)); err != nil {
		t.Fatal(err)
	}
	afterDir := t.TempDir()
	copyDB(t, mainPath, filepath.Join(afterDir, "db_v2.sqlite3"))
	copyDB(t, authPath, filepath.Join(afterDir, "auth.db"))
	after := readback(t, filepath.Join(afterDir, "db_v2.sqlite3"), filepath.Join(afterDir, "auth.db"))

	// What the rules decide differently: the server's own accounts are
	// there too; an account in both files keeps the server's name and
	// guid; the server's address book stays where both had one; and a
	// book read from the server's table (keyed by name) carries the name.
	users := dig(after, "users")
	for _, name := range []string{"zed", "frank"} {
		if _, ok := users[name]; !ok {
			t.Errorf("the server's account %s is gone", name)
		}
		delete(users, name)
	}
	expect := []struct {
		user, key string
		before    any
	}{
		{"bob", "guid", ""},
		{"dave", "guid", "guid-dave"},
		{"bob", "personalBook", map[string]any{"ab_type": "personal", "data": `{"peers":[]}`}},
		{"alice", "legacyBook", map[string]any{"ab_type": "legacy", "data": `{"peers":[{"id":"PEER1"}]}`}},
	}
	if book := dig(after, "users", "alice", "legacyBook"); book["username"] != "alice" {
		t.Errorf("alice's book after the import: %v", book)
	}
	bu := dig(before, "users")
	bu["dave"] = bu["Dave"]
	delete(bu, "Dave")
	for _, e := range expect {
		u := bu[e.user].(map[string]any)
		if got := u[e.key]; !reflect.DeepEqual(normalize(got), normalize(e.before)) {
			t.Errorf("before the import %s.%s = %v, want %v", e.user, e.key, got, e.before)
		}
		u[e.key] = dig(after, "users", e.user)[e.key]
	}
	for _, section := range []string{"users", "userGroups", "deviceGroups", "folders", "assignments", "strategies", "settings", "audit"} {
		b, _ := json.Marshal(before[section])
		a, _ := json.Marshal(after[section])
		if string(a) != string(b) {
			t.Errorf("%s differ after the import:\n before %s\n after  %s", section, b, a)
		}
	}
}

func normalize(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
