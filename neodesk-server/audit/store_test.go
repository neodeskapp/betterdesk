package audit

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/db"
)

func storeForTest(t *testing.T) db.Database {
	t.Helper()
	database, err := db.OpenSQLite(filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

// TestEventsOutliveTheLogger: with a store, what was logged is read back
// at once, paged and counted by action, and a logger made at the next
// start reads the same events (L91, L126).
func TestEventsOutliveTheLogger(t *testing.T) {
	database := storeForTest(t)
	first := NewLogger("")
	first.UseStore(database)
	first.Log(ActionAuthLoginFailed, "203.0.113.7", "alice", map[string]string{"reason": "wrong password"})
	first.Log(ActionPeerBanned, "alice", "123456789", map[string]string{"reason": "spam"})
	first.Log(ActionAuthLoginFailed, "203.0.113.8", "bob", nil)

	events, total, err := first.Page(ActionAuthLoginFailed, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(events) != 1 || events[0].Target != "alice" || events[0].Details["reason"] != "wrong password" {
		t.Fatalf("page 2 of the failed sign-ins: %+v of %d", events, total)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	restarted := NewLogger("")
	restarted.UseStore(database)
	defer restarted.Close()
	if got := restarted.Total(); got != 3 {
		t.Fatalf("after a restart: %d events, want 3", got)
	}
	recent := restarted.Recent(10)
	if len(recent) != 3 || recent[0].Target != "bob" || recent[1].Actor != "alice" || recent[2].Action != ActionAuthLoginFailed {
		t.Fatalf("after a restart: %+v", recent)
	}
	if time.Since(recent[0].Timestamp) > time.Minute {
		t.Errorf("timestamp %v", recent[0].Timestamp)
	}
}

// TestWriterPrunesOldEvents: the writer deletes events older than
// RetentionDays when it starts.
func TestWriterPrunesOldEvents(t *testing.T) {
	database := storeForTest(t)
	old := time.Now().AddDate(0, 0, -RetentionDays-1)
	young := time.Now().AddDate(0, 0, -RetentionDays+1)
	if err := database.AddServerEvents([]*db.ServerEvent{
		{Time: old, Action: string(ActionConfigChanged), Actor: "alice", Target: "old"},
		{Time: young, Action: string(ActionConfigChanged), Actor: "alice", Target: "young"},
	}); err != nil {
		t.Fatal(err)
	}
	l := NewLogger("")
	l.UseStore(database)
	defer l.Close()
	events := l.Recent(10)
	if len(events) != 1 || events[0].Target != "young" {
		t.Fatalf("after the start: %+v", events)
	}
}
