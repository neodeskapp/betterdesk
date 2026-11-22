package db

import (
	"testing"
	"time"
)

// TestListPeersForOrg: the device list of an organization works on both
// databases, although peers and org_devices both have a tags column; the
// device's own tags come back (BUGS RV73).
func TestListPeersForOrg(t *testing.T) {
	freshStores(t, func(t *testing.T, database Database) {
		if err := database.CreateOrganization(&Organization{ID: "rv73-org", Name: "RV73", Slug: "rv73", Settings: "{}", CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
		if err := database.UpsertPeer(&Peer{ID: "RV73PEER", Status: "ONLINE", Tags: "peer-tag"}); err != nil {
			t.Fatal(err)
		}
		if err := database.AssignDeviceToOrg(&OrgDevice{OrgID: "rv73-org", DeviceID: "RV73PEER", Tags: "org-tag"}); err != nil {
			t.Fatal(err)
		}

		peers, err := database.ListPeersForOrg("rv73-org", false)
		if err != nil {
			t.Fatalf("ListPeersForOrg: %v", err)
		}
		if len(peers) != 1 || peers[0].ID != "RV73PEER" || peers[0].Tags != "peer-tag" {
			t.Errorf("ListPeersForOrg: %+v", peers)
		}
		page, total, err := database.ListPeersForOrgPaginated("rv73-org", false, 10, 0)
		if err != nil {
			t.Fatalf("ListPeersForOrgPaginated: %v", err)
		}
		if total != 1 || len(page) != 1 || page[0].ID != "RV73PEER" || page[0].Tags != "peer-tag" {
			t.Errorf("ListPeersForOrgPaginated: total %d, %+v", total, page)
		}
	})
}
