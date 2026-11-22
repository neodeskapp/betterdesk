package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// markStack writes the test stack's marker, as the stack does before it
// seeds.
func markStack(t *testing.T, db *store.DB) {
	t.Helper()
	if _, err := db.Exec(context.Background(), `INSERT INTO server_config (key, value) VALUES (?, ?)`, markerKey, markerValue); err != nil {
		t.Fatal(err)
	}
}

// commandsOn are the commands that write into a database, each with the
// arguments that make it write into the one at dsn. dir is a folder for
// the files they write.
func commandsOn(t *testing.T, dsn, dir string) map[string][]string {
	t.Helper()
	passwordFile := filepath.Join(dir, "e2e_password")
	if err := os.WriteFile(passwordFile, []byte("E2e-Secret-1!"), 0o600); err != nil {
		t.Fatal(err)
	}
	return map[string][]string{
		"demo": {"demo", "--go-db", dsn, "--auth-db", dsn, "--admin-password", "x", "--panel", "http://127.0.0.1:1",
			"--online-file", filepath.Join(dir, "online.json"), "--now", "1795083630456"},
		"many-devices":         {"many-devices", "--go-db", dsn, "--count", "5"},
		"many-devices remove":  {"many-devices", "--go-db", dsn, "--remove"},
		"accounts":             {"accounts", "--go-db", dsn, passwordFile},
		"accounts tokens only": {"accounts", "--device-tokens-only", "--go-db", dsn, passwordFile},
	}
}

// written counts what the commands write: devices, accounts, tokens and
// the files of the folder besides the password.
func written(t *testing.T, db *store.DB, dir string) (peers, users, tokens, files int) {
	t.Helper()
	ctx := context.Background()
	for table, n := range map[string]*int{"peers": &peers, "users": &users, "access_tokens": &tokens} {
		if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM `+table).Scan(n); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return peers, users, tokens, len(entries) - 1
}

// TestTheCommandsRefuseADatabaseWithoutTheMarker: demo, many-devices and
// accounts refuse a database without the test stack's marker, or with
// another value, before they write anything, and say what is missing.
// NODE_ENV is not set, so nothing else stops them (BUGS RV87).
func TestTheCommandsRefuseADatabaseWithoutTheMarker(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		storetest.Prepare(t, db, storetest.GoServerFirst)
		ctx := context.Background()
		// The owner of the device tokens, and a perf device for --remove
		if _, err := db.Exec(ctx, `INSERT INTO users (username, password_hash, role) VALUES ('admin', 'hash', 'admin')`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, `INSERT INTO peers (id, hostname) VALUES ('950000001', 'perf-lab-00001')`); err != nil {
			t.Fatal(err)
		}
		for _, value := range []string{"", "0"} {
			if value != "" {
				if _, err := db.Exec(ctx, `INSERT INTO server_config (key, value) VALUES (?, ?)`, markerKey, value); err != nil {
					t.Fatal(err)
				}
			}
			dir := t.TempDir()
			for name, args := range commandsOn(t, testDSN(t, db), dir) {
				err := run(ctx, args, func(string) string { return "" }, io.Discard, io.Discard)
				if err == nil || !strings.Contains(err.Error(), `refusing to seed`) || !strings.Contains(err.Error(), `server_config key "devseed_test_stack" with the value "1"`) {
					t.Errorf("%s with the marker %q: %v", name, value, err)
				}
			}
			if peers, users, tokens, files := written(t, db, dir); peers != 1 || users != 1 || tokens != 0 || files != 0 {
				t.Errorf("with the marker %q the commands wrote: %d devices, %d accounts, %d tokens, %d files", value, peers, users, tokens, files)
			}
		}
	})
}

// TestTheCommandsRefuseADatabaseWithoutServerConfig: a database the
// signal server never set up has no server_config, and so no marker.
func TestTheCommandsRefuseADatabaseWithoutServerConfig(t *testing.T) {
	db := storetest.SQLite(t)
	dir := t.TempDir()
	for name, args := range commandsOn(t, testDSN(t, db), dir) {
		err := run(context.Background(), args, func(string) string { return "" }, io.Discard, io.Discard)
		if err == nil || !strings.Contains(err.Error(), `cannot read the test stack marker (server_config key "devseed_test_stack")`) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// TestDemoRefusesAnAuthDBWithoutTheMarker: demo writes into a separate
// auth.db only when that one carries the marker too, and then seeds
// neither database.
func TestDemoRefusesAnAuthDBWithoutTheMarker(t *testing.T) {
	mainDB := storetest.SQLite(t)
	storetest.Prepare(t, mainDB, storetest.GoServerFirst)
	markStack(t, mainDB)
	auth := storetest.SQLite(t)
	storetest.Prepare(t, auth, storetest.ConsoleFirst)
	dir := t.TempDir()
	args := []string{"demo", "--go-db", testDSN(t, mainDB), "--auth-db", testDSN(t, auth), "--admin-password", "x",
		"--panel", "http://127.0.0.1:1", "--online-file", filepath.Join(dir, "online.json")}
	err := run(context.Background(), args, func(string) string { return "" }, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "refusing to seed "+testDSN(t, auth)) {
		t.Fatalf("demo with an auth.db without the marker: %v", err)
	}
	if peers, _, _, _ := written(t, mainDB, dir); peers != 0 {
		t.Errorf("demo wrote %d devices into the main database", peers)
	}
}

// TestNodeEnvAndForceNoLongerDecide: with the marker demo seeds although
// NODE_ENV is production (it gets as far as signing in to the console,
// which is not there), and --force is no flag any more.
func TestNodeEnvAndForceNoLongerDecide(t *testing.T) {
	db := storetest.SQLite(t)
	storetest.Prepare(t, db, storetest.GoServerFirst)
	markStack(t, db)
	dir := t.TempDir()
	args := commandsOn(t, testDSN(t, db), dir)["demo"]
	production := func(k string) string { return map[string]string{"NODE_ENV": "production"}[k] }
	err := run(context.Background(), args, production, io.Discard, io.Discard)
	if err == nil || strings.Contains(err.Error(), "refusing") || strings.Contains(err.Error(), "NODE_ENV") {
		t.Fatalf("demo with the marker and NODE_ENV=production: %v", err)
	}
	if peers, _, _, _ := written(t, db, dir); peers == 0 {
		t.Error("demo with the marker wrote no devices")
	}
	err = run(context.Background(), append(args, "--force"), production, io.Discard, io.Discard)
	if err == nil || err.Error() != "unknown flag --force" {
		t.Errorf("demo --force: %v", err)
	}
}
