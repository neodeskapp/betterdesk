package devicegroups_test

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/devicegroups"
	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// The lookups of the help request mail (BUGS PA8): the folder a device is
// in, and the accounts on the access lists of that folder and of the
// device groups the device belongs to, with their mail addresses.

// eachInstall runs fn on a database the signal server set up first, so
// its device table peers is there, on SQLite and PostgreSQL.
func eachInstall(t *testing.T, fn func(t *testing.T, db *store.DB)) {
	t.Helper()
	storetest.Each(t, func(t *testing.T, db *store.DB) { fn(t, storetest.Prepare(t, db, storetest.GoServerFirst)) })
}

func userID(t *testing.T, db *store.DB, username string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(context.Background(), `SELECT id FROM users WHERE username = ?`, username).Scan(&id); err != nil {
		t.Fatalf("user %s: %v", username, err)
	}
	return id
}

func mustDo(t *testing.T, what string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

// seedOperators: dev-1 is in the folder "Zürich", whose access list names
// alice, and a stored member of the group "Desk", whose access list names
// erin and the user group "Support" (bob, carol, dave). The group "Lab"
// does not hold dev-1 and names frank. dev-2 is in no folder and no group.
// It returns the folder's id.
func seedOperators(t *testing.T, db *store.DB) int64 {
	t.Helper()
	ctx := context.Background()
	s := newService(t, db)
	for _, u := range [][2]string{
		{"alice", "alice@example.org"}, {"bob", " bob@example.org "}, {"carol", ""},
		{"dave", "bob@example.org"}, {"erin", "erin@example.org"}, {"frank", "frank@example.org"},
	} {
		exec(t, db, `INSERT INTO users (username, password_hash, role, email) VALUES (?, 'x', 'operator', ?)`, u[0], u[1])
	}
	exec(t, db, `INSERT INTO folders (name, color, icon, sort_order) VALUES ('Zürich', '#ffffff', 'folder', 0)`)
	var folder int64
	if err := db.QueryRow(ctx, `SELECT id FROM folders WHERE name = 'Zürich'`).Scan(&folder); err != nil {
		t.Fatal(err)
	}
	exec(t, db, `INSERT INTO device_folder_assignments (device_id, folder_id) VALUES ('dev-1', ?)`, folder)

	folderGroup := "folder_" + strconv.FormatInt(folder, 10)
	for guid, name := range map[string]string{folderGroup: "Zürich", "g-desk": "Desk", "g-lab": "Lab"} {
		_, err := s.CreateDeviceGroup(ctx, devicegroups.GroupData{GUID: jsbind.String(guid), Name: jsbind.String(name)})
		mustDo(t, "create group "+name, err)
	}
	_, err := s.CreateUserGroup(ctx, devicegroups.UserGroupData{GUID: jsbind.String("ug-support"), Name: jsbind.String("Support")})
	mustDo(t, "create user group", err)
	for _, name := range []string{"bob", "carol", "dave"} {
		_, err := s.SetUserGroupMemberships(ctx, userID(t, db, name), []string{"ug-support"})
		mustDo(t, "member "+name, err)
	}
	mustDo(t, "add dev-1", s.AddDeviceToGroup(ctx, "g-desk", "dev-1"))
	for guid, users := range map[string][]string{folderGroup: {"alice"}, "g-desk": {"erin"}, "g-lab": {"frank"}} {
		_, err := s.SetDeviceGroupUserAccess(ctx, guid, users)
		mustDo(t, "access list of "+guid, err)
	}
	_, err = s.SetDeviceGroupUserGroupAccess(ctx, "g-desk", []string{"ug-support"})
	mustDo(t, "user group on g-desk", err)
	return folder
}

func TestResolveFolderNameForDevice(t *testing.T) {
	eachInstall(t, func(t *testing.T, db *store.DB) {
		seedOperators(t, db)
		ctx := context.Background()
		for id, want := range map[string]string{"dev-1": "Zürich", " dev-1\n": "Zürich", "dev-2": "", "": "", "  ": ""} {
			got, err := devicegroups.ResolveFolderNameForDevice(ctx, db, id)
			if err != nil || got != want {
				t.Errorf("folder of %q: %q, %v; want %q", id, got, err, want)
			}
		}
	})
}

func TestResolveOperatorUsernamesForDevice(t *testing.T) {
	eachInstall(t, func(t *testing.T, db *store.DB) {
		seedOperators(t, db)
		ctx := context.Background()
		// the folder's list first, then the groups by name, each group's
		// own names before the members of its user groups
		for id, want := range map[string]string{"dev-1": "alice erin bob carol dave", " dev-1 ": "alice erin bob carol dave", "dev-2": "", "": ""} {
			got, err := devicegroups.ResolveOperatorUsernamesForDevice(ctx, db, id)
			if err != nil || got == nil || strings.Join(got, " ") != want {
				t.Errorf("operators of %q: %q, %v; want %q", id, got, err, want)
			}
		}
		emails, err := devicegroups.ResolveOperatorEmailsForDevice(ctx, db, "dev-1")
		if err != nil {
			t.Fatal(err)
		}
		// carol has no address; dave's is bob's, which goes once
		if got := fmt.Sprint(emails); got != "[{alice alice@example.org} {erin erin@example.org} {bob bob@example.org}]" {
			t.Errorf("addresses of dev-1: %s", got)
		}
		if emails, err := devicegroups.ResolveOperatorEmailsForDevice(ctx, db, "dev-2"); err != nil || emails == nil || len(emails) != 0 {
			t.Errorf("addresses of dev-2: %v, %v", emails, err)
		}
	})
}

// A tag group takes the tags of the server's device table peers, which
// has them on both databases: the console's copy peer has no tags on
// PostgreSQL, and on SQLite it keeps a tag the server has dropped. A
// deleted device has no tags (BUGS D4).
func TestTagGroupOperatorsTakeTheServersTags(t *testing.T) {
	eachInstall(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		s := newService(t, db)
		exec(t, db, `INSERT INTO users (username, password_hash, role, email) VALUES ('gina', 'x', 'operator', 'gina@example.org')`)
		_, err := s.CreateDeviceGroup(ctx, devicegroups.GroupData{
			GUID: jsbind.String("g-print"), Name: jsbind.String("Printers"),
			SourceType: jsbind.String("tag"), TagFilter: jsbind.String("Printer"),
		})
		mustDo(t, "create the tag group", err)
		_, err = s.SetDeviceGroupUserAccess(ctx, "g-print", []string{"gina"})
		mustDo(t, "access list", err)
		exec(t, db, `INSERT INTO peers (id, tags) VALUES ('dev-3', 'office,printer'), ('dev-4', 'printer'), ('dev-5', 'printer')`)
		exec(t, db, `UPDATE peers SET soft_deleted = NOT soft_deleted WHERE id = 'dev-5'`)
		// the console's copy takes dev-4's tag, then the server drops it
		s.SyncServerPeers(ctx)
		exec(t, db, `UPDATE peers SET tags = '' WHERE id = 'dev-4'`)

		for id, want := range map[string]string{"dev-3": "gina", "dev-4": "", "dev-5": ""} {
			got, err := devicegroups.ResolveOperatorUsernamesForDevice(ctx, db, id)
			if err != nil || strings.Join(got, " ") != want {
				t.Errorf("operators of %s: %q, %v; want %q", id, got, err, want)
			}
		}
	})
}
