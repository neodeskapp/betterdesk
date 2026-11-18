package access

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// Jest: panel-access.ceiling.test.js, "requestHasPermission (audit H7)" and
// "clamps resolved sections to the role ceiling". The save half is in
// TestPolicyStore.
func TestRoleCeilingH7(t *testing.T) {
	gaPermissions := newPermissionSet([]string{"user.view", "user.edit", "user.create"})
	for _, c := range []struct {
		name       string
		ctx        Context
		permission string
		want       bool
	}{
		{"global_admin gets no server.config from settings at write",
			Context{Role: "global_admin", Section: "settings", Policy: policyWith("settings", LevelWrite), Loaded: true, Permissions: gaPermissions}, "server.config", false},
		{"a permission the role holds still passes",
			Context{Role: "global_admin", Section: "users", Policy: policyWith("users", LevelWrite), Loaded: true, Permissions: gaPermissions}, "user.edit", true},
		{"the section narrows: read refuses the write permission",
			Context{Role: "operator", Section: "devices", Policy: policyWith("devices", LevelRead), Loaded: true, Permissions: newPermissionSet([]string{"device.view", "device.edit"})}, "device.edit", false},
		{"and lets the read permission through",
			Context{Role: "operator", Section: "devices", Policy: policyWith("devices", LevelRead), Loaded: true, Permissions: newPermissionSet([]string{"device.view", "device.edit"})}, "device.view", true},
		{"super admins pass at none",
			Context{Role: "super_admin", Section: "settings", Policy: policyWith("settings", LevelNone), Loaded: true, Permissions: PermissionSet{}}, "server.config", true},
		{"without a loaded context the built-in map decides: viewer has device.view",
			Context{Role: "viewer"}, "device.view", true},
		{"and not server.config", Context{Role: "viewer"}, "server.config", false},
	} {
		ctx := c.ctx
		ctx.UserID, ctx.HasUser = 1, true
		if got := ctx.HasPermission(c.permission); got != c.want {
			t.Errorf("%s: HasPermission(%q) = %v, want %v", c.name, c.permission, got, c.want)
		}
	}

	data := &fakeData{sections: map[string][]SectionRow{
		"user:5": {{ResourceKey: "settings", AccessLevel: "write"}, {ResourceKey: "users", AccessLevel: "write"}},
	}}
	s := fakeService(data, &fakeRoles{permissions: map[string][]string{"global_admin": {"user.view", "user.edit", "device.view"}}})
	policy, err := s.ResolveForUser(context.Background(), PolicyUser{ID: 5, Role: "global_admin"})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := policy.Sections.Get("settings"); got != (SectionAccess{LevelNone, SourceUser, LevelNone}) {
		t.Errorf("settings: %+v", got)
	}
	if got, _ := policy.Sections.Get("users"); got != (SectionAccess{LevelWrite, SourceUser, LevelWrite}) {
		t.Errorf("users: %+v", got)
	}
}

// A saved "group:<key>" row is capped at the ceiling of the first section of
// that catalog group (BUGS UM10). dashboard comes first in core and has no
// write permission, so core at write is stored as read for every role but
// the super admins, and devices then resolves to read although the role
// reaches write there.
func TestSavedGroupRowTakesTheFirstSectionsCeiling(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		createSchema(t, db)
		ctx := context.Background()
		mustExec(t, db, `INSERT INTO users (username, password_hash, role) VALUES (?, ?, ?)`, "op1", "x", "operator")
		mustExec(t, db, `INSERT INTO users (username, password_hash, role) VALUES (?, ?, ?)`, "ga1", "x", "global_admin")
		s := New(db, &fakeRoles{permissions: map[string][]string{
			"operator":     {"device.view", "device.edit"},
			"global_admin": {"user.view", "branding.edit", "server.attestation"},
		}}, Options{})
		write := func(key string) []any { return []any{map[string]any{"resource_key": key, "access_level": "write"}} }

		saved, err := s.SavePolicy(ctx, "user", "1", write("group:core"), nil)
		if err != nil || jsJSON(t, saved.Sections) != `[{"resource_key":"group:core","access_level":"read"}]` {
			t.Fatalf("operator, core at write: %s, %v", jsJSON(t, saved.Sections), err)
		}
		policy, err := s.ResolveForUser(ctx, PolicyUser{ID: 1, Role: "operator"})
		if err != nil {
			t.Fatal(err)
		}
		if got, _ := policy.Sections.Get("devices"); got != (SectionAccess{LevelRead, SourceUser, LevelWrite}) {
			t.Errorf("operator devices: %+v", got)
		}

		// system starts with languages (server.config): a role that reaches
		// server-attestation only gets none for the whole group.
		if saved, err = s.SavePolicy(ctx, "user", "2", write("group:system"), nil); err != nil ||
			jsJSON(t, saved.Sections) != `[{"resource_key":"group:system","access_level":"none"}]` {
			t.Fatalf("global_admin, system at write: %s, %v", jsJSON(t, saved.Sections), err)
		}
		if policy, err = s.ResolveForUser(ctx, PolicyUser{ID: 2, Role: "global_admin"}); err != nil {
			t.Fatal(err)
		}
		if got, _ := policy.Sections.Get("server-attestation"); got != (SectionAccess{LevelNone, SourceUser, LevelWrite}) {
			t.Errorf("global_admin server-attestation: %+v", got)
		}
	})
}

