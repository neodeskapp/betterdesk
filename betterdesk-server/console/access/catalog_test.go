package access

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"testing"
)

// The Go API serves the same section list at GET /api/panel/catalog
// (package api, panelSectionCatalog); the Node console loaded it at start
// and looked sections up in it. The two lists must stay the same, in the
// same order, with the same permissions. Jest: panelCatalogParity.test.js.
func TestCatalogMatchesTheAPI(t *testing.T) {
	read := func(parts ...string) string {
		t.Helper()
		body, err := os.ReadFile(filepath.Join(append([]string{"..", ".."}, parts...)...))
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	constants := map[string]string{}
	for _, m := range regexp.MustCompile(`(Perm\w+)\s*=\s*"([^"]+)"`).FindAllStringSubmatch(read("auth", "permissions.go"), -1) {
		constants[m[1]] = m[2]
	}
	api := read("api", "panel_catalog.go")
	line := regexp.MustCompile(`\{Key: "([^"]+)", Label: "([^"]+)", Group: "([^"]+)"(?:, ReadPermission: auth\.(\w+))?(?:, WritePermission: auth\.(\w+))?\}`)
	var fromAPI []Section
	for _, m := range line.FindAllStringSubmatch(api, -1) {
		fromAPI = append(fromAPI, Section{m[1], m[2], m[3], Permission(constants[m[4]]), Permission(constants[m[5]])})
	}
	if len(fromAPI) < 20 || !reflect.DeepEqual(fromAPI, Catalog) {
		t.Errorf("the API's catalog differs:\n api     %v\n console %v", fromAPI, Catalog)
	}
	var groups []SectionGroup
	for _, m := range regexp.MustCompile(`\{"key": "([^"]+)", "label": "([^"]+)"\}`).FindAllStringSubmatch(api, -1) {
		groups = append(groups, SectionGroup{m[1], m[2]})
	}
	if !reflect.DeepEqual(groups, SectionGroups) {
		t.Errorf("the API's groups differ: %v", groups)
	}
}

func TestNormalizeLevel(t *testing.T) {
	cases := []struct {
		in   any
		want Level
	}{
		{"write", LevelWrite}, {"read", LevelRead}, {"none", LevelNone},
		{" Write ", LevelWrite}, {"READ", LevelRead}, {"\twrite\n", LevelWrite},
		{"unexpected", LevelNone}, {"", LevelNone}, {nil, LevelNone}, {true, LevelNone},
		{float64(2), LevelWrite}, {float64(7), LevelWrite}, {float64(1), LevelRead},
		{float64(0), LevelNone}, {float64(-1), LevelNone}, {1.5, LevelNone},
		{2, LevelWrite}, {1, LevelRead}, {json.Number("2"), LevelWrite},
		// Text that looks like a number is text, and names no level.
		{"2", LevelNone}, {"1", LevelNone},
		{LevelWrite, LevelWrite}, {Level("WRITE"), LevelWrite}, {Level("admin"), LevelNone},
	}
	for _, c := range cases {
		if got := NormalizeLevel(c.in); got != c.want {
			t.Errorf("NormalizeLevel(%#v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCompareLevels(t *testing.T) {
	if CompareLevels("read", "write") >= 0 || CompareLevels("write", "none") <= 0 || CompareLevels("read", " READ ") != 0 {
		t.Fatal("levels do not order none < read < write")
	}
	if CompareLevels("bogus", "none") != 0 {
		t.Fatal("an unknown level must rank as none")
	}
}

// Jest: panelAccessCatalog.test.js, "maps legacy permissions to a read/write ceiling".
func TestLegacySectionCeiling(t *testing.T) {
	has := newPermissionSet([]string{"device.view", "device.edit"}).Has
	never := func(string) bool { return false }
	cases := []struct {
		key        string
		has        func(string) bool
		superAdmin bool
		want       Level
	}{
		{"devices", has, false, LevelWrite},
		{"activity", has, false, LevelNone},
		{"devices", never, true, LevelWrite},
		{"dashboard", has, false, LevelRead},                                          // no write permission exists
		{"activity", newPermissionSet([]string{"audit.view"}).Has, false, LevelRead},  // no write permission exists
		{"devices", newPermissionSet([]string{"device.view"}).Has, false, LevelRead},  // read only
		{"devices", newPermissionSet([]string{"device.edit"}).Has, false, LevelWrite}, // write does not need read
		{"toolkit", newPermissionSet([]string{"server.config"}).Has, false, LevelNone},
		{"toolkit", newPermissionSet([]string{"device.connect"}).Has, false, LevelRead},
		{"nope", has, false, LevelNone},
		{"nope", never, true, LevelWrite},
	}
	for _, c := range cases {
		if got := LegacySectionCeiling(c.key, c.has, c.superAdmin); got != c.want {
			t.Errorf("LegacySectionCeiling(%q, super admin %v) = %q, want %q", c.key, c.superAdmin, got, c.want)
		}
	}
}

// Jest: panelAccessCatalog.test.js, "contains stable entries for every client feature".
func TestCatalogContents(t *testing.T) {
	for _, key := range []string{"commercialization", "settings", "devices", "users"} {
		if _, ok := CatalogEntry(key); !ok {
			t.Errorf("catalog has no section %q", key)
		}
	}
	features := map[string]bool{}
	for _, f := range ClientFeatures {
		features[f.Key] = f.HardLock
	}
	for _, key := range []string{"file_transfer", "clipboard", "audio", "tunnel"} {
		if _, ok := features[key]; !ok {
			t.Errorf("no client feature %q", key)
		}
	}
	if !features["client_actions"] {
		t.Error("client_actions must be hard locked")
	}

	// Every key is listed once; toolkit is in the operations group.
	toolkit, _ := CatalogEntry("toolkit")
	if toolkit.Group != "operations" || toolkit.ReadPermission != "device.connect" || toolkit.WritePermission != "device.edit" {
		t.Errorf("toolkit: lookup finds %+v", toolkit)
	}
	if len(Catalog) != 33 || len(catalogKeys()) != 33 {
		t.Errorf("catalog has %d entries and %d keys", len(Catalog), len(catalogKeys()))
	}

	// Sections without a write permission say null in JSON.
	for _, key := range []string{"dashboard", "activity"} {
		entry, _ := CatalogEntry(key)
		body, err := json.Marshal(entry)
		if err != nil {
			t.Fatal(err)
		}
		var decoded map[string]any
		if err := json.Unmarshal(body, &decoded); err != nil {
			t.Fatal(err)
		}
		if value, present := decoded["writePermission"]; !present || value != nil {
			t.Errorf("%s: writePermission is %v in %s, want null", key, value, body)
		}
	}
	view := GetCatalog()
	if len(view.Levels) != 3 || view.Levels[0] != LevelNone || view.Levels[2] != LevelWrite || len(view.Groups) != 6 || len(view.Features) != 10 {
		t.Errorf("catalog view: %+v", view)
	}
}

// Every catalog section at every level: what the guard lets through.
func TestEverySectionAtEveryLevel(t *testing.T) {
	for _, key := range catalogKeys() {
		for _, level := range Levels {
			policy := policyWith(key, level)
			wantRead := level == LevelRead || level == LevelWrite
			wantWrite := level == LevelWrite
			if got := AccessAllows(policy, key, LevelRead); got != wantRead {
				t.Errorf("%s at %s: read allowed %v, want %v", key, level, got, wantRead)
			}
			if got := AccessAllows(policy, key, LevelWrite); got != wantWrite {
				t.Errorf("%s at %s: write allowed %v, want %v", key, level, got, wantWrite)
			}
			if !AccessAllows(policy, key, LevelNone) {
				t.Errorf("%s at %s: a section with a level must satisfy none", key, level)
			}

			// The section's own permissions follow the level on its paths
			// when the role holds them, and are never granted when it
			// does not.
			entry, _ := CatalogEntry(key)
			holds := newPermissionSet([]string{string(entry.ReadPermission), string(entry.WritePermission)})
			c := &Context{UserID: 1, HasUser: true, Role: "nobody", Section: key, Policy: policy, Loaded: true, Permissions: holds}
			lacks := &Context{UserID: 1, HasUser: true, Role: "nobody", Section: key, Policy: policy, Loaded: true, Permissions: PermissionSet{}}
			if entry.WritePermission != "" {
				// A permission that is the read permission too needs read
				// only (S14); the guard asks write for writes.
				want := wantWrite
				if entry.WritePermission == entry.ReadPermission {
					want = wantRead
				}
				if got := c.HasPermission(string(entry.WritePermission)); got != want {
					t.Errorf("%s at %s: write permission %s granted %v, want %v", key, level, entry.WritePermission, got, want)
				}
				if lacks.HasPermission(string(entry.WritePermission)) {
					t.Errorf("%s at %s: write permission %s granted to a role without it", key, level, entry.WritePermission)
				}
			}
			if entry.ReadPermission != entry.WritePermission {
				if got := c.HasPermission(string(entry.ReadPermission)); got != wantRead {
					t.Errorf("%s at %s: read permission %s granted %v, want %v", key, level, entry.ReadPermission, got, wantRead)
				}
				if lacks.HasPermission(string(entry.ReadPermission)) {
					t.Errorf("%s at %s: read permission %s granted to a role without it", key, level, entry.ReadPermission)
				}
			}
		}
	}
	if AccessAllows(nil, "devices", LevelNone) || AccessAllows(&Policy{}, "devices", LevelNone) {
		t.Error("without a policy or without the section nothing is allowed")
	}
}
