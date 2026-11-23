package access

import (
	"bytes"
	"context"
	"log"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

func folder(id int64) *int64 { return &id }

var threeDevices = []Device{{ID: "100", FolderID: folder(1)}, {ID: "200", FolderID: folder(2)}, {ID: "300"}}

// Jest: deviceGroupService.scope.test.js.
func TestDeviceScopeOpenDefault(t *testing.T) {
	ctx := context.Background()
	operator := &ScopeUser{ID: 5, Username: "op1", Role: "operator"}

	// No access lists and no grants: no limit.
	s := fakeService(&fakeData{}, &fakeRoles{})
	if scope, err := s.DeviceScopeForUser(ctx, operator, threeDevices); err != nil || scope != nil {
		t.Fatalf("open default without rules: %v, %v; want no limit", scope.IDs(), err)
	}

	// A folder closed to the account hides its devices; a grant is always visible.
	data := &fakeData{
		deviceGroups: []DeviceGroup{{GUID: "folder_1", FolderID: folder(1), AllowedUsers: []string{"other"}, SourceType: "manual"}},
		grants:       []string{"300"},
	}
	withGroups := *operator
	withGroups.UserGroups = []string{}
	scope, err := fakeService(data, &fakeRoles{}).DeviceScopeForUser(ctx, &withGroups, threeDevices)
	if err != nil || scope == nil {
		t.Fatalf("scope: %v, %v", scope, err)
	}
	if got := scope.IDs(); !reflect.DeepEqual(got, []string{"200", "300"}) {
		t.Errorf("scope: %v, want 200 and 300", got)
	}
	if scope.Has("100") || !scope.Has("300") || scope.Size() != 2 || scope.Unlimited() {
		t.Errorf("scope: %v", scope.IDs())
	}
}

func TestDeviceScopeRules(t *testing.T) {
	ctx := context.Background()
	devices := []Device{
		{ID: "A", FolderID: folder(1)}, {ID: "B", FolderID: folder(2), Tags: []string{"Lab"}},
		{ID: "C", Tags: []string{"prod", "lab"}}, {ID: "D"}, {ID: ""},
	}
	groups := []DeviceGroup{
		{GUID: "ops", AllowedGroups: []string{"ug-ops"}},                                   // members A and D
		{GUID: "lab", SourceType: "tag", TagFilter: "LAB", AllowedUsers: []string{"lab1"}}, // B and C by tag
		{GUID: "FOLDER_2", AllowedUsers: []string{"op2"}},                                  // devices of folder 2
		{GUID: "everyone"}, // no access list: not a rule
	}
	members := map[string][]string{"ops": {"A", "D"}, "lab": {"GONE"}, "FOLDER_2": {"A"}, "everyone": {"A", "B", "C", "D"}}
	cases := []struct {
		name       string
		user       *ScopeUser
		userGroups []UserGroup
		grants     []string
		restricted bool
		want       []string // nil: no limit
	}{
		{"nobody signed in", nil, nil, nil, true, nil},
		{"no user id", &ScopeUser{Username: "x", Role: "operator"}, nil, nil, true, nil},
		{"admin", &ScopeUser{ID: 1, Role: "admin"}, nil, nil, true, nil},
		{"super_admin", &ScopeUser{ID: 1, Role: "super_admin"}, nil, nil, true, nil},
		{"global_admin", &ScopeUser{ID: 1, Role: "global_admin"}, nil, nil, true, nil},
		{"server_admin", &ScopeUser{ID: 1, Role: "server_admin"}, nil, nil, true, nil},

		{"open: in no group, sees what is in no closed group", &ScopeUser{ID: 9, Username: "x", Role: "viewer"}, nil, nil, false, []string{}},
		{"open: member of the ops user group", &ScopeUser{ID: 9, Username: "x", Role: "operator"}, []UserGroup{{GUID: "ug-ops"}}, nil, false, []string{"A", "D"}},
		{"open: named on the lab group", &ScopeUser{ID: 9, Username: "lab1", Role: "operator"}, nil, nil, false, []string{"B", "C"}},
		{"open: named on the folder group", &ScopeUser{ID: 9, Username: "op2", Role: "operator"}, nil, nil, false, []string{"B"}},
		{"open: a grant shows a device of a closed group", &ScopeUser{ID: 9, Username: "x", Role: "viewer"}, nil, []string{"D", "ZZ"}, false, []string{"D"}},
		{"open: groups given with the user are not looked up", &ScopeUser{ID: 9, Username: "x", Role: "viewer", UserGroups: []string{}}, []UserGroup{{GUID: "ug-ops"}}, nil, false, []string{}},

		{"restricted: in no group, sees nothing", &ScopeUser{ID: 9, Username: "x", Role: "viewer"}, nil, nil, true, []string{}},
		{"restricted: member of the ops user group", &ScopeUser{ID: 9, Username: "x", Role: "operator"}, []UserGroup{{GUID: "ug-ops"}}, nil, true, []string{"A", "D"}},
		{"restricted: stored members count even when the device is gone", &ScopeUser{ID: 9, Username: "lab1", Role: "operator"}, nil, nil, true, []string{"B", "C", "GONE"}},
		{"restricted: grants count even for unknown devices", &ScopeUser{ID: 9, Username: "x", Role: "viewer"}, nil, []string{"D", "ZZ"}, true, []string{"D", "ZZ"}},
		{"restricted: a folder group has the folder's devices only", &ScopeUser{ID: 9, Username: "op2", Role: "operator"}, nil, nil, true, []string{"B"}},
	}
	for _, c := range cases {
		data := &fakeData{deviceGroups: groups, members: members, userGroups: c.userGroups, grants: c.grants}
		s := fakeService(data, &fakeRoles{})
		s.scopeEnvRestricted = c.restricted
		scope, err := s.DeviceScopeForUser(ctx, c.user, devices)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if (scope == nil) != (c.want == nil) {
			t.Errorf("%s: no limit %v, want %v (%v)", c.name, scope == nil, c.want == nil, scope.IDs())
			continue
		}
		if scope != nil && !reflect.DeepEqual(scope.IDs(), c.want) {
			t.Errorf("%s: %v, want %v", c.name, scope.IDs(), c.want)
		}
	}

	// Without any access list or grant the default alone decides.
	plain := &ScopeUser{ID: 9, Username: "x", Role: "viewer"}
	s := fakeService(&fakeData{deviceGroups: []DeviceGroup{{GUID: "everyone"}}, members: members}, &fakeRoles{})
	if scope, _ := s.DeviceScopeForUser(ctx, plain, devices); scope != nil {
		t.Errorf("open, no rules: %v", scope.IDs())
	}
	s.scopeEnvRestricted = true
	s.InvalidateDeviceScopeDefault()
	if scope, _ := s.DeviceScopeForUser(ctx, plain, devices); scope == nil || scope.Size() != 0 {
		t.Errorf("restricted, no rules: want the empty scope")
	}

	// A failure to read the device groups, their members, the account's
	// user groups or its grants is an error (PA9).
	for _, failing := range []string{"deviceGroups", "grants", "members", "userGroups"} {
		data := &fakeData{deviceGroups: groups, members: members, grants: []string{"D"}, userGroups: []UserGroup{{GUID: "ug-ops"}}, failing: failing}
		if scope, err := fakeService(data, &fakeRoles{}).DeviceScopeForUser(ctx, plain, devices); err == nil {
			t.Errorf("failing %s: scope %v, want an error", failing, scope.IDs())
		}
	}
}

// PA9: in the open default a device group with an access list hides its
// devices from everyone else. While its members cannot be read, those
// devices must not show; the request fails instead.
func TestDeviceScopeFailsOnEveryReadError(t *testing.T) {
	ctx := context.Background()
	devices := []Device{{ID: "secret"}, {ID: "public"}}
	closedGroup := []DeviceGroup{{GUID: "board", AllowedUsers: []string{"ceo"}}}
	someone := &ScopeUser{ID: 9, Username: "someone", Role: "operator"}
	data := &fakeData{deviceGroups: closedGroup, members: map[string][]string{"board": {"secret"}}}
	scope, err := fakeService(data, &fakeRoles{}).DeviceScopeForUser(ctx, someone, devices)
	if err != nil || scope.Has("secret") || !scope.Has("public") {
		t.Fatalf("readable members: %v, %v", scope.IDs(), err)
	}
	for _, failing := range []string{"members", "userGroups", "grants"} {
		data.failing = failing
		s := fakeService(data, &fakeRoles{})
		if scope, err := s.DeviceScopeForUser(ctx, someone, devices); err == nil {
			t.Errorf("unreadable %s: scope %v, want an error", failing, scope.IDs())
		}
		if ok, err := s.UserCanAccessDevice(ctx, someone, &devices[0], devices); err == nil || ok {
			t.Errorf("unreadable %s: access to the closed group's device %v, %v", failing, ok, err)
		}
		if _, err := s.CountEffectiveScope(ctx, someone, devices); err == nil {
			t.Errorf("unreadable %s: the count must fail", failing)
		}
	}
	// Admin roles read nothing and see everything.
	data.failing = "members"
	if scope, err := fakeService(data, &fakeRoles{}).DeviceScopeForUser(ctx, &ScopeUser{ID: 1, Role: "server_admin"}, devices); err != nil || scope != nil {
		t.Errorf("admin: %v, %v", scope.IDs(), err)
	}
}

func TestUserCanAccessDevice(t *testing.T) {
	ctx := context.Background()
	data := &fakeData{
		deviceGroups: []DeviceGroup{{GUID: "closed", AllowedUsers: []string{"other"}}},
		members:      map[string][]string{"closed": {"A"}},
	}
	s := fakeService(data, &fakeRoles{})
	user := &ScopeUser{ID: 9, Username: "x", Role: "operator"}
	a, b := &Device{ID: "A"}, &Device{ID: "B"}
	for _, c := range []struct {
		name   string
		user   *ScopeUser
		device *Device
		all    []Device
		want   bool
	}{
		{"a device of a closed group", user, a, nil, false},
		{"another device", user, b, nil, true},
		{"among all devices", user, b, []Device{*a, *b}, true},
		{"not in the given list", user, b, []Device{*a}, false},
		{"an empty list is used as given", user, b, []Device{}, false},
		{"no device", user, nil, nil, false},
		{"admins see everything", &ScopeUser{ID: 1, Role: "admin"}, a, nil, true},
		{"no device, no limit", &ScopeUser{ID: 1, Role: "admin"}, nil, nil, true},
	} {
		if got, err := s.UserCanAccessDevice(ctx, c.user, c.device, c.all); err != nil || got != c.want {
			t.Errorf("%s: %v, %v; want %v", c.name, got, err, c.want)
		}
	}
	if _, err := fakeService(&fakeData{failing: "deviceGroups"}, &fakeRoles{}).UserCanAccessDevice(ctx, user, a, nil); err == nil {
		t.Error("an unreadable scope must not answer")
	}
}

func TestFilterByScope(t *testing.T) {
	id := func(d Device) string { return d.ID }
	if got := FilterByScope(threeDevices, nil, id); len(got) != 3 {
		t.Errorf("no limit: %v", got)
	}
	scope := newDeviceScope()
	scope.add("200")
	if got := FilterByScope(threeDevices, scope, id); len(got) != 1 || got[0].ID != "200" {
		t.Errorf("limited: %v", got)
	}
	if got := FilterByScope(threeDevices, newDeviceScope(), id); got == nil || len(got) != 0 {
		t.Errorf("empty scope: %v", got)
	}
}

func TestDeviceScopeDefault(t *testing.T) {
	ctx := context.Background()
	clk := &clock{now: time.Unix(1_800_000_000, 0)}
	data := &fakeData{}
	s := fakeService(data, &fakeRoles{})
	s.now = clk.Now

	// Nothing stored: the environment decides.
	if s.DeviceScopeRestrictedDefault(ctx) {
		t.Fatal("open by default")
	}
	s.scopeEnvRestricted = true
	if s.DeviceScopeRestrictedDefault(ctx) {
		t.Fatal("the answer is reused for 30 seconds")
	}
	clk.Advance(30*time.Second - time.Millisecond)
	if s.DeviceScopeRestrictedDefault(ctx) {
		t.Fatal("still within 30 seconds")
	}
	clk.Advance(time.Millisecond)
	if !s.DeviceScopeRestrictedDefault(ctx) || !s.DeviceScopeEnvRestricted() {
		t.Fatal("after 30 seconds the environment's value is read")
	}

	// The stored setting wins, in any letter case; an empty one does not count.
	for value, want := range map[string]bool{"open": false, "restricted": true, "RESTRICTED": true, "Open": false, "": true, "other": false} {
		data.settingValue = value
		s.InvalidateDeviceScopeDefault()
		if got := s.DeviceScopeRestrictedDefault(ctx); got != want {
			t.Errorf("setting %q: restricted %v, want %v", value, got, want)
		}
	}

	// A failed read answers the environment and is not kept.
	data.settingValue = "open"
	data.failing = "setting"
	s.InvalidateDeviceScopeDefault()
	if !s.DeviceScopeRestrictedDefault(ctx) {
		t.Fatal("a failed read must answer the environment's value")
	}
	data.failing = ""
	if s.DeviceScopeRestrictedDefault(ctx) {
		t.Fatal("the failed read was kept")
	}

	if !New(nil, &fakeRoles{}, Options{DeviceScopeDefault: "Restricted"}).DeviceScopeEnvRestricted() {
		t.Error("DEVICE_SCOPE_DEFAULT is read in any letter case")
	}
	if New(nil, &fakeRoles{}, Options{}).DeviceScopeEnvRestricted() {
		t.Error("the default mode is open")
	}
}

func TestGroupPeerIDs(t *testing.T) {
	ctx := context.Background()
	devices := []Device{
		{ID: "A", FolderID: folder(7), Tags: []string{"lab"}}, {ID: "B", FolderID: folder(8), Tags: []string{"LAB", "x"}},
		{ID: "C", Tags: []string{"labs"}},
	}
	s := fakeService(&fakeData{members: map[string][]string{"g": {"M1", "A"}, "folder_7": {"M2"}}}, &fakeRoles{})
	for _, c := range []struct {
		name  string
		group DeviceGroup
		want  []string
	}{
		{"stored members", DeviceGroup{GUID: "g"}, []string{"A", "M1"}},
		{"members and tag", DeviceGroup{GUID: "g", SourceType: "tag", TagFilter: " Lab "}, []string{"A", "B", "M1"}},
		{"a tag group without a tag", DeviceGroup{GUID: "g", SourceType: "tag"}, []string{"A", "M1"}},
		{"a manual group ignores its tag", DeviceGroup{GUID: "g", SourceType: "manual", TagFilter: "lab"}, []string{"A", "M1"}},
		{"a folder group by guid has no stored members", DeviceGroup{GUID: "folder_7"}, []string{"A"}},
		{"a folder group by guid, any letter case", DeviceGroup{GUID: " Folder_8 "}, []string{"B"}},
		{"an explicit folder wins over the guid", DeviceGroup{GUID: "folder_7", FolderID: folder(8)}, []string{"B"}},
		{"a folder nobody is in", DeviceGroup{GUID: "folder_99"}, []string{}},
		{"not a folder guid", DeviceGroup{GUID: "folder_7x"}, []string{}},
	} {
		got := []string{}
		for id := range s.GroupPeerIDs(ctx, c.group, devices) {
			got = append(got, id)
		}
		if !reflect.DeepEqual(sortedStrings(got), c.want) {
			t.Errorf("%s: %v, want %v", c.name, sortedStrings(got), c.want)
		}
	}
	enriched := s.EnrichGroups(ctx, []DeviceGroup{{GUID: "g"}, {GUID: "folder_7"}}, devices)
	if *enriched[0].MemberCount != 2 || *enriched[1].MemberCount != 1 {
		t.Errorf("member counts: %d, %d", *enriched[0].MemberCount, *enriched[1].MemberCount)
	}
}

func TestScopeNormalizers(t *testing.T) {
	if got := NormalizeTags(`["x"," y ",""]`); !reflect.DeepEqual(got, []string{"x", "y"}) {
		t.Errorf("tags from a JSON list: %v", got)
	}
	if got := NormalizeTags(" a , b ,, "); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("tags from text: %v", got)
	}
	if got := NormalizeTags(nil); got == nil || len(got) != 0 {
		t.Errorf("no tags: %v", got)
	}
	if got := NormalizePeerIDs([]any{" a ", "b", "a", ""}); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("peer ids: %v", got)
	}
	if got := NormalizePeerIDs("D1, D2,,D1"); !reflect.DeepEqual(got, []string{"D1", "D2"}) {
		t.Errorf("peer ids from text: %v", got)
	}
	many := make([]any, 600)
	for i := range many {
		many[i] = float64(i)
	}
	if got := NormalizePeerIDs(many); len(got) != 500 || got[0] != "1" {
		// 0 counts as empty and is dropped.
		t.Errorf("peer ids are capped at 500: %d, first %q", len(got), got[0])
	}
	if got := NormalizeFolderIDs([]any{float64(1), "3", "x", float64(3), " 4", "5x", nil}); !reflect.DeepEqual(got, []int64{1, 3, 4, 5}) {
		t.Errorf("folder ids: %v", got)
	}
	if got := NormalizeFolderIDs("1,2"); len(got) != 0 {
		t.Errorf("folder ids from text: %v, want none", got)
	}
	if got := NormalizeFolderIDs(many); len(got) != 200 {
		t.Errorf("folder ids are capped at 200: %d", len(got))
	}
	if FolderGroupGUID(12) != "folder_12" {
		t.Error("FolderGroupGUID")
	}
	names := make([]string, 130)
	for i := range names {
		names[i] = "u" + strings.Repeat("x", i)
	}
	if got := NormalizeUsernames(strings.Join(names, ",")); len(got) != 100 {
		t.Errorf("usernames are capped at 100: %d", len(got))
	}
}

