package access

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// TestOracleStore replays a scenario the Node console's real services ran
// on a scratch database (testdata/store_*.json.gz): the same rows, the
// same calls in the same order, and every answer must be the same.

type storeStep struct {
	Op       string         `json:"op"`
	GUID     string         `json:"guid"`
	UserID   int64          `json:"userId"`
	ID       any            `json:"id"`
	Type     string         `json:"type"`
	Body     any            `json:"body"`
	Role     string         `json:"role"`
	Value    string         `json:"value"`
	User     *oracleUser    `json:"user"`
	Devices  []oracleDevice `json:"devices"`
	Device   *oracleDevice  `json:"device"`
	All      []oracleDevice `json:"all"`
	Username string         `json:"username"`
	FolderID any            `json:"folderIds"`
	PeerIDs  any            `json:"peerIds"`

	Result    json.RawMessage `json:"result"`
	Found     *bool           `json:"found"`
	LoadError bool            `json:"loadError"`
	Error     bool            `json:"error"`
	Status    *int            `json:"status"`
	Message   *string         `json:"message"`
}

type storeScenario struct {
	Dialect         string              `json:"dialect"`
	RolePermissions map[string][]string `json:"rolePermissions"`
	Seed            [][2]any            `json:"seed"`
	Steps           []storeStep         `json:"steps"`
}

// plainDeviceGroup is a device group in the shape the scenario records.
type plainDeviceGroup struct {
	ID                int64      `json:"id"`
	GUID              string     `json:"guid"`
	Name              string     `json:"name"`
	Note              *string    `json:"note"`
	TeamID            *string    `json:"team_id"`
	SourceType        string     `json:"source_type"`
	TagFilter         string     `json:"tag_filter"`
	MemberCount       *int       `json:"member_count"`
	AllowedUsers      []string   `json:"allowed_users"`
	AllowedGroups     []string   `json:"allowed_groups"`
	AllowedUserGroups []GroupRef `json:"allowed_user_groups"`
	CreatedAtType     string     `json:"created_at_type"`
}

var (
	sqliteTime = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$`)
	isoTime    = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$`)
)

// plainGroup converts a group and checks its creation time has the form the
// database's Node adapter gave: text on SQLite, a date on PostgreSQL (an ISO
// string once it is JSON).
func plainGroup(t *testing.T, db *store.DB, g *DeviceGroup) *plainDeviceGroup {
	t.Helper()
	if g == nil {
		return nil
	}
	kind, form := "string", sqliteTime
	if db.IsPostgres() {
		kind, form = "date", isoTime
	}
	if g.CreatedAt == nil || !form.MatchString(*g.CreatedAt) {
		t.Fatalf("device group %s: created_at %v does not look like a %s time", g.GUID, g.CreatedAt, db.Dialect())
	}
	return &plainDeviceGroup{
		ID: g.ID, GUID: g.GUID, Name: g.Name, Note: g.Note, TeamID: g.TeamID, SourceType: g.SourceType,
		TagFilter: g.TagFilter, MemberCount: g.MemberCount, AllowedUsers: g.AllowedUsers,
		AllowedGroups: g.AllowedGroups, AllowedUserGroups: g.AllowedUserGroups, CreatedAtType: kind,
	}
}

func loadScenario(t *testing.T, db *store.DB) storeScenario {
	t.Helper()
	name := "store_" + db.Dialect().String()
	if _, err := os.Stat(filepath.Join("testdata", name+".json.gz")); err != nil {
		t.Fatalf("no recorded scenario for %s: %v", db.Dialect(), err)
	}
	var scenario storeScenario
	loadVectors(t, name, &scenario)
	if scenario.Dialect != db.Dialect().String() || len(scenario.Steps) < 200 {
		t.Fatalf("scenario %s: dialect %q, %d steps", name, scenario.Dialect, len(scenario.Steps))
	}
	return scenario
}

// seedScenario creates the tables and the scenario's rows. The two
// "Default" groups come first, as on a database the Node console started on.
func seedScenario(t *testing.T, db *store.DB, scenario storeScenario) {
	t.Helper()
	createSchema(t, db)
	mustExec(t, db, `INSERT INTO user_groups (guid, name, note) VALUES (?, ?, ?)`, "fresh-ug", "Default", "Default user group")
	mustExec(t, db, `INSERT INTO device_groups (guid, name, note) VALUES (?, ?, ?)`, "fresh-dg", "Default", "Default device group")
	for _, statement := range scenario.Seed {
		query, _ := statement[0].(string)
		raw, _ := statement[1].([]any)
		args := make([]any, len(raw))
		for i, arg := range raw {
			if f, ok := arg.(float64); ok {
				arg = int64(f)
			}
			args[i] = arg
		}
		mustExec(t, db, query, args...)
	}
}