// Jest: panelAccessPolicyService.test.js, "applies user overrides over group
// and role defaults".
func TestResolveForUserPrecedence(t *testing.T) {
	data := &fakeData{
		sections: map[string][]SectionRow{
			"user:7":    {{ResourceKey: "devices", AccessLevel: "none"}},
			"group:ops": {{ResourceKey: "remote", AccessLevel: "read"}},
		},
		features: map[string][]FeatureRow{
			"user:7":    {{FeatureKey: "clipboard", Enabled: true}},
			"group:ops": {{FeatureKey: "clipboard", Enabled: false}},
		},
		userGroups: []UserGroup{{GUID: "ops", Name: "Operators"}},
	}
	roles := &fakeRoles{permissions: map[string][]string{
		"operator": {"device.view", "device.edit", "remote_target.view", "remote_target.connect"},
	}}
	policy, err := fakeService(data, roles).ResolveForUser(context.Background(), PolicyUser{ID: 7, Role: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	section := func(key string) SectionAccess {
		entry, ok := policy.Sections.Get(key)
		if !ok {
			t.Fatalf("no section %q", key)
		}
		return entry
	}
	if got := section("devices"); got != (SectionAccess{Access: LevelNone, Source: SourceUser, RoleCeiling: LevelWrite}) {
		t.Errorf("devices: %+v", got)
	}
	if got := section("remote"); got != (SectionAccess{Access: LevelRead, Source: SourceGroup, RoleCeiling: LevelWrite}) {
		t.Errorf("remote: %+v", got)
	}
	if got := section("folders"); got != (SectionAccess{Access: LevelWrite, Source: SourceRole, RoleCeiling: LevelWrite}) {
		t.Errorf("folders: %+v", got)
	}
	if got := section("users"); got != (SectionAccess{Access: LevelNone, Source: SourceRole, RoleCeiling: LevelNone}) {
		t.Errorf("users: %+v", got)
	}
	if clipboard, _ := policy.Features.Get("clipboard"); clipboard != (FeatureAccess{Enabled: true, Source: SourceUser}) {
		t.Errorf("clipboard: %+v", clipboard)
	}
	if locked, _ := policy.Features.Get("client_actions"); locked != (FeatureAccess{Enabled: false, Source: SourceDefault, HardLock: true}) {
		t.Errorf("client_actions: %+v", locked)
	}
	if policy.SubjectType != "user" || policy.SubjectID != "7" || len(policy.Groups) != 1 || policy.Groups[0] != (GroupRef{GUID: "ops", Name: "Operators"}) {
		t.Errorf("policy head: %+v", policy)
	}
}

func TestMergeUserPolicyRules(t *testing.T) {
	ceiling := ceilingFor(newPermissionSet([]string{"device.view", "user.view", "user.edit", "device.connect"}))
	cases := []struct {
		name    string
		rows    userPolicyRows
		section string
		want    SectionAccess
	}{
		{"the role's ceiling alone", userPolicyRows{}, "users",
			SectionAccess{LevelWrite, SourceRole, LevelWrite}},
		{"the account's row for the section wins",
			userPolicyRows{
				sections:      []SectionRow{{"users", "read"}, {"group:administration", "none"}},
				groupSections: []SectionRow{{"users", "write"}},
			}, "users", SectionAccess{LevelRead, SourceUser, LevelWrite}},
		{"then the account's row for the catalog group",
			userPolicyRows{
				sections:      []SectionRow{{"group:administration", "none"}},
				groupSections: []SectionRow{{"users", "write"}},
			}, "users", SectionAccess{LevelNone, SourceUser, LevelWrite}},
		{"then the highest row of its user groups",
			userPolicyRows{groupSections: []SectionRow{{"users", "none"}, {"group:administration", "read"}, {"users", "none"}}},
			"users", SectionAccess{LevelRead, SourceGroup, LevelWrite}},
		{"a group's none is a row too and beats the role",
			userPolicyRows{groupSections: []SectionRow{{"users", "none"}}},
			"users", SectionAccess{LevelNone, SourceGroup, LevelWrite}},
		{"a row cannot give more than the role",
			userPolicyRows{sections: []SectionRow{{"settings", "write"}}},
			"settings", SectionAccess{LevelNone, SourceUser, LevelNone}},
		{"a row is capped at the role's level",
			userPolicyRows{sections: []SectionRow{{"devices", "write"}}},
			"devices", SectionAccess{LevelRead, SourceUser, LevelRead}},
		{"a group row cannot give more than the role",
			userPolicyRows{groupSections: []SectionRow{{"group:system", "read"}}},
			"permissions", SectionAccess{LevelNone, SourceGroup, LevelNone}},
		{"the highest group row is capped too",
			userPolicyRows{groupSections: []SectionRow{{"devices", "write"}, {"devices", "none"}}},
			"devices", SectionAccess{LevelRead, SourceGroup, LevelRead}},
		{"a stored level that is none of the three is none",
			userPolicyRows{sections: []SectionRow{{"users", "admin"}}},
			"users", SectionAccess{LevelNone, SourceUser, LevelWrite}},
		{"a stored level in odd spelling counts",
			userPolicyRows{groupSections: []SectionRow{{"users", " READ "}}},
			"users", SectionAccess{LevelRead, SourceGroup, LevelWrite}},
		{"toolkit follows a row for its catalog group",
			userPolicyRows{sections: []SectionRow{{"group:operations", "none"}}},
			"toolkit", SectionAccess{LevelNone, SourceUser, LevelRead}},
		{"toolkit ignores a row for the commercialization group",
			userPolicyRows{sections: []SectionRow{{"group:commercialization", "none"}}},
			"toolkit", SectionAccess{LevelRead, SourceRole, LevelRead}},
		{"toolkit's own row wins and is capped",
			userPolicyRows{sections: []SectionRow{{"toolkit", "write"}, {"group:operations", "none"}}},
			"toolkit", SectionAccess{LevelRead, SourceUser, LevelRead}},
	}
	for _, c := range cases {
		policy := mergeUserPolicy("1", ceiling, c.rows)
		if got, _ := policy.Sections.Get(c.section); got != c.want {
			t.Errorf("%s: %s = %+v, want %+v", c.name, c.section, got, c.want)
		}
	}
}

func TestMergeFeatures(t *testing.T) {
	rows := userPolicyRows{
		features: []FeatureRow{{"audio", false}, {"client_actions", true}},
		groupFeatures: []FeatureRow{
			{"tunnel", false}, {"tunnel", true}, // any group that turns it on wins
			{"terminal", false}, {"terminal", false},
			{"audio", true}, // the account's own row wins
		},
	}
	policy := mergeUserPolicy("1", superAdminCeiling(), rows)
	for key, want := range map[string]FeatureAccess{
		"audio":          {Enabled: false, Source: SourceUser},
		"tunnel":         {Enabled: true, Source: SourceGroup},
		"terminal":       {Enabled: false, Source: SourceGroup},
		"clipboard":      {Enabled: true, Source: SourceDefault},
		"client_actions": {Enabled: false, Source: SourceUser, HardLock: true},
	} {
		if got, _ := policy.Features.Get(key); got != want {
			t.Errorf("%s: %+v, want %+v", key, got, want)
		}
	}

	group := mergeGroupPolicy("g", []SectionRow{{"group:core", "read"}, {"devices", "write"}, {"chat", "bogus"}},
		[]FeatureRow{{"audio", false}, {"client_actions", true}})
	for key, want := range map[string]SectionAccess{
		"devices":   {Access: LevelWrite, Source: SourceGroup},
		"dashboard": {Access: LevelRead, Source: SourceGroup},
		"chat":      {Access: LevelNone, Source: SourceGroup},
		"users":     {Access: LevelNone, Source: SourceUnset},
	} {
		if got, _ := group.Sections.Get(key); got != want {
			t.Errorf("group %s: %+v, want %+v", key, got, want)
		}
	}
	if got, _ := group.Features.Get("audio"); got != (FeatureAccess{Enabled: false, Source: SourceGroup}) {
		t.Errorf("group audio: %+v", got)
	}
	if got, _ := group.Features.Get("client_actions"); got != (FeatureAccess{Enabled: false, Source: SourceGroup, HardLock: true}) {
		t.Errorf("group client_actions: %+v", got)
	}
}

// The JSON of a policy: keys in catalog order with toolkit once, an
// account's policy with groups and role ceilings, a group's without.
func TestPolicyJSON(t *testing.T) {
	user := mergeUserPolicy("7", superAdminCeiling(), userPolicyRows{})
	body := jsJSON(t, user)
	wantStart := `{"subject_type":"user","subject_id":"7","sections":{"dashboard":{"access":"write","source":"role","role_ceiling":"write"},"devices":`
	if !strings.HasPrefix(body, wantStart) {
		t.Errorf("user policy starts with\n %s", body[:len(wantStart)+20])
	}
	if !strings.HasSuffix(body, `"client_actions":{"enabled":false,"source":"default","hard_lock":true}},"groups":[]}`) {
		t.Errorf("user policy ends with\n %s", body[len(body)-120:])
	}
	if got, want := user.Sections.Keys(), catalogKeys(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("section order: %v", got)
	}
	if strings.Count(body, `"toolkit":`) != 1 || strings.Index(body, `"toolkit":`) > strings.Index(body, `"commercialization":`) {
		t.Error("toolkit must appear once, at its first place in the catalog")
	}
	var decoded struct {
		Features map[string]json.RawMessage `json:"features"`
	}
	if err := json.Unmarshal([]byte(body), &decoded); err != nil || len(decoded.Features) != len(ClientFeatures) {
		t.Errorf("features: %v, %v", decoded.Features, err)
	}

	group := jsJSON(t, mergeGroupPolicy("ops", nil, nil))
	if !strings.HasPrefix(group, `{"subject_type":"group","subject_id":"ops","sections":{"dashboard":{"access":"none","source":"unset"},`) {
		t.Errorf("group policy starts with\n %s", group[:120])
	}
	if strings.Contains(group, `"groups"`) || strings.Contains(group, `role_ceiling`) {
		t.Error("a group's policy has neither groups nor role ceilings")
	}
}

func TestValidateSubject(t *testing.T) {
	for _, c := range []struct{ kind, id, want string }{
		{"user", "7", "7"}, {"group", "ops", "ops"}, {"user", " 7 ", "7"}, {"group", "a.b:c@d-e_f", "a.b:c@d-e_f"},
		{"group", strings.Repeat("x", 120), strings.Repeat("x", 120)},
	} {
		if got, err := ValidateSubject(c.kind, c.id); err != nil || got != c.want {
			t.Errorf("ValidateSubject(%q, %q) = %q, %v", c.kind, c.id, got, err)
		}
	}
	for _, c := range []struct{ kind, id, message string }{
		{"team", "7", "Invalid policy subject type"}, {"", "7", "Invalid policy subject type"}, {"USER", "7", "Invalid policy subject type"},
		{"user", "", "Invalid policy subject"}, {"user", "  ", "Invalid policy subject"}, {"group", "a b", "Invalid policy subject"},
		{"group", "a/b", "Invalid policy subject"}, {"group", strings.Repeat("x", 121), "Invalid policy subject"},
		{"group", "äbc", "Invalid policy subject"}, {"user", "7;drop", "Invalid policy subject"},
	} {
		_, err := ValidateSubject(c.kind, c.id)
		var se *StatusError
		if !errors.As(err, &se) || se.Status != 400 || se.Message != c.message {
			t.Errorf("ValidateSubject(%q, %q): %v, want 400 %q", c.kind, c.id, err, c.message)
		}
	}
}

func TestNormalizePolicyRows(t *testing.T) {
	sections := NormalizeSectionPolicies([]any{
		map[string]any{"resource_key": "devices", "access_level": "write"},
		map[string]any{"key": "users", "access": "read"},
		map[string]any{"resource_key": "devices", "access_level": "none"}, // a repeat
		map[string]any{"resource_key": "nope", "access_level": "write"},   // an unknown key
		map[string]any{"resource_key": " chat ", "access_level": " WRITE "},
		map[string]any{"resource_key": "group:core", "access_level": float64(1)},
		map[string]any{"resource_key": "group:nope", "access_level": "write"},
		map[string]any{"resource_key": "fleet", "access_level": nil, "access": "write"},
		map[string]any{"resource_key": "files", "access_level": "", "access": "write"},
		map[string]any{"resource_key": "reports"},
		"devices", nil, float64(3),
	})
	want := `[{"resource_key":"devices","access_level":"write"},{"resource_key":"users","access_level":"read"},` +
		`{"resource_key":"chat","access_level":"write"},{"resource_key":"group:core","access_level":"read"},` +
		`{"resource_key":"fleet","access_level":"write"},{"resource_key":"files","access_level":"none"},` +
		`{"resource_key":"reports","access_level":"none"}]`
	if got := jsJSON(t, sections); got != want {
		t.Errorf("sections:\n got  %s\n want %s", got, want)
	}
	for _, notAList := range []any{nil, "devices", float64(7), map[string]any{}} {
		if got := NormalizeSectionPolicies(notAList); got == nil || len(got) != 0 {
			t.Errorf("NormalizeSectionPolicies(%#v) = %v, want an empty list", notAList, got)
		}
		if got := NormalizeFeaturePolicies(notAList); got == nil || len(got) != 0 {
			t.Errorf("NormalizeFeaturePolicies(%#v) = %v, want an empty list", notAList, got)
		}
	}

	features := NormalizeFeaturePolicies([]any{
		map[string]any{"feature_key": "clipboard", "enabled": true},
		map[string]any{"key": "audio", "enabled": float64(1)},
		map[string]any{"key": "tunnel", "enabled": "true"},
		map[string]any{"key": "terminal", "enabled": "TRUE"},
		map[string]any{"key": "reboot", "enabled": float64(2)},
		map[string]any{"key": "privacy_mode"},
		map[string]any{"key": "clipboard", "enabled": false}, // a repeat
		map[string]any{"key": "nope", "enabled": true},       // an unknown key
		map[string]any{"key": "client_actions", "enabled": true},
	})
	wantFeatures := `[{"feature_key":"clipboard","enabled":true},{"feature_key":"audio","enabled":true},` +
		`{"feature_key":"tunnel","enabled":true},{"feature_key":"terminal","enabled":false},` +
		`{"feature_key":"reboot","enabled":false},{"feature_key":"privacy_mode","enabled":false},` +
		`{"feature_key":"client_actions","enabled":true}]`
	if got := jsJSON(t, features); got != wantFeatures {
		t.Errorf("features:\n got  %s\n want %s", got, wantFeatures)
	}
}

func TestResolveErrors(t *testing.T) {
	ctx := context.Background()
	roles := &fakeRoles{permissions: map[string][]string{"viewer": {"device.view"}}}

	// No role counts as viewer.
	s := fakeService(&fakeData{}, roles)
	policy, err := s.ResolveForUser(ctx, PolicyUser{ID: 3})
	if err != nil {
		t.Fatal(err)
	}
	if devices, _ := policy.Sections.Get("devices"); devices.Access != LevelRead || devices.RoleCeiling != LevelRead {
		t.Errorf("no role: devices %+v, want the viewer's read", devices)
	}

	// Super admins need no lookup.
	before := roles.calls
	policy, err = s.ResolveForUser(ctx, PolicyUser{ID: 1, Role: "admin"})
	if err != nil || roles.calls != before {
		t.Fatalf("admin: %v, %d lookups", err, roles.calls-before)
	}
	for _, key := range policy.Sections.Keys() {
		if entry, _ := policy.Sections.Get(key); entry.Access != LevelWrite || entry.RoleCeiling != LevelWrite {
			t.Errorf("admin: %s is %+v", key, entry)
		}
	}

	// A role whose permissions cannot be looked up has no policy.
	if _, err := s.ResolveForUser(ctx, PolicyUser{ID: 3, Role: "made_up"}); err == nil {
		t.Error("an unknown role resolved a policy")
	}
	// Nor does an account whose rows cannot be read.
	failing := fakeService(&fakeData{failing: "policies"}, roles)
	if _, err := failing.ResolveForUser(ctx, PolicyUser{ID: 3, Role: "viewer"}); !errors.Is(err, errFake) {
		t.Errorf("unreadable rows: %v", err)
	}
	failing = fakeService(&fakeData{failing: "userGroups"}, roles)
	if _, err := failing.ResolveForUser(ctx, PolicyUser{ID: 3, Role: "viewer"}); !errors.Is(err, errFake) {
		t.Errorf("unreadable user groups: %v", err)
	}

	var se *StatusError
	if _, err := s.ResolveForUser(ctx, PolicyUser{Role: "viewer"}); !errors.As(err, &se) || se.Status != 404 || se.Message != "User not found" {
		t.Errorf("no user: %v", err)
	}
	if _, err := s.ResolveForGroup(ctx, ""); !errors.As(err, &se) || se.Status != 404 || se.Message != "Group not found" {
		t.Errorf("no group: %v", err)
	}
}

func TestPolicyStore(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		createSchema(t, db)
		ctx := context.Background()
		mustExec(t, db, `INSERT INTO users (username, password_hash, role) VALUES (?, ?, ?)`, "op1", "x", "operator")
		mustExec(t, db, `INSERT INTO users (username, password_hash, role) VALUES (?, ?, ?)`, "norole", "x", nil)
		mustExec(t, db, `INSERT INTO user_groups (guid, name) VALUES (?, ?)`, "ops", "Operators")
		mustExec(t, db, `INSERT INTO user_groups (guid, name) VALUES (?, ?)`, "lab", "Lab")
		mustExec(t, db, `INSERT INTO user_group_members (user_group_id, user_id) VALUES (?, ?)`, 1, 1)
		mustExec(t, db, `INSERT INTO user_group_members (user_group_id, user_id) VALUES (?, ?)`, 2, 1)
		mustExec(t, db, `INSERT INTO users (username, password_hash, role) VALUES (?, ?, ?)`, "odd", "x", "made_up")
		s := New(db, &fakeRoles{permissions: map[string][]string{
			"operator": {"device.view", "device.edit", "user.view", "user.edit"}, "viewer": {"device.view"},
		}}, Options{})

		// Nothing stored yet.
		if rows, err := s.PanelAccessPolicies(ctx, "user", "1"); err != nil || rows == nil || len(rows) != 0 {
			t.Fatalf("no rows: %v, %v", rows, err)
		}

		// A row above the role's level is stored at the role's level; a
		// "group:" row is capped at the first section of its catalog group.
		saved, err := s.SavePolicy(ctx, "user", " 1 ",
			[]any{
				map[string]any{"resource_key": "users", "access_level": "write"},
				map[string]any{"resource_key": "devices", "access_level": "read"},
				map[string]any{"resource_key": "settings", "access_level": "write"},
				map[string]any{"resource_key": "group:operations", "access_level": "write"},
				map[string]any{"resource_key": "group:system", "access_level": "read"},
				map[string]any{"resource_key": "nope", "access_level": "write"},
			},
			[]any{
				map[string]any{"feature_key": "tunnel", "enabled": true},
				map[string]any{"feature_key": "audio", "enabled": false},
			})
		if err != nil {
			t.Fatal(err)
		}
		if got := jsJSON(t, saved); got != `{"sections":[{"resource_key":"users","access_level":"write"},{"resource_key":"devices","access_level":"read"},`+
			`{"resource_key":"settings","access_level":"none"},{"resource_key":"group:operations","access_level":"write"},{"resource_key":"group:system","access_level":"none"}],`+
			`"features":[{"feature_key":"tunnel","enabled":true},{"feature_key":"audio","enabled":false}]}` {
			t.Errorf("saved: %s", got)
		}
		// Read back ordered by key, under the trimmed subject id.
		sections, _ := s.PanelAccessPolicies(ctx, "user", "1")
		features, _ := s.ClientFeaturePolicies(ctx, "user", "1")
		if got := jsJSON(t, sections); got != `[{"resource_key":"devices","access_level":"read"},{"resource_key":"group:operations","access_level":"write"},`+
			`{"resource_key":"group:system","access_level":"none"},{"resource_key":"settings","access_level":"none"},{"resource_key":"users","access_level":"write"}]` {
			t.Errorf("stored sections: %s", got)
		}
		if got := jsJSON(t, features); got != `[{"feature_key":"audio","enabled":false},{"feature_key":"tunnel","enabled":true}]` {
			t.Errorf("stored features: %s", got)
		}
		var stamped int
		if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM panel_access_policies WHERE updated_at IS NOT NULL`).Scan(&stamped); err != nil || stamped != 5 {
			t.Errorf("rows with a write time: %d, %v", stamped, err)
		}

		// The subject id is read as Number.parseInt reads it. An account
		// without a role, and an id that names no account, count as viewers.
		viewerSave := []any{map[string]any{"resource_key": "devices", "access_level": "write"}}
		for _, id := range []string{"2", "2abc", "99", "-1"} {
			saved, err := s.SavePolicy(ctx, "user", id, viewerSave, nil)
			if err != nil || jsJSON(t, saved.Sections) != `[{"resource_key":"devices","access_level":"read"}]` {
				t.Errorf("save for %q: %s, %v", id, jsJSON(t, saved.Sections), err)
			}
		}
		if saved, err := s.SavePolicy(ctx, "user", "1.9xyz", viewerSave, nil); err != nil || jsJSON(t, saved.Sections) != `[{"resource_key":"devices","access_level":"write"}]` {
			t.Errorf(`save for "1.9xyz" (account 1, an operator): %s, %v`, jsJSON(t, saved.Sections), err)
		}
		mustExec(t, db, `DELETE FROM panel_access_policies WHERE subject_id <> ?`, "1")
		_, err = s.SavePolicy(ctx, "user", "abc", viewerSave, nil)
		if db.IsPostgres() != (err != nil) {
			t.Errorf("save for an id that is no number: %v (an error only on PostgreSQL)", err)
		}
		// A role whose permissions cannot be looked up stores nothing.
		if _, err := s.SavePolicy(ctx, "user", "3", viewerSave, []any{map[string]any{"feature_key": "audio", "enabled": true}}); err == nil || ErrorStatus(err) != 500 {
			t.Errorf("save for an account with an unknown role: %v", err)
		}
		if rows, _ := s.PanelAccessPolicies(ctx, "user", "3"); len(rows) != 0 {
			t.Errorf("rows stored for an account with an unknown role: %v", rows)
		}
		if rows, _ := s.ClientFeaturePolicies(ctx, "user", "3"); len(rows) != 0 {
			t.Errorf("features stored for an account with an unknown role: %v", rows)
		}

		// A flag is stored as each database's Node adapter stored it.
		var raw any
		if err := db.QueryRow(ctx, `SELECT enabled FROM client_feature_policies WHERE feature_key = ?`, "tunnel").Scan(&raw); err != nil {
			t.Fatal(err)
		}
		if db.IsPostgres() {
			if raw != true {
				t.Errorf("PostgreSQL stores %#v, want true", raw)
			}
		} else {
			if raw != int64(1) {
				t.Errorf("SQLite stores %#v, want 1", raw)
			}
			// Any other number than 0 reads as on.
			mustExec(t, db, `UPDATE client_feature_policies SET enabled = 5 WHERE feature_key = ?`, "audio")
			if features, _ := s.ClientFeaturePolicies(ctx, "user", "1"); !features[0].Enabled {
				t.Error("a flag stored as 5 must read as on")
			}
		}

		// A user group's policy cannot be saved: its ceiling is never worked
		// out, so the save fails with a 500 and writes nothing (BUGS UM2).
		mustExec(t, db, `INSERT INTO panel_access_policies (subject_type, subject_id, resource_key, access_level) VALUES (?, ?, ?, ?)`,
			"group", "ops", "chat", "write")
		for _, guid := range []string{"ops", "no-such-group"} {
			_, err := s.SavePolicy(ctx, "group", guid, []any{map[string]any{"resource_key": "users", "access_level": "read"}}, nil)
			if !errors.Is(err, errGroupCeiling) || ErrorStatus(err) != 500 {
				t.Errorf("save for group %q: %v", guid, err)
			}
		}
		if rows, _ := s.PanelAccessPolicies(ctx, "group", "ops"); jsJSON(t, rows) != `[{"resource_key":"chat","access_level":"write"}]` {
			t.Errorf("the group's rows after a failed save: %v", rows)
		}

		// A write replaces all rows of its subject.
		if _, err := s.SavePolicy(ctx, "user", "1", []any{map[string]any{"resource_key": "folders", "access_level": "none"}}, nil); err != nil {
			t.Fatal(err)
		}
		sections, _ = s.PanelAccessPolicies(ctx, "user", "1")
		features, _ = s.ClientFeaturePolicies(ctx, "user", "1")
		if jsJSON(t, sections) != `[{"resource_key":"folders","access_level":"none"}]` || len(features) != 0 {
			t.Errorf("after the second write: %s %s", jsJSON(t, sections), jsJSON(t, features))
		}

		// Refused subjects write nothing, and are refused before the ceiling.
		var se *StatusError
		if _, err := s.SavePolicy(ctx, "team", "1", nil, nil); !errors.As(err, &se) || se.Status != 400 {
			t.Errorf("bad subject type: %v", err)
		}
		if _, err := s.SavePolicy(ctx, "user", "a b", nil, nil); !errors.As(err, &se) || se.Status != 400 {
			t.Errorf("bad subject id: %v", err)
		}
		if _, err := s.SavePolicy(ctx, "group", "a b", nil, nil); !errors.As(err, &se) || se.Status != 400 {
			t.Errorf("bad group subject id: %v", err)
		}

		// The resolved policy reads the rows of the account and of its groups.
		policy, err := s.ResolveForUser(ctx, PolicyUser{ID: 1, Role: "operator"})
		if err != nil {
			t.Fatal(err)
		}
		if got, _ := policy.Sections.Get("folders"); got != (SectionAccess{LevelNone, SourceUser, LevelWrite}) {
			t.Errorf("folders: %+v", got)
		}
		if got, _ := policy.Sections.Get("chat"); got != (SectionAccess{LevelNone, SourceGroup, LevelNone}) {
			t.Errorf("chat: %+v", got)
		}
		if got := jsJSON(t, policy.Groups); got != `[{"guid":"lab","name":"Lab"},{"guid":"ops","name":"Operators"}]` {
			t.Errorf("groups: %s", got)
		}

		// Lookups of the accounts and groups the routes resolve policies for.
		if user, err := s.PolicyUserByID(ctx, 1); err != nil || user == nil || *user != (PolicyUser{ID: 1, Role: "operator"}) {
			t.Errorf("user 1: %+v, %v", user, err)
		}
		if user, err := s.PolicyUserByID(ctx, 2); err != nil || user == nil || user.Role != "" {
			t.Errorf("user without a role: %+v, %v", user, err)
		}
		if user, err := s.PolicyUserByID(ctx, 99); err != nil || user != nil {
			t.Errorf("unknown user: %+v, %v", user, err)
		}
		if group, err := s.UserGroupByGUID(ctx, "ops"); err != nil || group == nil || group.Name != "Operators" || group.ID != 1 {
			t.Errorf("group ops: %+v, %v", group, err)
		}
		if group, err := s.UserGroupByGUID(ctx, "nope"); err != nil || group != nil {
			t.Errorf("unknown group: %+v, %v", group, err)
		}
		if groups, err := s.UserGroupsForUser(ctx, 2); err != nil || len(groups) != 0 {
			t.Errorf("groups of a user in none: %v, %v", groups, err)
		}
	})
}