// Jest: userScopeService.test.js and the store behind it.
func TestScopeStore(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		createSchema(t, db)
		ctx := context.Background()
		for _, name := range []string{"op1", "op2"} {
			mustExec(t, db, `INSERT INTO users (username, password_hash, role) VALUES (?, ?, ?)`, name, "x", "operator")
		}
		mustExec(t, db, `INSERT INTO folders (name, sort_order) VALUES (?, ?)`, "Lab", 1)
		mustExec(t, db, `INSERT INTO folders (name, sort_order) VALUES (?, ?)`, "Office", 0)
		var logged bytes.Buffer
		previous := log.Writer()
		log.SetOutput(&logged)
		t.Cleanup(func() { log.SetOutput(previous) })
		known := map[string]bool{"a": true}
		s := New(db, &fakeRoles{}, Options{PeerExists: func(_ context.Context, id string) (bool, error) { return known[id], nil }})

		// Grants: normalised ids are stored and returned; unknown devices are logged.
		got, err := s.SyncUserPeerGrants(ctx, 1, []any{" a ", "b", "a", ""})
		if err != nil || !reflect.DeepEqual(got, []string{"a", "b"}) {
			t.Fatalf("SyncUserPeerGrants: %v, %v", got, err)
		}
		if ids, _ := s.UserPeerGrantIDs(ctx, 1); !reflect.DeepEqual(ids, []string{"a", "b"}) {
			t.Errorf("stored grants: %v", ids)
		}
		if !strings.Contains(logged.String(), "peer grant references unknown device id: b") || strings.Contains(logged.String(), "device id: a") {
			t.Errorf("log: %q", logged.String())
		}
		// A write replaces the list; another account's grants stay.
		if _, err := s.SyncUserPeerGrants(ctx, 2, "z"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.SyncUserPeerGrants(ctx, 1, []any{"c"}); err != nil {
			t.Fatal(err)
		}
		if ids, _ := s.UserPeerGrantIDs(ctx, 1); !reflect.DeepEqual(ids, []string{"c"}) {
			t.Errorf("after the second write: %v", ids)
		}
		if ids, _ := s.UserPeerGrantIDs(ctx, 2); !reflect.DeepEqual(ids, []string{"z"}) {
			t.Errorf("the other account's grants: %v", ids)
		}
		// Grants for an account that does not exist fail, they are not dropped.
		if _, err := s.SyncUserPeerGrants(ctx, 99, []any{"a"}); err == nil {
			t.Error("grants for an unknown account must fail")
		}
		if ids, err := s.SyncUserPeerGrants(ctx, 0, []any{"a"}); err != nil || len(ids) != 0 {
			t.Errorf("no account: %v, %v", ids, err)
		}
		if ids, err := s.UserPeerGrantIDs(ctx, 0); err != nil || ids == nil || len(ids) != 0 {
			t.Errorf("no account: %v, %v", ids, err)
		}

		// The log stops after 20 unknown devices.
		logged.Reset()
		many := make([]any, 30)
		for i := range many {
			many[i] = "unknown-" + strings.Repeat("x", i)
		}
		if _, err := s.SyncUserPeerGrants(ctx, 1, many); err != nil {
			t.Fatal(err)
		}
		if n := strings.Count(logged.String(), "unknown device id"); n != 20 || !strings.Contains(logged.String(), "capped after 20 warnings (10 remaining unchecked)") {
			t.Errorf("%d unknown-device lines:\n%s", n, logged.String())
		}

		// Folder access is the username on the folder's mirror device group.
		if ids, err := s.UserFolderIDs(ctx, "op1"); err != nil || len(ids) != 0 {
			t.Fatalf("no folders yet: %v, %v", ids, err)
		}
		selected, err := s.SyncUserFolderAccess(ctx, "op1", []any{float64(1), "2", float64(99)})
		if err != nil || !reflect.DeepEqual(selected, []int64{1, 2, 99}) {
			t.Fatalf("SyncUserFolderAccess: %v, %v", selected, err)
		}
		// Folders come in display order: Office (2) before Lab (1).
		if ids, _ := s.UserFolderIDs(ctx, "op1"); !reflect.DeepEqual(ids, []int64{2, 1}) {
			t.Errorf("op1's folders: %v", ids)
		}
		mirror, err := s.DeviceGroupByGUID(ctx, "folder_1")
		if err != nil || mirror == nil {
			t.Fatalf("mirror group: %v, %v", mirror, err)
		}
		if mirror.Name != "Lab" || *mirror.Note != "NeoDesk folder access scope" || mirror.SourceType != "manual" || !reflect.DeepEqual(mirror.AllowedUsers, []string{"op1"}) || mirror.MemberCount != nil {
			t.Errorf("mirror group: %+v", mirror)
		}
		// A second account joins one folder, the first leaves the other.
		if _, err := s.SyncUserFolderAccess(ctx, "op2", []any{float64(1)}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.SyncUserFolderAccess(ctx, "op1", []any{float64(1)}); err != nil {
			t.Fatal(err)
		}
		mirror, _ = s.DeviceGroupByGUID(ctx, "folder_1")
		office, _ := s.DeviceGroupByGUID(ctx, "folder_2")
		if !reflect.DeepEqual(mirror.AllowedUsers, []string{"op1", "op2"}) || len(office.AllowedUsers) != 0 {
			t.Errorf("after the changes: Lab %v, Office %v", mirror.AllowedUsers, office.AllowedUsers)
		}
		// A renamed folder renames its mirror group at the next sync.
		mustExec(t, db, `UPDATE folders SET name = ? WHERE id = ?`, "Laboratory", 1)
		if _, err := s.SyncUserFolderAccess(ctx, "op1", []any{float64(1)}); err != nil {
			t.Fatal(err)
		}
		if mirror, _ = s.DeviceGroupByGUID(ctx, "folder_1"); mirror.Name != "Laboratory" {
			t.Errorf("mirror group name: %q", mirror.Name)
		}
		// Names of no account are dropped from an access list; an unknown group is left alone.
		if err := s.SetDeviceGroupUserAccess(ctx, "folder_1", []string{"op2", " op2 ", "ghost", ""}); err != nil {
			t.Fatal(err)
		}
		if mirror, _ = s.DeviceGroupByGUID(ctx, "folder_1"); !reflect.DeepEqual(mirror.AllowedUsers, []string{"op2"}) {
			t.Errorf("access list: %v", mirror.AllowedUsers)
		}
		if err := s.SetDeviceGroupUserAccess(ctx, "nope", []string{"op1"}); err != nil {
			t.Errorf("unknown group: %v", err)
		}

		// The mirror groups act as scope rules: op1 lost the Lab folder.
		devices := []Device{{ID: "L1", FolderID: folder(1)}, {ID: "O1", FolderID: folder(2)}, {ID: "X"}}
		scope, err := s.DeviceScopeForUser(ctx, &ScopeUser{ID: 1, Username: "op1", Role: "operator"}, devices)
		if err != nil || scope == nil || !reflect.DeepEqual(scope.IDs(), []string{"O1", "X"}) {
			// "c" and the unknown ids are granted but not among the devices.
			t.Errorf("op1's scope: %v, %v", scope.IDs(), err)
		}
		groups, err := s.AllDeviceGroups(ctx)
		if err != nil || len(groups) != 2 || groups[0].GUID != "folder_1" || *groups[0].MemberCount != 0 {
			t.Errorf("device groups: %+v, %v", groups, err)
		}
		if members, err := s.DeviceGroupMembers(ctx, "nope"); err != nil || members == nil || len(members) != 0 {
			t.Errorf("members of an unknown group: %v, %v", members, err)
		}

		// The stored default mode.
		if s.DeviceScopeRestrictedDefault(ctx) {
			t.Error("no setting: open")
		}
		mustExec(t, db, `INSERT INTO settings (key, value) VALUES (?, ?)`, "device_scope_default", "restricted")
		s.InvalidateDeviceScopeDefault()
		if !s.DeviceScopeRestrictedDefault(ctx) {
			t.Error("the stored setting must be read")
		}
		count, err := s.CountEffectiveScope(ctx, &ScopeUser{ID: 2, Username: "op2", Role: "operator"}, devices)
		if err != nil || count != (ScopeCount{Count: 2, Restricted: true}) {
			// op2: the Lab folder's device and the grant "z".
			t.Errorf("op2's count: %+v, %v", count, err)
		}
		if count, _ := s.CountEffectiveScope(ctx, &ScopeUser{ID: 1, Role: "admin"}, devices); count != (ScopeCount{Count: 3}) {
			t.Errorf("admin's count: %+v", count)
		}
	})
}