func TestOracleStore(t *testing.T) {
	withNodeTables(t)
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		scenario := loadScenario(t, db)
		seedScenario(t, db, scenario)
		ctx := context.Background()
		s := New(db, &fakeRoles{permissions: scenario.RolePermissions}, Options{DeviceScopeDefault: "open"})

		// UM10, UM11: Go caps a stored "group:<key>" row at the highest
		// ceiling among its sections and saves the rows of an account whose
		// role it does not know (at none); Node capped at the group's first
		// section and failed. Saves and stored rows are compared without the
		// group rows, and the rows of the unknown role's account not at all.
		errored, differ := 0, 0
		unknownRole := map[string]bool{}
		for i, step := range scenario.Steps {
			s.ClearRoleCache()
			s.InvalidateDeviceScopeDefault()
			label := "step " + jsJSON(t, i) + " " + step.Op

			var result any
			var err error
			found, superAdmin := true, false
			switch step.Op {
			case "groups":
				var groups []DeviceGroup
				groups, err = s.AllDeviceGroups(ctx)
				plain := []*plainDeviceGroup{}
				for j := range groups {
					plain = append(plain, plainGroup(t, db, &groups[j]))
				}
				result = plain
			case "group":
				var group *DeviceGroup
				group, err = s.DeviceGroupByGUID(ctx, step.GUID)
				result = plainGroup(t, db, group)
			case "userGroups":
				var groups []UserGroup
				groups, err = s.UserGroupsForUser(ctx, step.UserID)
				refs := []GroupRef{}
				for _, g := range groups {
					refs = append(refs, GroupRef{GUID: g.GUID, Name: g.Name})
				}
				result = refs
			case "resolveUser":
				id, _ := step.ID.(float64)
				var user *PolicyUser
				if user, err = s.PolicyUserByID(ctx, int64(id)); err == nil {
					if found = user != nil; found {
						superAdmin = IsSuperAdminRole(user.Role)
						var policy *Policy
						if policy, err = s.ResolveForUser(ctx, *user); err == nil {
							result = jsJSON(t, policy)
						}
					}
				}
			case "resolveGroup":
				var group *UserGroup
				if group, err = s.UserGroupByGUID(ctx, step.GUID); err == nil {
					if found = group != nil; found {
						var policy *Policy
						if policy, err = s.ResolveForGroup(ctx, group.GUID); err == nil {
							result = jsJSON(t, policy)
						}
					}
				}
			case "loadPolicy":
				c := s.Load(ctx, Session{UserID: step.UserID, HasUser: true, Role: step.Role})
				if (c.PolicyErr != nil) != step.LoadError {
					t.Fatalf("%s: policy load error %v, Node: %v", label, c.PolicyErr, step.LoadError)
				}
				if c.Policy != nil {
					result = jsJSON(t, c.Policy)
				}
			case "rawPolicies":
				id, _ := step.ID.(string)
				var raw struct {
					Sections []SectionRow `json:"sections"`
					Features []FeatureRow `json:"features"`
				}
				if raw.Sections, err = s.PanelAccessPolicies(ctx, step.Type, id); err == nil {
					raw.Features, err = s.ClientFeaturePolicies(ctx, step.Type, id)
				}
				result = raw
			case "savePolicy":
				id, _ := step.ID.(string)
				body, _ := step.Body.(map[string]any)
				var putBack func()
				if step.Type == SubjectGroup && step.Error {
					putBack = keepGroupPolicy(t, s, id)
				}
				result, err = s.SavePolicy(ctx, step.Type, id, body["sections"], body["features"])
				if putBack != nil && err == nil {
					putBack()
				}
			case "setSetting":
				mustExec(t, db, `INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
					scopeDefaultSetting, step.Value)
				continue
			case "setEnv":
				s.scopeEnvRestricted = step.Value == scopeRestricted
				continue
			case "scope":
				var scope *DeviceScope
				scope, err = s.DeviceScopeForUser(ctx, step.User.scopeUser(), toDevices(step.Devices))
				result = scope.IDs()
			case "count":
				result, err = s.CountEffectiveScope(ctx, step.User.scopeUser(), toDevices(step.Devices))
			case "accessContext":
				result = s.UserAccessContext(ctx, step.User.scopeUser()).UserGroups
			case "canAccess":
				var all []Device
				if step.All != nil {
					all = toDevices(step.All)
				}
				result, err = s.UserCanAccessDevice(ctx, step.User.scopeUser(), &toDevices([]oracleDevice{*step.Device})[0], all)
			case "enrich":
				var groups []DeviceGroup
				groups, err = s.AllDeviceGroups(ctx)
				counts := map[string]int{}
				for _, g := range s.EnrichGroups(ctx, groups, toDevices(step.Devices)) {
					counts[g.GUID] = *g.MemberCount
				}
				var want map[string]int
				if jsonErr := json.Unmarshal(step.Result, &want); jsonErr != nil || err != nil || !reflect.DeepEqual(counts, want) {
					t.Fatalf("%s: member counts %v (%v), Node says %s", label, counts, err, step.Result)
				}
				continue
			case "userFolderIds":
				result, err = s.UserFolderIDs(ctx, step.Username)
			case "syncFolders":
				result, err = s.SyncUserFolderAccess(ctx, step.Username, step.FolderID)
			case "grantIds":
				result, err = s.UserPeerGrantIDs(ctx, step.UserID)
			case "syncGrants":
				result, err = s.SyncUserPeerGrants(ctx, step.UserID, step.PeerIDs)
			default:
				t.Fatalf("%s: unknown step", label)
			}

			id, _ := step.ID.(string)
			if step.Op == "savePolicy" && step.Error && step.Status == nil && err == nil && step.Type == SubjectUser {
				if user, _ := s.PolicyUserByID(ctx, mustAtoi(id)); user != nil && scenario.RolePermissions[user.Role] == nil && !IsSuperAdminRole(user.Role) {
					unknownRole[id] = true
					differ++
					continue
				}
			}
			// UM2: a user group's policy is stored as given, where Node
			// failed for every group (DECISIONS A70). The group's rows were
			// put back, so the steps after it see what Node had.
			if _, isSave := result.(SavedPolicy); step.Op == "savePolicy" && step.Error && step.Status == nil && err == nil &&
				step.Type == SubjectGroup && isSave {
				differ++
				continue
			}
			if step.Op == "rawPolicies" && unknownRole[id] {
				differ++
				continue
			}
			if (step.Op == "savePolicy" || step.Op == "rawPolicies") && err == nil && !step.Error {
				if got := jsJSON(t, result); got != string(step.Result) {
					if g, w := withoutGroupRows(t, got), withoutGroupRows(t, string(step.Result)); g != w {
						t.Fatalf("%s without the group rows\n got  %s\n want %s", label, g, w)
					}
					differ++
					continue
				}
			}
			if step.Error {
				errored++
				if err == nil {
					t.Fatalf("%s: Node failed, Go answered %s", label, jsJSON(t, result))
				}
				var se *StatusError
				if step.Status != nil && (!errors.As(err, &se) || se.Status != *step.Status || se.Message != *step.Message) {
					t.Fatalf("%s: error %v, Node answers %d %q", label, err, *step.Status, *step.Message)
				}
				if step.Status == nil && ErrorStatus(err) != 500 {
					t.Fatalf("%s: error %v has status %d, Node has none", label, err, ErrorStatus(err))
				}
				continue
			}
			if err != nil {
				t.Fatalf("%s: %v", label, err)
			}
			if step.Found != nil && *step.Found != found {
				t.Fatalf("%s: found %v, Node: %v", label, found, *step.Found)
			}
			want := string(step.Result)
			if want == "" {
				want = "null"
			}
			if superAdmin {
				// RV13: no section row lowers a super admin (DECISIONS A92)
				var text string
				if json.Unmarshal(step.Result, &text) == nil {
					want = jsJSON(t, superAdminPolicy(text))
				}
			}
			if got := jsJSON(t, result); got != want {
				t.Fatalf("%s %s\n got  %s\n want %s", label, jsJSON(t, step.User), got, want)
			}
		}
		if errored < 3 {
			t.Fatalf("only %d failing steps ran", errored)
		}
		if differ == 0 || len(unknownRole) == 0 {
			t.Errorf("%d steps compared without group rows, %d accounts with an unknown role", differ, len(unknownRole))
		}
	})
}

// withoutGroupRows drops the "group:<key>" rows from the sections of a
// saved or stored policy in JSON.
func withoutGroupRows(t *testing.T, raw string) string {
	t.Helper()
	var policy struct {
		Sections []SectionRow `json:"sections"`
		Features []FeatureRow `json:"features"`
	}
	if err := json.Unmarshal([]byte(raw), &policy); err != nil {
		return raw
	}
	kept := []SectionRow{}
	for _, row := range policy.Sections {
		if !strings.HasPrefix(row.ResourceKey, groupKeyPrefix) {
			kept = append(kept, row)
		}
	}
	policy.Sections = kept
	if policy.Features == nil {
		policy.Features = []FeatureRow{}
	}
	return jsJSON(t, policy)
}

// keepGroupPolicy reads a user group's stored rows and returns a function
// that writes them back as they were.
func keepGroupPolicy(t *testing.T, s *Service, guid string) func() {
	t.Helper()
	ctx := context.Background()
	sections, err := s.PanelAccessPolicies(ctx, SubjectGroup, guid)
	if err != nil {
		t.Fatal(err)
	}
	features, err := s.ClientFeaturePolicies(ctx, SubjectGroup, guid)
	if err != nil {
		t.Fatal(err)
	}
	return func() {
		t.Helper()
		if err := errors.Join(s.setPanelAccessPolicies(ctx, SubjectGroup, guid, sections), s.setClientFeaturePolicies(ctx, SubjectGroup, guid, features)); err != nil {
			t.Fatal(err)
		}
	}
}

// mustAtoi reads an account id of the scenario, 0 for one that is no
// number.
func mustAtoi(s string) int64 {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return n
}
