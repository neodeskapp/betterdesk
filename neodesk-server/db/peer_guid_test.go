package db

import (
	"strings"
	"testing"
)

// TestChangePeerIDKeepsGUID: a peer whose id changes keeps its guid, so
// what is assigned to the guid (a strategy) stays with it, also for a
// device whose uuid is not UUID-shaped and so cannot give the same guid
// again (BUGS D16).
func TestChangePeerIDKeepsGUID(t *testing.T) {
	panelGroupStores(t, func(t *testing.T, database Database) {
		// The PostgreSQL test database is shared: ids of this run only,
		// removed afterwards
		suffix := strings.ToUpper(strings.ReplaceAll(strings.TrimPrefix(t.Name(), "TestChangePeerIDKeepsGUID/"), "/", ""))
		oldID, newID := "D16OLD"+suffix, "D16NEW"+suffix
		clean := func() {
			_ = database.HardDeletePeer(oldID)
			_ = database.HardDeletePeer(newID)
		}
		clean()
		t.Cleanup(clean)
		if err := database.UpsertPeer(&Peer{ID: oldID, UUID: "not-a-uuid-" + suffix, Status: "OFFLINE"}); err != nil {
			t.Fatal(err)
		}
		guid, err := database.EnsurePeerGUID(oldID)
		if err != nil || guid == "" {
			t.Fatalf("EnsurePeerGUID: %q %v", guid, err)
		}
		if err := database.ChangePeerID(oldID, newID, "test"); err != nil {
			t.Fatal(err)
		}
		if got, err := database.GetPeerIDByGUID(guid); err != nil || got != newID {
			t.Errorf("peer of guid %s after the change: %q %v, want %s", guid, got, err, newID)
		}
		if got, err := database.EnsurePeerGUID(newID); err != nil || got != guid {
			t.Errorf("guid of %s: %q %v, want %s", newID, got, err, guid)
		}
	})
}
