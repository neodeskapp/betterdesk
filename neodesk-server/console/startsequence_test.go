package console

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"testing"
)

// quiet keeps the discovery jobs off the network in these tests: the
// mDNS announcement off; the LAN responder announces nothing for a
// console on loopback, the address these tests listen on.
var quiet = map[string]string{"PANEL_MDNS": "off"}

// TestDiscoveryAndCleanUpsRun pins that the clean-ups, LAN discovery and,
// with PANEL_MDNS on, the mDNS announcement are jobs of the console.
func TestDiscoveryAndCleanUpsRun(t *testing.T) {
	w := newWorld(t, setup{env: map[string]string{"PANEL_MDNS": "on"}})
	for _, name := range []string{"integration housekeeping", "online status sync", "LAN discovery", "mDNS announcement"} {
		if !slices.Contains(w.s.Jobs().Names(), name) {
			t.Errorf("%q must run, jobs = %q", name, w.s.Jobs().Names())
		}
	}
}

// TestMDNSOnlyWhenAsked pins that the mDNS announcement is off unless
// PANEL_MDNS asks for it: the old console announced only where
// bonjour-service had been installed by hand.
func TestMDNSOnlyWhenAsked(t *testing.T) {
	for _, value := range []string{"", "off", "0", "no", "N", "FALSE"} {
		w := newWorld(t, setup{env: map[string]string{"PANEL_MDNS": value}})
		if slices.Contains(w.s.Jobs().Names(), "mDNS announcement") {
			t.Errorf("PANEL_MDNS=%q: the announcement runs, jobs = %q", value, w.s.Jobs().Names())
		}
	}
}

// legacyAuthDB writes an auth.db with one account into the data folder.
func legacyAuthDB(t *testing.T, w *world) {
	t.Helper()
	handle, err := sql.Open("sqlite", "file:"+filepath.Join(w.s.cfg.DataDir, "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()
	for _, stmt := range []string{
		`CREATE TABLE users (id INTEGER PRIMARY KEY AUTOINCREMENT, username TEXT UNIQUE NOT NULL, password_hash TEXT NOT NULL,
			role TEXT DEFAULT 'admin', email TEXT DEFAULT '')`,
		`INSERT INTO users (id, username, password_hash, role, email) VALUES (40, 'legacy_admin', '$2b$10$x', 'admin', 'old@example.com')`,
	} {
		if _, err := handle.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
}

// TestStartImportsAuthDB: the console imports the old console's auth.db
// before it listens.
func TestStartImportsAuthDB(t *testing.T) {
	w := newWorld(t, setup{env: quiet})
	if w.db.IsPostgres() {
		t.Skip("auth.db exists only next to SQLite")
	}
	legacyAuthDB(t, w)
	if err := w.s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := w.db.QueryRow(context.Background(), `SELECT COUNT(*) FROM users WHERE username = 'legacy_admin' AND email = 'old@example.com'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Error("the console did not import auth.db at start")
	}
}
