package api

import (
	"reflect"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/config"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
	"github.com/neodeskapp/neodesk/neodesk-server/peer"
)

// groupedPanelStore is a panel store whose devices are in device groups.
type groupedPanelStore struct {
	mockPanelACLStore
	deviceGroups map[string][]string
}

func (g *groupedPanelStore) ListDeviceGroupGUIDsForPeer(peerID string) ([]string, error) {
	return g.deviceGroups[peerID], nil
}

// TestAccessRulesReachGroupsAndUsers: an access rule applies to the
// devices it names, to the devices of the users it names, to the devices in
// the device groups it is assigned to, and to the devices its Who and where
// fields name (a device group, a user group of the device's user). A rule
// for the device wins over one for its user, which wins over one for a
// group, as the sheet says (L414).
func TestAccessRulesReachGroupsAndUsers(t *testing.T) {
	database := testSetupDB(t)
	t.Cleanup(func() { database.Close() })
	users := map[string]*db.User{}
	for _, name := range []string{"alice", "bob"} {
		u := &db.User{Username: name, PasswordHash: "x", Role: "operator"}
		if err := database.CreateUser(u); err != nil {
			t.Fatal(err)
		}
		users[name] = u
	}
	for _, p := range []*db.Peer{
		{ID: "PEERAAA1", User: "alice"},
		{ID: "PEERBBB2"},
		{ID: "PEERCCC3", User: "bob"},
	} {
		if err := database.UpsertPeer(p); err != nil {
			t.Fatal(err)
		}
	}
	rule := func(guid, permissions, userGroup, deviceGroup string) {
		t.Helper()
		if err := database.CreateStrategy(&db.Strategy{GUID: guid, Name: guid, Enabled: true, Permissions: permissions,
			UserGroupGUID: userGroup, DeviceGroupGUID: deviceGroup}); err != nil {
			t.Fatal(err)
		}
	}
	rule("s-office", `{"enable-file-transfer":"N","enable-camera":"N"}`, "", "")
	rule("s-alice", `{"enable-file-transfer":"N","disable-clipboard":"Y"}`, "", "")
	rule("s-peer", `{"enable-file-transfer":"Y"}`, "", "")
	rule("s-lab", `{"enable-audio":"N"}`, "", "dg-lab")
	rule("s-ops", `{"enable-tunnel":"N"}`, "ug-ops", "")
	assign := func(guid string, peers, users, groups []string) {
		t.Helper()
		if err := database.AssignStrategy(guid, peers, users, groups); err != nil {
			t.Fatal(err)
		}
	}
	peerKey := func(id string) string {
		key, err := database.ResolvePeerAssignmentKey(id)
		if err != nil {
			t.Fatal(err)
		}
		return key
	}
	userKey, err := database.ResolveUserAssignmentKey("alice")
	if err != nil {
		t.Fatal(err)
	}
	assign("s-office", nil, nil, []string{"dg-office"})
	assign("s-alice", nil, []string{userKey}, nil)
	assign("s-peer", []string{peerKey("PEERAAA1")}, nil, nil)

	srv := New(config.DefaultConfig(), database, peer.NewMap(), nil, "test")
	srv.SetPanelStore(&groupedPanelStore{
		mockPanelACLStore: mockPanelACLStore{
			userIDs:    map[string]int64{"alice": users["alice"].ID, "bob": users["bob"].ID},
			userGroups: map[int64][]string{users["bob"].ID: {"ug-ops"}},
		},
		deviceGroups: map[string][]string{
			"PEERAAA1": {"dg-office"},
			"PEERBBB2": {"dg-office", "dg-lab"},
		},
	})
	for _, c := range []struct {
		device string
		want   map[string]string
	}{
		// the device's own rule over its user's over its group's
		{"PEERAAA1", map[string]string{"enable-file-transfer": "Y", "disable-clipboard": "Y", "enable-camera": "N"}},
		// a device group by assignment and by the rule's Where field
		{"PEERBBB2", map[string]string{"enable-file-transfer": "N", "enable-camera": "N", "enable-audio": "N"}},
		// the device's user is in the rule's user group
		{"PEERCCC3", map[string]string{"enable-tunnel": "N"}},
	} {
		if got := srv.clientStrategyConfigOptions(c.device); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s gets %v, want %v", c.device, got, c.want)
		}
	}
}
