package db

import "testing"

// TestUpsertPeerKeepsTypeLinkAndName: a device saved through UpsertPeer
// keeps its type, its linked device and its display name on both databases;
// a later save without them leaves them as they are, one with new values
// replaces them (BUGS RV74).
func TestUpsertPeerKeepsTypeLinkAndName(t *testing.T) {
	freshStores(t, func(t *testing.T, database Database) {
		check := func(when, deviceType, linked, name string) {
			t.Helper()
			p, err := database.GetPeer("RV74AGENT")
			if err != nil || p == nil {
				t.Fatalf("%s: GetPeer: %v %v", when, p, err)
			}
			if p.DeviceType != deviceType || p.LinkedPeerID != linked || p.DisplayName != name {
				t.Errorf("%s: type %q link %q name %q, want %q %q %q", when, p.DeviceType, p.LinkedPeerID, p.DisplayName, deviceType, linked, name)
			}
		}
		if err := database.UpsertPeer(&Peer{ID: "RV74MAIN", Status: "ONLINE"}); err != nil {
			t.Fatal(err)
		}
		if err := database.UpsertPeer(&Peer{ID: "RV74AGENT", Status: "ONLINE", DeviceType: "os_agent", LinkedPeerID: "RV74MAIN", DisplayName: "Front desk"}); err != nil {
			t.Fatal(err)
		}
		check("new", "os_agent", "RV74MAIN", "Front desk")
		linked, err := database.GetLinkedPeers("RV74MAIN")
		if err != nil || len(linked) != 1 || linked[0].ID != "RV74AGENT" {
			t.Errorf("linked peers: %+v %v", linked, err)
		}

		if err := database.UpsertPeer(&Peer{ID: "RV74AGENT", Status: "OFFLINE"}); err != nil {
			t.Fatal(err)
		}
		check("saved without them", "os_agent", "RV74MAIN", "Front desk")

		if err := database.UpsertPeer(&Peer{ID: "RV74AGENT", Status: "ONLINE", DeviceType: "mesh_agent", LinkedPeerID: "RV74OTHER", DisplayName: "Back office"}); err != nil {
			t.Fatal(err)
		}
		check("saved with new values", "mesh_agent", "RV74OTHER", "Back office")
	})
}
