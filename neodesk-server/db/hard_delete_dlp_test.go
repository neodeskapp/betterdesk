package db

import (
	"testing"
)

func dlpEventCount(t *testing.T, store Database, deviceID string) int {
	t.Helper()
	var n int
	var err error
	switch d := store.(type) {
	case *SQLiteDB:
		err = d.db.QueryRow(`SELECT COUNT(*) FROM dlp_events WHERE device_id = ?`, deviceID).Scan(&n)
	case *PostgresDB:
		err = d.pool.QueryRow(d.ctx, `SELECT COUNT(*) FROM dlp_events WHERE device_id = $1`, deviceID).Scan(&n)
	}
	if err != nil {
		t.Fatalf("count the data guard events of %s: %v", deviceID, err)
	}
	return n
}

// TestHardDeleteRemovesDataGuardEvents: a hard delete removes the device's
// data loss prevention events (the console's dlp_events), whether the
// console or a client of the API port deletes it, the single delete and
// the cascade alike, as all of them delete through HardDeletePeer. A soft
// delete keeps them, as the device can come back, and other devices keep
// theirs. A database without the console's table deletes as before
// (BUGS L54).
func TestHardDeleteRemovesDataGuardEvents(t *testing.T) {
	freshStores(t, func(t *testing.T, store Database) {
		for _, id := range []string{"DLP1", "DLP2", "DLP3"} {
			if err := store.UpsertPeer(&Peer{ID: id, Status: "ONLINE"}); err != nil {
				t.Fatal(err)
			}
		}
		if err := store.HardDeletePeer("DLP3"); err != nil {
			t.Fatalf("hard delete without the console's table: %v", err)
		}
		if p, err := store.GetPeer("DLP3"); err != nil || p != nil {
			t.Fatalf("DLP3 after its hard delete: %+v, %v", p, err)
		}

		rawExec(t, store, `CREATE TABLE dlp_events (device_id TEXT NOT NULL, event_type TEXT)`)
		for _, id := range []string{"DLP1", "DLP1", "DLP2"} {
			rawExec(t, store, `INSERT INTO dlp_events (device_id, event_type) VALUES (?, 'usb')`, id)
		}
		if err := store.DeletePeer("DLP1"); err != nil {
			t.Fatal(err)
		}
		if n := dlpEventCount(t, store, "DLP1"); n != 2 {
			t.Errorf("events of DLP1 after a soft delete: %d, want 2", n)
		}
		if err := store.HardDeletePeer("DLP1"); err != nil {
			t.Fatal(err)
		}
		if n := dlpEventCount(t, store, "DLP1"); n != 0 {
			t.Errorf("events of DLP1 after its hard delete: %d, want 0", n)
		}
		if n := dlpEventCount(t, store, "DLP2"); n != 1 {
			t.Errorf("events of DLP2 after DLP1's hard delete: %d, want 1", n)
		}
	})
}
