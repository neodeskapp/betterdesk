package guestaccess

import (
	"encoding/json"
	"testing"
	"time"
)

// TestListAndRevokeRemoveEndedLinks: the rows of links past their end,
// revoked or not, are deleted when the links are listed or one is taken
// back, so the table holds only links that can still be used or are
// revoked before their end (RV115).
func TestListAndRevokeRemoveEndedLinks(t *testing.T) {
	for _, via := range []string{"list", "revoke"} {
		t.Run(via, func(t *testing.T) {
			store := newStore(t)
			_, live, err := store.Create([]string{"DEV111"}, "alice", 0, 30, false, "", 0, "")
			if err != nil {
				t.Fatal(err)
			}
			_, revoked, err := store.Create([]string{"DEV111"}, "alice", 0, 30, false, "", 0, "")
			if err != nil {
				t.Fatal(err)
			}
			if ok, err := store.RevokeByID(revoked.ID, "", true); err != nil || !ok {
				t.Fatalf("revoke: %v %v", ok, err)
			}
			past := time.Now().UTC().Add(-time.Minute)
			ended := map[string]Grant{
				"gal_ended": {ID: "ended", PeerIDs: []string{"DEV111"}, ExpiresAt: past},
				"gal_ended_revoked": {ID: "ended-revoked", PeerIDs: []string{"DEV111"}, ExpiresAt: past,
					RevokedAt: &past},
			}
			for key, g := range ended {
				b, _ := json.Marshal(g)
				if err := store.DB.SetConfig(key, string(b)); err != nil {
					t.Fatal(err)
				}
			}
			switch via {
			case "list":
				list, err := store.ListActive("", true)
				if err != nil || len(list) != 1 || list[0].ID != live.ID {
					t.Fatalf("list %v %v", list, err)
				}
			case "revoke":
				if ok, err := store.RevokeByID("unknown", "", true); err != nil || ok {
					t.Fatalf("revoke unknown: %v %v", ok, err)
				}
			}
			for key := range ended {
				if raw, _ := store.DB.GetConfig(key); raw != "" {
					t.Errorf("%s is still stored", key)
				}
			}
			entries, err := store.DB.ListConfigByPrefix(configPrefix)
			if err != nil || len(entries) != 2 {
				t.Errorf("%d rows left, want the live and the revoked link: %v", len(entries), err)
			}
		})
	}
}
