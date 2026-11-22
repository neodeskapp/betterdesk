package db

import (
	"testing"
	"time"
)

// TestBlocklistEntriesAreKept: entries are stored once per kind and
// value, a second save replaces the reason, a delete removes one entry,
// and the time comes back as stored (L127).
func TestBlocklistEntriesAreKept(t *testing.T) {
	freshStores(t, func(t *testing.T, store Database) {
		at := time.Date(2026, 11, 20, 8, 30, 0, 0, time.UTC)
		for _, e := range []*BlocklistEntry{
			{Kind: "id", Value: "123456789", Reason: "revoked via panel", CreatedAt: at},
			{Kind: "ip", Value: "203.0.113.7", Reason: "admin", CreatedAt: at.Add(time.Minute)},
			{Kind: "cidr", Value: "198.51.100.0/24", Reason: "admin", CreatedAt: at.Add(2 * time.Minute)},
			{Kind: "id", Value: "123456789", Reason: "revoked via cascade", CreatedAt: at},
		} {
			if err := store.SaveBlocklistEntry(e); err != nil {
				t.Fatal(err)
			}
		}
		if err := store.DeleteBlocklistEntry("ip", "203.0.113.7"); err != nil {
			t.Fatal(err)
		}
		if err := store.DeleteBlocklistEntry("ip", "no such entry"); err != nil {
			t.Fatal(err)
		}
		got, err := store.ListBlocklistEntries()
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 {
			t.Fatalf("entries %+v, want the id and the network", got)
		}
		if e := got[0]; e.Kind != "id" || e.Value != "123456789" || e.Reason != "revoked via cascade" || !e.CreatedAt.Equal(at) {
			t.Errorf("first entry %+v", e)
		}
		if e := got[1]; e.Kind != "cidr" || e.Value != "198.51.100.0/24" || !e.CreatedAt.Equal(at.Add(2*time.Minute)) {
			t.Errorf("second entry %+v", e)
		}
	})
}
