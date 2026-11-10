package access

import (
	"encoding/json"
	"testing"
)

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
		{"dashboard", has, false, LevelRead},                                           // no write permission exists
		{"reports", newPermissionSet([]string{"metrics.view"}).Has, false, LevelRead},  // no write permission exists
		{"devices", newPermissionSet([]string{"device.view"}).Has, false, LevelRead},   // read only
		{"devices", newPermissionSet([]string{"device.edit"}).Has, false, LevelWrite},  // write does not need read
		{"toolkit", newPermissionSet([]string{"server.config"}).Has, false, LevelNone}, // the first toolkit entry decides
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

	// "toolkit" is listed twice; lookups find the first entry.
	count := 0
	for _, entry := range Catalog {
		if entry.Key == "toolkit" {
			count++
		}
	}
	first, _ := CatalogEntry("toolkit")
	if count != 2 || first.Group != "operations" || first.ReadPermission != "device.connect" || first.WritePermission != "device.edit" {
		t.Errorf("toolkit: %d entries, lookup finds %+v", count, first)
	}
	if len(Catalog) != 34 || len(catalogKeys()) != 33 {
		t.Errorf("catalog has %d entries and %d keys", len(Catalog), len(catalogKeys()))
	}

	// Sections without a write permission say null in JSON.
	for _, key := range []string{"dashboard", "activity", "reports"} {
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

			// The section's own permissions follow the level on its paths.
			entry, _ := CatalogEntry(key)
			c := &Context{UserID: 1, HasUser: true, Role: "nobody", Section: key, Policy: policy, Loaded: true, Permissions: PermissionSet{}}
			if entry.WritePermission != "" {
				if got := c.HasPermission(string(entry.WritePermission)); got != wantWrite {
					t.Errorf("%s at %s: write permission %s granted %v, want %v", key, level, entry.WritePermission, got, wantWrite)
				}
			}
			if entry.ReadPermission != entry.WritePermission {
				if got := c.HasPermission(string(entry.ReadPermission)); got != wantRead {
					t.Errorf("%s at %s: read permission %s granted %v, want %v", key, level, entry.ReadPermission, got, wantRead)
				}
			}
		}
	}
	if AccessAllows(nil, "devices", LevelNone) || AccessAllows(&Policy{}, "devices", LevelNone) {
		t.Error("without a policy or without the section nothing is allowed")
	}
}
