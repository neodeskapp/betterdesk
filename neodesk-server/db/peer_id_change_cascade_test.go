package db

import (
	"strings"
	"testing"
)

// TestChangePeerIDMovesAccessAndGroups: a device that changes its id keeps
// its unattended access policy, its direct grants, its device groups and
// its folder on both databases; nothing stays under the old id. Rows a
// device that once had the new id left behind give way to the moving
// device's (BUGS RV76).
func TestChangePeerIDMovesAccessAndGroups(t *testing.T) {
	freshStores(t, func(t *testing.T, database Database) {
		store := database.(PanelGroupStore)
		const oldID, newID = "RV76OLD", "RV76NEW"
		if err := database.UpsertPeer(&Peer{ID: oldID, Status: "ONLINE"}); err != nil {
			t.Fatal(err)
		}
		group := &DeviceGroup{Name: "RV76 group", SourceType: "manual"}
		if err := database.CreateDeviceGroup(group); err != nil {
			t.Fatal(err)
		}
		if err := store.SetDeviceGroupPeers(group.GUID, []string{oldID, newID}); err != nil {
			t.Fatal(err)
		}
		folder := &Folder{Name: "RV76 folder"}
		if err := store.CreatePanelFolder(folder); err != nil {
			t.Fatal(err)
		}
		other := &Folder{Name: "RV76 other folder"}
		if err := store.CreatePanelFolder(other); err != nil {
			t.Fatal(err)
		}
		if err := store.SetDevicePanelFolder(oldID, folder.ID); err != nil {
			t.Fatal(err)
		}
		if err := store.SetDevicePanelFolder(newID, other.ID); err != nil {
			t.Fatal(err)
		}
		user := &User{Username: "rv76user", PasswordHash: "x", Role: "viewer"}
		if err := database.CreateUser(user); err != nil {
			t.Fatal(err)
		}
		if err := store.SetUserPeerGrants(user.ID, []string{oldID, newID}); err != nil {
			t.Fatal(err)
		}
		for _, p := range []*AccessPolicy{
			{PeerID: oldID, UnattendedEnabled: true, AllowedOperators: "moving"},
			{PeerID: newID, UnattendedEnabled: false, AllowedOperators: "stale"},
		} {
			if err := database.SaveAccessPolicy(p); err != nil {
				t.Fatal(err)
			}
		}

		if err := database.ChangePeerID(oldID, newID, "client"); err != nil {
			t.Fatalf("ChangePeerID: %v", err)
		}

		if members, err := store.ListDeviceGroupPeerIDs(group.GUID); err != nil || strings.Join(members, ",") != newID {
			t.Errorf("group members %q, %v; want %s", members, err, newID)
		}
		folders, err := store.ListPanelDeviceFolderAssignments()
		if err != nil {
			t.Fatal(err)
		}
		if _, left := folders[oldID]; left || folders[newID] != folder.ID {
			t.Errorf("folder assignments %v, want %s in folder %d", folders, newID, folder.ID)
		}
		if grants, err := store.ListPanelUserPeerGrants(user.ID); err != nil || strings.Join(grants, ",") != newID {
			t.Errorf("grants %q, %v; want %s", grants, err, newID)
		}
		moved, err := database.GetAccessPolicy(newID)
		if err != nil || moved == nil || !moved.UnattendedEnabled || moved.AllowedOperators != "moving" {
			t.Errorf("policy under the new id: %+v %v", moved, err)
		}
		// GetAccessPolicy answers a missing policy with the driver's no-rows error
		if left, _ := database.GetAccessPolicy(oldID); left != nil {
			t.Errorf("policy under the old id: %+v", left)
		}
	})
}

// TestChangePeerIDBackDropsStalePending: a device changed from A to B
// that enrolled under A meanwhile left pending_device_A; when the console
// changes it back to A, that entry goes, so the approved device takes
// sessions and leaves the pending list. Other pending entries stay (L241).
func TestChangePeerIDBackDropsStalePending(t *testing.T) {
	freshStores(t, func(t *testing.T, database Database) {
		const idA, idB, other = "L241AAAA", "L241BBBB", "L241OTHER"
		if err := database.UpsertPeer(&Peer{ID: idA, Status: "ONLINE"}); err != nil {
			t.Fatal(err)
		}
		if err := database.ChangePeerID(idA, idB, "panel"); err != nil {
			t.Fatalf("ChangePeerID A to B: %v", err)
		}
		for _, id := range []string{idA, other} {
			if err := database.SetConfig("pending_device_"+id, `{"device_id":"`+id+`"}`); err != nil {
				t.Fatal(err)
			}
		}
		if err := database.ChangePeerID(idB, idA, "panel"); err != nil {
			t.Fatalf("ChangePeerID B to A: %v", err)
		}
		if v, err := database.GetConfig("pending_device_" + idA); err != nil || v != "" {
			t.Fatalf("pending_device_%s = %q, %v after the change back", idA, v, err)
		}
		if v, err := database.GetConfig("pending_device_" + other); err != nil || v == "" {
			t.Fatalf("pending_device_%s = %q, %v, want it kept", other, v, err)
		}
	})
}
