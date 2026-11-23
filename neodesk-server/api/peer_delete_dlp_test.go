package api

import (
	"database/sql"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/config"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
	"github.com/neodeskapp/neodesk/neodesk-server/peer"
)

// TestHardDeleteTakesTheDataGuardEventsAlong: DELETE /api/peers/{id} with
// hard removes the data loss prevention events of the device and of the
// linked devices its cascade deletes; a soft delete with cascade keeps
// them, and a device the delete leaves alone keeps its own (BUGS L54).
func TestHardDeleteTakesTheDataGuardEventsAlong(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	database, err := db.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if err := database.Migrate(); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { raw.Close() })
	// The console's table, as the console creates it
	if _, err := raw.Exec(`CREATE TABLE dlp_events (id INTEGER PRIMARY KEY AUTOINCREMENT, device_id TEXT NOT NULL, event_type TEXT)`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"MAIN001", "LINKED1", "MAIN002", "LINKED2", "OTHER01"} {
		if err := database.UpsertPeer(&db.Peer{ID: id, Status: "OFFLINE"}); err != nil {
			t.Fatal(err)
		}
		if _, err := raw.Exec(`INSERT INTO dlp_events (device_id, event_type) VALUES (?, 'usb')`, id); err != nil {
			t.Fatal(err)
		}
	}
	for id, main := range map[string]string{"LINKED1": "MAIN001", "LINKED2": "MAIN002"} {
		if err := database.UpdatePeerFields(id, map[string]string{"linked_peer_id": main}); err != nil {
			t.Fatal(err)
		}
	}
	events := func() map[string]int {
		t.Helper()
		rows, err := raw.Query(`SELECT device_id, COUNT(*) FROM dlp_events GROUP BY device_id`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := map[string]int{}
		for rows.Next() {
			var id string
			var n int
			if err := rows.Scan(&id, &n); err != nil {
				t.Fatal(err)
			}
			out[id] = n
		}
		return out
	}
	srv := New(config.DefaultConfig(), database, peer.NewMap(), nil, "1.0.0-test")

	if status, body := asConsole(t, srv, http.MethodDelete, "/api/peers/MAIN002?cascade=true", ""); status != http.StatusOK {
		t.Fatalf("soft delete with cascade: %d %s", status, body)
	}
	if got := events(); len(got) != 5 {
		t.Errorf("events after a soft delete with cascade: %v, want every device's", got)
	}
	if status, body := asConsole(t, srv, http.MethodDelete, "/api/peers/MAIN001?hard=true&cascade=true", ""); status != http.StatusOK {
		t.Fatalf("hard delete with cascade: %d %s", status, body)
	}
	if got := events(); len(got) != 3 || got["MAIN001"] != 0 || got["LINKED1"] != 0 {
		t.Errorf("events after the hard delete of MAIN001 with cascade: %v, want those of MAIN002, LINKED2 and OTHER01", got)
	}
}
