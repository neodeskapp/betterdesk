package devicegroups_test

import (
	"context"
	"encoding/json"
	"math"
	"regexp"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/access"
	"github.com/unitronix/betterdesk-server/console/devicegroups"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

func newService(t *testing.T, db *store.DB) *devicegroups.Service {
	t.Helper()
	return devicegroups.New(devicegroups.Options{DB: db, Access: access.New(db, access.NewDBRoleSource(nil), access.Options{})})
}

func exec(t *testing.T, db *store.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(context.Background(), query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func jsonOf(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// The form a stored time and an id take in JSON, by database and by who
// made the table.
var (
	sqliteTime = `"\d{4}-\d\d-\d\d \d\d:\d\d:\d\d"`
	isoTime    = `"\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z"`
)

func idForm(db *store.DB, origin storetest.Origin, id string) string {
	if db.IsPostgres() && origin == storetest.GoServerFirst {
		return `"` + id + `"`
	}
	return id
}

// idPattern matches an id in the form idForm gives, whatever its number.
func idPattern(db *store.DB, origin storetest.Origin) string { return idForm(db, origin, `\d+`) }

func timeForm(db *store.DB) string {
	if db.IsPostgres() {
		return isoTime
	}
	return sqliteTime
}

func TestFolders(t *testing.T) {
	storetest.EachOrigin(t, func(t *testing.T, db *store.DB, origin storetest.Origin) {
		ctx := context.Background()
		s := newService(t, db)
		id, err := s.CreateFolder(ctx, jsbind.String("Office"), jsbind.String("#22c55e"), jsbind.Undefined)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := jsonOf(t, id), idForm(db, origin, "1"); got != want {
			t.Errorf("id of the new folder = %s, want %s", got, want)
		}
		if _, err := s.CreateFolder(ctx, jsbind.String("Annex"), jsbind.String(""), jsbind.String("box")); err != nil {
			t.Fatal(err)
		}
		folders, err := s.AllFolders(ctx)
		if err != nil {
			t.Fatal(err)
		}
		want := regexp.MustCompile(`^\[\{"id":` + idForm(db, origin, "2") + `,"name":"Annex","color":"#6366f1","icon":"box","sort_order":0,"created_at":` + timeForm(db) + `\},` +
			`\{"id":` + idForm(db, origin, "1") + `,"name":"Office","color":"#22c55e","icon":"folder","sort_order":0,"created_at":` + timeForm(db) + `\}\]$`)
		if got := jsonOf(t, folders); !want.MatchString(got) {
			t.Errorf("folders:\n%s", got)
		}

		// The id as the request gave it: a number finds the folder, text
		// and NaN find nothing on SQLite and fail on PostgreSQL
		if row, err := s.FolderByID(ctx, jsbind.Number(1)); err != nil || row == nil || jsonOf(t, row.Get("name")) != `"Office"` {
			t.Errorf("folder 1: %v %v", row, err)
		}
		for _, id := range []jsbind.Value{jsbind.Number(math.NaN()), jsbind.String("abc"), jsbind.Number(1.5)} {
			row, err := s.FolderByID(ctx, id)
			if db.IsPostgres() != (err != nil) || row != nil {
				t.Errorf("folder %s: %v %v", id.JSON(), row, err)
			}
		}
		if row, err := s.FolderByID(ctx, jsbind.Bool(true)); err == nil || row != nil {
			t.Errorf("folder true: %v %v", row, err)
		}

		// Only the fields that are not undefined are written
		if err := s.UpdateFolder(ctx, jsbind.Number(1), jsbind.String("Office 2"), jsbind.Undefined, jsbind.String("star")); err != nil {
			t.Fatal(err)
		}
		if err := s.UpdateFolder(ctx, jsbind.Number(1), jsbind.Undefined, jsbind.Undefined, jsbind.Undefined); err != nil {
			t.Fatalf("an update without fields: %v", err)
		}
		row, _ := s.FolderByID(ctx, jsbind.Number(1))
		if got := jsonOf(t, []any{row.Get("name"), row.Get("color"), row.Get("icon")}); got != `["Office 2","#22c55e","star"]` {
			t.Errorf("after the update: %s", got)
		}

		// Devices into folders; a number as device id is written as the
		// driver bound it
		if err := s.AssignDevicesToFolder(ctx, []jsbind.Value{jsbind.String("A"), jsbind.Number(7)}, jsbind.Number(1)); err != nil {
			t.Fatal(err)
		}
		if err := s.AssignDeviceToFolder(ctx, jsbind.String("B"), jsbind.Number(2)); err != nil {
			t.Fatal(err)
		}
		if err := s.AssignDeviceToFolder(ctx, jsbind.String("A"), jsbind.Number(2)); err != nil {
			t.Fatal(err)
		}
		assignments, err := s.AllFolderAssignments(ctx)
		if err != nil {
			t.Fatal(err)
		}
		seven := "7.0"
		if db.IsPostgres() {
			seven = "7"
		}
		if got, want := jsonOf(t, assignments), `{"`+seven+`":1,"A":2,"B":2}`; got != want {
			t.Errorf("assignments = %s, want %s", got, want)
		}
		// A value better-sqlite3 cannot bind stops the whole batch on
		// SQLite; node-postgres sends it as text
		err = s.AssignDevicesToFolder(ctx, []jsbind.Value{jsbind.String("C"), jsbind.Bool(true)}, jsbind.Number(1))
		after, _ := s.AllFolderAssignments(ctx)
		if db.IsPostgres() {
			if err != nil || after["C"] == nil || after["true"] == nil {
				t.Errorf("a flag as device id on PostgreSQL: %v %v", err, after)
			}
			if err := s.AssignDevicesToFolder(ctx, []jsbind.Value{jsbind.String("C"), jsbind.String("true")}, jsbind.Null); err != nil {
				t.Fatal(err)
			}
		} else if err == nil || after["C"] != nil {
			t.Errorf("a flag as device id on SQLite: %v %v", err, after)
		}
		if err := s.AssignDeviceToFolder(ctx, jsbind.String("B"), jsbind.Null); err != nil {
			t.Fatal(err)
		}
		if err := s.AssignDevicesToFolder(ctx, []jsbind.Value{jsbind.String("A")}, jsbind.Null); err != nil {
			t.Fatal(err)
		}
		if after, _ := s.AllFolderAssignments(ctx); jsonOf(t, after) != `{"`+seven+`":1}` {
			t.Errorf("after taking devices out: %s", jsonOf(t, after))
		}

		if err := s.DeleteFolder(ctx, jsbind.Number(1)); err != nil {
			t.Fatal(err)
		}
		if after, _ := s.AllFolderAssignments(ctx); len(after) != 0 {
			t.Errorf("assignments of a deleted folder: %v", after)
		}
		if row, _ := s.FolderByID(ctx, jsbind.Number(1)); row != nil {
			t.Error("the folder is still there")
		}
	})
}

var uuid = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestDeviceGroups(t *testing.T) {
	storetest.EachOrigin(t, func(t *testing.T, db *store.DB, origin storetest.Origin) {
		ctx := context.Background()
		s := newService(t, db)
		exec(t, db, `DELETE FROM device_groups`)
		exec(t, db, `DELETE FROM user_groups`)
		for i, name := range []string{"bob", "alice", "carol"} {
			exec(t, db, `INSERT INTO users (id, username, password_hash, role) VALUES (?, ?, 'x', 'operator')`, i+1, name)
		}
		exec(t, db, `INSERT INTO user_groups (guid, name, note, team_id) VALUES ('ug-b', 'Beta', '', ''), ('ug-a', 'Alpha', 'first', 'org-1')`)

		created, err := s.CreateDeviceGroup(ctx, devicegroups.GroupData{Name: jsbind.String("Laptops"), SourceType: jsbind.String("tag"), TagFilter: jsbind.String("laptop"), TeamID: jsbind.String("org-1")})
		if err != nil {
			t.Fatal(err)
		}
		guid := jsonOf(t, created.Get("guid"))
		if !uuid.MatchString(strings.Trim(guid, `"`)) {
			t.Errorf("guid of a new group: %s", guid)
		}
		manual, err := s.CreateDeviceGroup(ctx, devicegroups.GroupData{GUID: jsbind.String("folder_3"), Name: jsbind.String("Annex"), SourceType: jsbind.String("dynamic"), TagFilter: jsbind.String("kept?")})
		if err != nil {
			t.Fatal(err)
		}
		if got := jsonOf(t, []any{manual.Get("note"), manual.Get("team_id"), manual.Get("source_type"), manual.Get("tag_filter")}); got != `["","","manual",""]` {
			t.Errorf("defaults of a manual group: %s", got)
		}

		if _, err := s.SetDeviceGroupUserAccess(ctx, "folder_3", []string{"carol", " alice", "nobody", "alice"}); err != nil {
			t.Fatal(err)
		}
		group, err := s.SetDeviceGroupUserGroupAccess(ctx, "folder_3", []string{"ug-b", " ug-a ", "missing", "ug-a", ""})
		if err != nil {
			t.Fatal(err)
		}
		if got := jsonOf(t, []any{group.Get("allowed_users"), group.Get("allowed_groups"), group.Get("allowed_user_groups")}); got != `[["alice","carol"],["ug-a","ug-b"],[{"guid":"ug-a","name":"Alpha"},{"guid":"ug-b","name":"Beta"}]]` {
			t.Errorf("access lists: %s", got)
		}
		if got, err := s.SetDeviceGroupUserGroupAccess(ctx, "no-such-group", []string{"ug-a"}); got != nil || err != nil {
			t.Errorf("access of an unknown group: %v %v", got, err)
		}
		exec(t, db, `INSERT INTO device_group_members (device_group_id, peer_id) SELECT id, 'P1' FROM device_groups WHERE name = 'Laptops'`)
		exec(t, db, `INSERT INTO device_group_members (device_group_id, peer_id) SELECT id, 'P2' FROM device_groups WHERE name = 'Laptops'`)

		all, err := s.AllDeviceGroups(ctx)
		if err != nil {
			t.Fatal(err)
		}
		want := regexp.MustCompile(`^\[\{"id":` + idPattern(db, origin) + `,"guid":"folder_3","name":"Annex","note":"","team_id":"","source_type":"manual","tag_filter":"","created_at":` + timeForm(db) +
			`,"member_count":0,"allowed_users":\["alice","carol"\],"allowed_groups":\["ug-a","ug-b"\],"allowed_user_groups":\[\{"guid":"ug-a","name":"Alpha"\},\{"guid":"ug-b","name":"Beta"\}\]\},` +
			`\{"id":` + idPattern(db, origin) + `,"guid":"[0-9a-f-]{36}","name":"Laptops","note":"","team_id":"org-1","source_type":"tag","tag_filter":"laptop","created_at":` + timeForm(db) +
			`,"member_count":2,"allowed_users":\[\],"allowed_groups":\[\],"allowed_user_groups":\[\]\}\]$`)
		if got := jsonOf(t, all); !want.MatchString(got) {
			t.Errorf("all device groups:\n%s", got)
		}

		// An update writes what is given; the tag filter as text cut to 50
		updated, err := s.UpdateDeviceGroup(ctx, "folder_3", devicegroups.GroupData{Name: jsbind.String("Annex 2"), SourceType: jsbind.String("tag"), TagFilter: jsbind.String(strings.Repeat("t", 60))})
		if err != nil {
			t.Fatal(err)
		}
		if got := jsonOf(t, []any{updated.Get("name"), updated.Get("source_type"), updated.Get("tag_filter"), updated.Get("note")}); got != `["Annex 2","tag","`+strings.Repeat("t", 50)+`",""]` {
			t.Errorf("updated group: %s", got)
		}
		if got, err := s.UpdateDeviceGroup(ctx, "folder_3", devicegroups.GroupData{}); got != nil || err != nil {
			t.Errorf("an update without fields: %v %v", got, err)
		}
		if got, err := s.UpdateDeviceGroup(ctx, "folder_3", devicegroups.GroupData{TagFilter: jsbind.Number(0), SourceType: jsbind.Null}); err != nil || jsonOf(t, []any{got.Get("source_type"), got.Get("tag_filter")}) != `["manual",""]` {
			t.Errorf("an update with odd values: %v %v", got, err)
		}

		if err := s.DeleteDeviceGroup(ctx, "folder_3"); err != nil {
			t.Fatal(err)
		}
		if err := s.DeleteDeviceGroup(ctx, "folder_3"); err != nil {
			t.Errorf("deleting an unknown group: %v", err)
		}
		var left int
		if err := db.QueryRow(ctx, `SELECT (SELECT COUNT(*) FROM device_group_user_access) + (SELECT COUNT(*) FROM device_group_user_group_access)`).Scan(&left); err != nil || left != 0 {
			t.Errorf("access rows of a deleted group: %d %v", left, err)
		}
		if group, err := s.DeviceGroupByGUID(ctx, "folder_3"); group != nil || err != nil {
			t.Errorf("a deleted group: %v %v", group, err)
		}

		// User groups with their member count
		exec(t, db, `INSERT INTO user_group_members (user_group_id, user_id) SELECT id, 1 FROM user_groups WHERE guid = 'ug-a'`)
		userGroups, err := s.AllUserGroups(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if got := jsonOf(t, []any{userGroups[0].Get("guid"), userGroups[0].Get("member_count"), userGroups[1].Get("guid"), userGroups[1].Get("member_count")}); got != `["ug-a",1,"ug-b",0]` {
			t.Errorf("user groups: %s", got)
		}

		if id, ok, err := s.UserIDByID(ctx, 2); id != 2 || !ok || err != nil {
			t.Errorf("user 2: %d %v %v", id, ok, err)
		}
		if _, ok, err := s.UserIDByID(ctx, 99); ok || err != nil {
			t.Errorf("user 99: %v %v", ok, err)
		}
	})
}

func TestFolderIDFromGroupGUID(t *testing.T) {
	for guid, want := range map[any]float64{
		"folder_12": 12, " FOLDER_7 ": 7, "folder_007": 7, "Folder_0": 0, "folder_1x": -1, "folder_": -1,
		"xfolder_1": -1, "folder_1\n": 1, "folder_-1": -1, "": -1, nil: -1, float64(5): -1, "folder_1 2": -1,
		"folder_" + strings.Repeat("9", 400): -1,
	} {
		got, ok := devicegroups.FolderIDFromGroupGUID(guid)
		if want < 0 && ok || want >= 0 && (!ok || got != want) {
			t.Errorf("FolderIDFromGroupGUID(%q) = %v %v, want %v", guid, got, ok, want)
		}
	}
}

func TestScopeDevices(t *testing.T) {
	devices := []*goapi.Object{
		goapi.NewObject("id", "A", "folder_id", float64(3), "tags", "x, y"),
		goapi.NewObject("id", float64(7), "folder_id", "4abc", "tags", []any{"z", " "}),
		goapi.NewObject("id", "C", "folder_id", nil),
		nil,
	}
	got := devicegroups.ScopeDevices(devices)
	if len(got) != 3 || got[0].ID != "A" || *got[0].FolderID != 3 || strings.Join(got[0].Tags, "|") != "x|y" ||
		got[1].ID != "7" || *got[1].FolderID != 4 || strings.Join(got[1].Tags, "|") != "z" || got[2].FolderID != nil {
		t.Errorf("ScopeDevices: %+v", got)
	}
}

func TestDeviceStore(t *testing.T) {
	storetest.EachOrigin(t, func(t *testing.T, db *store.DB, origin storetest.Origin) {
		ctx := context.Background()
		s := newService(t, db)
		ds := devicegroups.DeviceStore{Groups: s}
		yes, no := any(1), any(0)
		if db.IsPostgres() {
			yes, no = true, false
		}
		exec(t, db, `INSERT INTO peer (id, note, status_online, is_banned, is_deleted) VALUES ('P1', 'old', ?, ?, ?), ('P2', '', ?, ?, ?), ('P3', '', ?, ?, ?)`,
			yes, no, no, no, yes, no, yes, no, yes)
		stats, err := ds.GetStats(ctx)
		if err != nil || jsonOf(t, stats) != `{"total":2,"online":1,"banned":1,"offline":1}` {
			t.Errorf("stats: %s %v", jsonOf(t, stats), err)
		}
		if err := ds.UpdateDevice(ctx, "P1", goapi.NewObject("note", "new", "display_name", "ignored")); err != nil {
			t.Fatal(err)
		}
		var note string
		if err := db.QueryRow(ctx, `SELECT note FROM peer WHERE id = 'P1'`).Scan(&note); err != nil || note != "new" {
			t.Errorf("note of P1: %q %v", note, err)
		}
		if err := ds.SetSetting(ctx, "server_backend", "betterdesk"); err != nil {
			t.Fatal(err)
		}
		if err := ds.SetSetting(ctx, "server_backend", "again"); err != nil {
			t.Fatal(err)
		}
		var value string
		if err := db.QueryRow(ctx, `SELECT value FROM settings WHERE key = 'server_backend'`).Scan(&value); err != nil || value != "again" {
			t.Errorf("setting: %q %v", value, err)
		}
		exec(t, db, `INSERT INTO peer_sysinfo (peer_id, hostname, platform, version, os_full) VALUES ('P1', 'host', 'Windows', '1.4', 'Windows 11')`)
		rows, err := ds.GetAllPeerSysinfo(ctx)
		if err != nil || jsonOf(t, rows) != `[{"peer_id":"P1","hostname":"host","platform":"Windows","version":"1.4","os_full":"Windows 11"}]` {
			t.Errorf("sysinfo: %s %v", jsonOf(t, rows), err)
		}
		if _, err := s.CreateFolder(ctx, jsbind.String("F"), jsbind.Undefined, jsbind.Undefined); err != nil {
			t.Fatal(err)
		}
		if err := s.AssignDeviceToFolder(ctx, jsbind.String("P1"), jsbind.Number(1)); err != nil {
			t.Fatal(err)
		}
		if got, err := ds.GetAllFolderAssignments(ctx); err != nil || jsonOf(t, got) != `{"P1":1}` {
			t.Errorf("assignments: %v %v", got, err)
		}
	})
}
