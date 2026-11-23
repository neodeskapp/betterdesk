package devicegroups_test

import (
	"context"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/devicegroups"
	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// TestMembers stores members of groups and reads them back: the groups of
// many devices in one go are the groups of each device, ordered by name;
// a device in no group is not in the answer; an unknown group does
// nothing; adding twice keeps one member.
func TestMembers(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		s := newService(t, db)
		ctx := context.Background()
		for _, name := range []string{"Office", "Lab"} {
			if _, err := s.CreateDeviceGroup(ctx, devicegroups.GroupData{GUID: jsbind.String("g-" + name), Name: jsbind.String(name)}); err != nil {
				t.Fatal(err)
			}
		}
		for _, m := range [][2]string{{"g-Office", "100"}, {"g-Lab", "100"}, {"g-Lab", "200"}, {"g-Lab", "200"}, {"no-such", "300"}} {
			if err := s.AddDeviceToGroup(ctx, m[0], m[1]); err != nil {
				t.Fatalf("add %v: %v", m, err)
			}
		}
		ids := []string{"100", "200", "300"}
		batch, err := s.DeviceGroupsForPeers(ctx, ids)
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range ids {
			single, err := s.DeviceGroupsForPeer(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := string(mustJSON(t, batch[id])), string(mustJSON(t, single)); (len(single) > 0 || batch[id] != nil) && got != want {
				t.Errorf("groups of %s: batch %s, one by one %s", id, got, want)
			}
		}
		names := func(id string) []string {
			var out []string
			for _, g := range batch[id] {
				out = append(out, devicegroups.Plain(g.Get("name")).(string))
			}
			return out
		}
		if got := names("100"); len(got) != 2 || got[0] != "Lab" || got[1] != "Office" {
			t.Errorf("groups of 100: %v, want Lab, Office", got)
		}
		if got := names("200"); len(got) != 1 {
			t.Errorf("groups of 200 after adding twice: %v", got)
		}
		if _, ok := batch["300"]; ok {
			t.Error("300 is in no group and must not be in the answer")
		}
		if only, _ := s.DeviceGroupsForPeers(ctx, []string{"200"}); len(only) != 1 || only["100"] != nil {
			t.Errorf("only the devices asked for: %v", only)
		}
		if none, _ := s.DeviceGroupsForPeers(ctx, nil); len(none) != 0 {
			t.Errorf("no devices: %v", none)
		}
		if err := s.RemoveDeviceFromGroup(ctx, "g-Lab", "100"); err != nil {
			t.Fatal(err)
		}
		if err := s.RemoveDeviceFromGroup(ctx, "no-such", "100"); err != nil {
			t.Fatal(err)
		}
		after, _ := s.DeviceGroupsForPeer(ctx, "100")
		if len(after) != 1 || devicegroups.Plain(after[0].Get("name")) != "Office" {
			t.Errorf("groups of 100 after removing from Lab: %s", mustJSON(t, after))
		}
	})
}
