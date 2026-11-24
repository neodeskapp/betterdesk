package access

import (
	"context"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/admintoken"
)

func TestLoad(t *testing.T) {
	ctx := context.Background()
	newService := func(data *fakeData) (*Service, *fakeRoles) {
		roles := &fakeRoles{permissions: map[string][]string{
			"operator": {"device.view", "device.edit", "user.view"},
			"viewer":   {"device.view"},
		}}
		return fakeService(data, roles), roles
	}

	// A signed-in operator: permissions, then the policy; one lookup serves both.
	s, roles := newService(&fakeData{sections: map[string][]SectionRow{"user:7": {{"users", "none"}}}})
	c := s.Load(ctx, Session{UserID: 7, HasUser: true, Role: "operator"})
	if !c.Loaded || c.PermissionErr != nil || c.PolicyErr != nil || c.Policy == nil || c.Section != "" {
		t.Fatalf("operator: %+v", c)
	}
	if !c.Permissions.Has("device.edit") || c.Permissions.Has("server.config") || roles.calls != 1 {
		t.Errorf("operator: permissions %v after %d lookups", c.Permissions, roles.calls)
	}
	if users, _ := c.Policy.Sections.Get("users"); users.Access != LevelNone || users.Source != SourceUser {
		t.Errorf("operator: users %+v", users)
	}
	if c.Policy.SubjectID != "7" || c.UserID != 7 || c.Role != "operator" {
		t.Errorf("operator: %+v", c)
	}
	// What the request then sees: the guard refuses the section's API, and
	// on that path user.view is gone although the role has it.
	if got := c.Guard("GET", "/api/users"); got.Verdict != VerdictDenied {
		t.Errorf("guard: %+v", got)
	}
	if c.HasPermission("user.view") || !c.HasPermission("device.view") {
		t.Error("on /api/users the section's level decides user.view")
	}

	// Super admins: no lookup, write everywhere.
	s, roles = newService(&fakeData{})
	c = s.Load(ctx, Session{UserID: 1, HasUser: true, Role: "admin"})
	if !c.Loaded || c.Permissions != nil || c.PermissionErr != nil || roles.calls != 0 || c.Policy == nil {
		t.Fatalf("admin: %+v after %d lookups", c, roles.calls)
	}
	if !AccessAllows(c.Policy, "settings", LevelWrite) {
		t.Error("admin: settings must be at write")
	}

	// The two lower admin roles are looked up like any other role: an
	// override that takes a permission away from them holds.
	for _, role := range []string{"global_admin", "server_admin"} {
		lower, lowerRoles := newService(&fakeData{})
		lowerRoles.permissions[role] = []string{"device.view"}
		c = lower.Load(ctx, Session{UserID: 3, HasUser: true, Role: role})
		if c.Permissions == nil || lowerRoles.calls != 1 || c.HasPermission("user.view") || !c.HasPermission("device.view") {
			t.Errorf("%s: permissions %v after %d lookups", role, c.Permissions, lowerRoles.calls)
		}
	}

	// Nobody signed in: nothing is loaded, but the context counts as loaded.
	c = s.Load(ctx, Session{})
	if !c.Loaded || c.Permissions != nil || c.Policy != nil || c.PermissionErr != nil || c.PolicyErr != nil || roles.calls != 0 {
		t.Fatalf("no session: %+v", c)
	}
	// A session without its user record: the role is not trusted.
	c = s.Load(ctx, Session{UserID: 7, Role: "admin"})
	if c.Role != "" || c.Permissions != nil || c.Policy != nil || c.HasPermission("device.view") {
		t.Fatalf("no user record: %+v", c)
	}
	// A user record without an id (not signed in yet): permissions, no policy.
	c = s.Load(ctx, Session{HasUser: true, Role: "viewer"})
	if c.Permissions == nil || c.Policy != nil || c.PolicyErr != nil {
		t.Fatalf("user record without an id: %+v", c)
	}
	// A user record without a role: no permissions; the policy is the viewer's.
	c = s.Load(ctx, Session{UserID: 7, HasUser: true})
	if c.Permissions != nil || c.PermissionErr != nil || c.Policy == nil {
		t.Fatalf("no role: %+v", c)
	}
	if devices, _ := c.Policy.Sections.Get("devices"); devices.Access != LevelRead {
		t.Errorf("no role: devices %+v", devices)
	}

	// A role that cannot be looked up: both parts fail, and the request goes on.
	s, _ = newService(&fakeData{})
	c = s.Load(ctx, Session{UserID: 7, HasUser: true, Role: "made_up"})
	if !c.Loaded || c.PermissionErr == nil || c.PolicyErr == nil || c.Permissions != nil || c.Policy != nil {
		t.Fatalf("unknown role: %+v", c)
	}
	// On a guarded API path the client sees the guard's 503 first; elsewhere
	// the permission check's.
	if got := c.Guard("GET", "/api/devices"); got.Verdict != VerdictUnavailable || got.Decision.Body.Code != CodePolicyUnavailable {
		t.Errorf("guard after a failed load: %+v", got)
	}
	if d := c.RequirePermission("device.view", "/api/cdap/devices"); d.Status != 503 || d.Body.Code != CodePermissionService {
		t.Errorf("permission check after a failed load: %+v", d)
	}
	if c.HasPermission("device.view") {
		t.Error("a failed lookup must refuse")
	}

	// The policy rows cannot be read: only the policy fails.
	s, _ = newService(&fakeData{failing: "policies"})
	c = s.Load(ctx, Session{UserID: 7, HasUser: true, Role: "viewer"})
	if c.PermissionErr != nil || c.Permissions == nil || c.PolicyErr == nil || c.Policy != nil {
		t.Fatalf("unreadable policy: %+v", c)
	}
	if d := c.RequirePermission("device.view", "/api/cdap/devices"); !d.Passed() {
		t.Errorf("without a policy the permissions still decide: %+v", d)
	}
	// Admins are refused by the guard as well when their policy cannot be read.
	c = s.Load(ctx, Session{UserID: 1, HasUser: true, Role: "admin"})
	if c.PolicyErr == nil {
		t.Fatal("admin with unreadable rows: the policy must fail")
	}
	if got := c.Guard("POST", "/api/users"); got.Verdict != VerdictUnavailable {
		t.Errorf("admin with unreadable rows: %+v", got)
	}
}