func TestPeerLookupFailuresAreLogged(t *testing.T) {
	var logged bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(previous) })
	s := fakeService(&fakeData{}, &fakeRoles{})
	s.peerExists = func(_ context.Context, id string) (bool, error) {
		if id == "bad" {
			return false, errFake
		}
		return true, nil
	}
	s.warnUnknownPeerIDs(context.Background(), []string{"known-1", "bad"})
	if !strings.Contains(logged.String(), "peer id lookup failed for bad") || strings.Contains(logged.String(), "known-1") {
		t.Errorf("log: %q", logged.String())
	}
	logged.Reset()
	s.peerExists = nil
	s.warnUnknownPeerIDs(context.Background(), []string{"x"})
	if logged.Len() != 0 {
		t.Errorf("without a lookup nothing is logged: %q", logged.String())
	}
}

// PA11: replacing an account's grants is one transaction on both
// databases, so a failed store keeps the old grants.
func TestSetUserPeerGrantsKeepsTheOldGrantsWhenStoringFails(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		createSchema(t, db)
		ctx := context.Background()
		mustExec(t, db, `INSERT INTO users (id, username, password_hash, role) VALUES (1, 'u1', 'x', 'viewer')`)
		s := New(db, &fakeRoles{}, Options{})
		if _, err := s.SyncUserPeerGrants(ctx, 1, []any{"a", "b"}); err != nil {
			t.Fatal(err)
		}
		// The database refuses the second id.
		mustExec(t, db, db.Pick(
			`CREATE TRIGGER no_long_ids BEFORE INSERT ON user_peer_grants WHEN NEW.peer_id = 'refused' BEGIN SELECT RAISE(ABORT, 'too long'); END`,
			`CREATE OR REPLACE FUNCTION no_long_ids() RETURNS trigger AS $$ BEGIN IF NEW.peer_id = 'refused' THEN RAISE EXCEPTION 'too long'; END IF; RETURN NEW; END $$ LANGUAGE plpgsql`))
		if db.IsPostgres() {
			mustExec(t, db, `CREATE TRIGGER no_long_ids BEFORE INSERT ON user_peer_grants FOR EACH ROW EXECUTE FUNCTION no_long_ids()`)
		}
		if _, err := s.SyncUserPeerGrants(ctx, 1, []any{"c", "refused"}); err == nil {
			t.Fatal("storing a refused id must fail")
		}
		got, err := s.UserPeerGrants(ctx, 1)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(sortedStrings(got), ",") != "a,b" {
			t.Errorf("grants after the failed store: %v, want the old a,b", got)
		}
	})
}

