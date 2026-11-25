package chatstore

import (
	"context"
	"testing"
)

// helpRequest adds a help request of a device as the API server stores
// one, with the table made when the database has none yet.
func helpRequest(t *testing.T, s *Store, device, status string) int64 {
	t.Helper()
	ctx := context.Background()
	if _, err := s.db.Exec(ctx, s.db.Pick(
		`CREATE TABLE IF NOT EXISTS help_requests (id INTEGER PRIMARY KEY AUTOINCREMENT, device_id TEXT NOT NULL, status TEXT DEFAULT 'pending')`,
		`CREATE TABLE IF NOT EXISTS help_requests (id BIGSERIAL PRIMARY KEY, device_id TEXT NOT NULL, status TEXT DEFAULT 'pending')`)); err != nil {
		t.Fatal(err)
	}
	return must(s.db.Insert(ctx, `INSERT INTO help_requests (device_id, status) VALUES (?, ?)`, device, status))
}

// TestTheComputersHelpRequestIsLinked: the computer's first message links
// its open help request to its conversation (A242, the lead's call on
// question 4); a resolved one or another computer's is never linked, and a
// later request replaces one that was resolved meanwhile.
func TestTheComputersHelpRequestIsLinked(t *testing.T) {
	each(t, func(t *testing.T, s *Store, _ *clock) {
		ctx := context.Background()
		helpRequest(t, s, "dev1", "resolved")
		open := helpRequest(t, s, "dev1", "pending")
		helpRequest(t, s, "dev2", "pending")

		post(t, s, "dev1", dev1, "my printer stopped")
		if c := must(s.Conversation(ctx, "dev1")); c.HelpRequestID != open {
			t.Errorf("after the first message: help request %d, want the open one %d", c.HelpRequestID, open)
		}
		// The console's answer and a message without a request change nothing
		post(t, s, "dev1", anna, "looking at it")
		post(t, s, "dev3", Device("dev3", "hall"), "hello")
		if c := must(s.Conversation(ctx, "dev3")); c.HelpRequestID != 0 {
			t.Errorf("a computer without a request: linked to %d", c.HelpRequestID)
		}

		// Resolved meanwhile: the next request is linked on the next message
		if _, err := s.db.Exec(ctx, `UPDATE help_requests SET status = 'resolved' WHERE id = ?`, open); err != nil {
			t.Fatal(err)
		}
		next := helpRequest(t, s, "dev1", "acknowledged")
		post(t, s, "dev1", dev1, "it broke again")
		if c := must(s.Conversation(ctx, "dev1")); c.HelpRequestID != next {
			t.Errorf("after a new request: help request %d, want %d", c.HelpRequestID, next)
		}
	})
}

// TestLinkingNeedsNoHelpRequestTable: a database without the API server's
// help requests (the console's own, in tests) still stores messages.
func TestLinkingNeedsNoHelpRequestTable(t *testing.T) {
	each(t, func(t *testing.T, s *Store, _ *clock) {
		if _, err := s.db.Exec(context.Background(), `DROP TABLE IF EXISTS help_requests`); err != nil {
			t.Fatal(err)
		}
		post(t, s, "dev1", dev1, "hello")
		if c := must(s.Conversation(context.Background(), "dev1")); c.HelpRequestID != 0 || c.LastMessageID == 0 {
			t.Errorf("conversation: %+v", c)
		}
	})
}
