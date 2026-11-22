package security

import (
	"errors"
	"testing"

	"github.com/unitronix/betterdesk-server/db"
)

// memStore is a Store in memory, keyed by kind and value.
type memStore struct {
	rows map[[2]string]db.BlocklistEntry
	fail error
}

func (m *memStore) SaveBlocklistEntry(e *db.BlocklistEntry) error {
	if m.fail != nil {
		return m.fail
	}
	m.rows[[2]string{e.Kind, e.Value}] = *e
	return nil
}

func (m *memStore) DeleteBlocklistEntry(kind, value string) error {
	delete(m.rows, [2]string{kind, value})
	return nil
}

func (m *memStore) ListBlocklistEntries() ([]*db.BlocklistEntry, error) {
	var out []*db.BlocklistEntry
	for _, e := range m.rows {
		out = append(out, &e)
	}
	return out, nil
}

// TestRuntimeEntriesSurviveARestart: what is blocked at run time is in the
// store, a blocklist made at the next start has it again, an unblock takes
// it out of the store, and a block the store refuses is no block (L127).
func TestRuntimeEntriesSurviveARestart(t *testing.T) {
	store := &memStore{rows: map[[2]string]db.BlocklistEntry{}}
	first := NewBlocklist()
	if err := first.UseStore(store); err != nil {
		t.Fatal(err)
	}
	if err := first.BlockID("123456789", "revoked via panel"); err != nil {
		t.Fatal(err)
	}
	if err := first.BlockIP("::ffff:203.0.113.7", "admin"); err != nil {
		t.Fatal(err)
	}
	if err := first.BlockCIDR("198.51.100.0/24", "admin"); err != nil {
		t.Fatal(err)
	}
	// The same address in its other spelling replaces the entry
	if err := first.BlockIP("203.0.113.7", "admin again"); err != nil {
		t.Fatal(err)
	}
	if len(store.rows) != 3 {
		t.Fatalf("stored %v, want three entries", store.rows)
	}

	restarted := NewBlocklist()
	if err := restarted.UseStore(store); err != nil {
		t.Fatal(err)
	}
	if !restarted.IsIDBlocked("123456789") || !restarted.IsIPBlocked("203.0.113.7") || !restarted.IsIPBlocked("198.51.100.9") {
		t.Fatalf("after a restart: %+v", restarted.List())
	}
	if !restarted.UnblockID("123456789") || !restarted.UnblockIP("203.0.113.7") || !restarted.UnblockCIDR("198.51.100.0/24") {
		t.Fatal("unblock found nothing")
	}
	if len(store.rows) != 0 {
		t.Fatalf("stored after the unblocks: %v", store.rows)
	}

	store.fail = errors.New("disk full")
	if err := restarted.BlockID("987654321", "revoked via panel"); err == nil || restarted.IsIDBlocked("987654321") {
		t.Fatalf("a block the store refused: %v, blocked %v", err, restarted.IsIDBlocked("987654321"))
	}
}
