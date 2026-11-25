package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/audit"
)

// unprovedEntries are the audit entries of refused joins under a known ID.
func unprovedEntries(srv *Server) []audit.Event {
	return srv.auditLog.RecentByAction(actionJoinUnproved, 100)
}

// TestARefusedJoinUnderAKnownIDIsAudited: a join under an enrolled ID that
// does not prove its key is what an admin needs to see. The entry names the
// device, the address and why: no proof, a bad proof, a replayed proof, an
// empty key or uuid.
func TestARefusedJoinUnderAKnownIDIsAudited(t *testing.T) {
	onboardStores(t, func(t *testing.T, srv *Server) {
		auditLog := audit.NewLogger("")
		t.Cleanup(func() { auditLog.Close() })
		srv.SetAuditLogger(auditLog)
		now := time.Now()
		srv.now = func() time.Time { return now }

		key := newJoinKey(t)
		enrolledLabComputer(t, srv, key)
		stranger := newJoinKey(t)
		code := makeOnboardCode(t, srv, map[string]any{"kind": "join", "max_uses": 2}).Code
		good := proofOf(key, "289485454", joinPath, "replay", now)
		if status, out := joinAs(t, srv, code, map[string]any{"uuid": "machine-1", "public_key": key.b64, "proof": good}); status != http.StatusOK {
			t.Fatalf("the computer's own join: %d %+v", status, out)
		}
		if n := len(unprovedEntries(srv)); n != 0 {
			t.Fatalf("a proved join was audited as unproved: %d", n)
		}

		for i, tc := range []struct {
			reason string
			device map[string]any
		}{
			{"no_proof", map[string]any{"uuid": "machine-1", "public_key": key.b64}},
			{"bad_proof", map[string]any{"uuid": "machine-1", "public_key": key.b64,
				"proof": proofOf(joinKey{b64: key.b64, private: stranger.private}, "289485454", joinPath, "bad", now)}},
			{"replayed", map[string]any{"uuid": "machine-1", "public_key": key.b64, "proof": good}},
			{"empty_key", map[string]any{"uuid": "machine-1"}},
			{"empty_uuid", map[string]any{"public_key": key.b64}},
		} {
			// One device gets an entry a window at most
			now = now.Add(auditUnprovedEvery + time.Second)
			if status, out := joinAs(t, srv, code, tc.device); status != http.StatusForbidden {
				t.Fatalf("%s: %d %+v", tc.reason, status, out)
			}
			entries := unprovedEntries(srv)
			if len(entries) != i+1 {
				t.Fatalf("%s: %d entries, want %d", tc.reason, len(entries), i+1)
			}
			e := entries[0]
			if e.Target != "289485454" || e.Details["reason"] != tc.reason || e.Details["ip"] == "" {
				t.Errorf("%s: entry %+v", tc.reason, e)
			}
		}
	})
}

// TestALoopOfRefusedJoinsCannotFloodTheAudit: one entry per device and
// window; the next entry says how many refusals it stands for.
func TestALoopOfRefusedJoinsCannotFloodTheAudit(t *testing.T) {
	onboardStores(t, func(t *testing.T, srv *Server) {
		auditLog := audit.NewLogger("")
		t.Cleanup(func() { auditLog.Close() })
		srv.SetAuditLogger(auditLog)
		now := time.Now()
		srv.now = func() time.Time { return now }
		key := newJoinKey(t)
		enrolledLabComputer(t, srv, key)
		code := makeOnboardCode(t, srv, map[string]any{"kind": "join"}).Code
		for i := 0; i < 50; i++ {
			joinAs(t, srv, code, map[string]any{"uuid": "machine-1", "public_key": key.b64})
		}
		if n := len(unprovedEntries(srv)); n != 1 {
			t.Fatalf("50 refusals in a loop made %d entries, want 1", n)
		}
		now = now.Add(auditUnprovedEvery + time.Second)
		joinAs(t, srv, code, map[string]any{"uuid": "machine-1", "public_key": key.b64})
		entries := unprovedEntries(srv)
		if len(entries) != 2 || entries[0].Details["repeats"] != "49" {
			t.Fatalf("after the window: %+v", entries)
		}
	})
}
