package db

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// panelGroupStores runs a test on SQLite and, when
// NEODESK_TEST_POSTGRES_DSN names one, on PostgreSQL.
func panelGroupStores(t *testing.T, test func(t *testing.T, store Database)) {
	t.Run("sqlite", func(t *testing.T) { test(t, newTestDB(t)) })
	t.Run("postgres", func(t *testing.T) {
		dsn := os.Getenv("NEODESK_TEST_POSTGRES_DSN")
		if dsn == "" {
			t.Skip("NEODESK_TEST_POSTGRES_DSN is not set")
		}
		database, err := OpenPostgres(dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { database.Close() })
		if err := database.Migrate(); err != nil {
			t.Fatal(err)
		}
		test(t, database)
	})
}

// TestSetDeviceManualGroup: the device goes into the chosen manual group
// and out of the other groups named, in one transaction. The other members
// and the groups not named stay; a chosen group that does not exist fails
// the whole write and leaves every group as it was (BUGS UM33).
func TestSetDeviceManualGroup(t *testing.T) {
	panelGroupStores(t, func(t *testing.T, database Database) {
		store := database.(PanelGroupStore)
		// The PostgreSQL test database is shared: names and ids of this
		// run only, removed afterwards
		suffix := strings.ReplaceAll(t.Name(), "/", "-")
		device, other := "UM33-DEV-"+suffix, "UM33-OTHER-"+suffix
		var groups []*DeviceGroup
		for _, name := range []string{"A", "B", "C", "Unlisted"} {
			g := &DeviceGroup{Name: "UM33 " + name + " " + suffix, SourceType: "manual"}
			if err := database.CreateDeviceGroup(g); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_ = store.SetDeviceGroupPeers(g.GUID, nil)
				_ = database.DeleteDeviceGroup(g.GUID)
			})
			groups = append(groups, g)
		}
		a, b, c, unlisted := groups[0].GUID, groups[1].GUID, groups[2].GUID, groups[3].GUID
		for guid, members := range map[string][]string{a: {device, other}, c: {other}, unlisted: {device}} {
			if err := store.SetDeviceGroupPeers(guid, members); err != nil {
				t.Fatal(err)
			}
		}
		members := func() string {
			t.Helper()
			var out []string
			for _, g := range groups {
				peers, err := store.ListDeviceGroupPeerIDs(g.GUID)
				if err != nil {
					t.Fatal(err)
				}
				for i, p := range peers {
					peers[i] = strings.TrimSuffix(strings.TrimPrefix(p, "UM33-"), "-"+suffix)
				}
				out = append(out, strings.Join(peers, ","))
			}
			return strings.Join(out, " | ")
		}
		const start = "DEV,OTHER |  | OTHER | DEV"
		if got := members(); got != start {
			t.Fatalf("members at the start: %q", got)
		}

		err := store.SetDeviceManualGroup(device, []string{a, b, c}, "no-such-group")
		if !errors.Is(err, ErrDeviceGroupNotFound) {
			t.Errorf("a chosen group that does not exist: %v, want ErrDeviceGroupNotFound", err)
		}
		if got := members(); got != start {
			t.Errorf("members after a failed write: %q, want %q", got, start)
		}

		if err := store.SetDeviceManualGroup(device, []string{a, b, c, "gone"}, b); err != nil {
			t.Fatal(err)
		}
		if got, want := members(), "OTHER | DEV | OTHER | DEV"; got != want {
			t.Errorf("members after moving the device to B: %q, want %q", got, want)
		}
		if err := store.SetDeviceManualGroup(device, []string{b, b}, b); err != nil {
			t.Fatal(err)
		}
		if got, want := members(), "OTHER | DEV | OTHER | DEV"; got != want {
			t.Errorf("members after choosing B again: %q, want %q", got, want)
		}
		if err := store.SetDeviceManualGroup(device, []string{a, b, c}, ""); err != nil {
			t.Fatal(err)
		}
		if got, want := members(), "OTHER |  | OTHER | DEV"; got != want {
			t.Errorf("members after taking the device out: %q, want %q", got, want)
		}
	})
}
