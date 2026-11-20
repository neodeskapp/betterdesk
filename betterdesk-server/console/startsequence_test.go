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

// TestDiscoveryRunsOnlyWithoutNode pins that LAN discovery and the mDNS
// announcement bind their ports only when no Node console is behind this
// one (that console binds them itself), while the clean-ups run in both.
func TestDiscoveryRunsOnlyWithoutNode(t *testing.T) {
	both := []string{"integration housekeeping", "online status sync"}
	own := []string{"LAN discovery", "mDNS announcement"}

	alone := newWorld(t, setup{env: map[string]string{"PANEL_MDNS": "on"}})
	for _, name := range append(both, own...) {
		if !slices.Contains(alone.s.Jobs().Names(), name) {
			t.Errorf("without a Node console %q must run here, jobs = %q", name, alone.s.Jobs().Names())
		}
	}
	front := newWorld(t, setup{env: quiet, node: true})
	for _, name := range both {
		if !slices.Contains(front.s.Jobs().Names(), name) {
			t.Errorf("in front of a Node console %q must run here too, jobs = %q", name, front.s.Jobs().Names())
		}
	}
	for _, name := range own {
		if slices.Contains(front.s.Jobs().Names(), name) {
			t.Errorf("with a Node console behind it %q must not run here, jobs = %q", name, front.s.Jobs().Names())
		}
	}
}

// TestMDNSOnlyWhenAsked pins that the mDNS announcement is off unless
// PANEL_MDNS asks for it: the old console announced only where
// bonjour-service had been installed by hand.
func TestMDNSOnlyWhenAsked(t *testing.T) {
	for _, value := range []string{"", "off", "0"} {
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

// TestStartImportsAuthDBOnlyWithoutNode: a Go console that serves
// everything imports the old console's auth.db before it listens; in
// front of a Node console, which still reads that file, it leaves it.
func TestStartImportsAuthDBOnlyWithoutNode(t *testing.T) {
	for _, node := range []bool{true, false} {
		w := newWorld(t, setup{env: quiet, node: node})
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
		if node && n != 0 {
			t.Error("in front of the Node console auth.db was imported")
		}
		if !node && n != 1 {
			t.Error("the Go console alone did not import auth.db at start")
		}
		w.s.Stop()
	}
}