// A request an API token signed in gives no role that holds a permission
// the token lacks, whatever its account may give: the token's capped set
// counts, not only the account's role. A browser session and a token with
// every permission of its account give what the role allows (L431).
func TestMayGiveRoleKeepsToTheTokensPermissions(t *testing.T) {
	s := fakeService(&fakeData{}, &fakeRoles{permissions: map[string][]string{
		"global_admin": {"user.view", "user.create", "user.edit", "device.view", "audit.view"},
		"viewer":       {"device.view", "audit.view"},
	}})
	token := func(full bool, permissions ...string) context.Context {
		return admintoken.WithUse(context.Background(), &admintoken.Use{Token: &admintoken.Token{Permissions: permissions}, Full: full})
	}
	for _, c := range []struct {
		name, caller string
		ctx          context.Context
		want         bool
	}{
		{"a browser session", "global_admin", context.Background(), true},
		{"a full token", "global_admin", token(true, "user.view", "user.create", "user.edit", "device.view", "audit.view"), true},
		{"a token without audit.view", "global_admin", token(false, "user.view", "user.create", "user.edit", "device.view"), false},
		{"a super admin's token without audit.view", "super_admin", token(false, "user.view", "user.create", "user.edit", "device.view"), false},
		{"a token with what the role holds", "global_admin", token(false, "user.create", "device.view", "audit.view"), true},
	} {
		got, err := s.MayGiveRole(c.ctx, c.caller, "viewer")
		if err != nil || got != c.want {
			t.Errorf("%s gives viewer: %v %v, want %v", c.name, got, err, c.want)
		}
	}
}