// PA10: an access list is checked whole. The 101st account of a list and
// the 101st user group are on it; reading a stored list keeps every entry.
func TestGroupAllowedForUserReadsTheWholeList(t *testing.T) {
	var names, guids []string
	for i := 0; i < 150; i++ {
		names = append(names, "user"+strconv.Itoa(i))
		guids = append(guids, "ug"+strconv.Itoa(i))
	}
	group := DeviceGroup{AllowedUsers: names}
	if !GroupAllowedForUser(group, &ScopeUser{ID: 9, Username: "user120", Role: "viewer"}) {
		t.Error("the 121st name of the list is not on it")
	}
	if GroupAllowedForUser(group, &ScopeUser{ID: 9, Username: "user150", Role: "viewer"}) {
		t.Error("a name the list does not have is on it")
	}
	byGroup := DeviceGroup{AllowedGroups: guids}
	if !GroupAllowedForUser(byGroup, &ScopeUser{ID: 9, Username: "x", Role: "viewer", UserGroups: []string{"ug140"}}) {
		t.Error("the 141st user group of the list is not on it")
	}
	if got := StoredUsernames(names); len(got) != 150 {
		t.Errorf("stored list read with %d names, want 150", len(got))
	}
	if got := StoredGroupGUIDs(anyList(guids)); len(got) != 150 {
		t.Errorf("stored list read with %d groups, want 150", len(got))
	}
	// A submitted list is still cut at 100 when it is saved.
	if got := NormalizeUsernames(anyList(names)); len(got) != 100 {
		t.Errorf("submitted list kept %d names, want 100", len(got))
	}
}

func anyList(items []string) []any {
	out := make([]any, len(items))
	for i, item := range items {
		out[i] = item
	}
	return out
}
