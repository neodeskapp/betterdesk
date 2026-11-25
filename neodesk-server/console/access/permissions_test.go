package access

import (
	"errors"
	"reflect"
	"slices"
	"sort"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
)

// consoleRoleMap is the role map console requests were answered with: the
// contract of the Node console's built-in map. Package auth holds the one
// copy the server uses; this table pins it for the console, so a change to
// auth/permissions.go that changes what a console account may do fails here.
var consoleRoleMap = map[string][]string{
	"server_admin": {
		"server.config", "server.keys", "server.attestation", "blocklist.edit", "user.view", "device.view",
		"remote_target.view", "audit.view", "metrics.view", "enrollment.manage",
	},
	"global_admin": {
		"user.view", "user.create", "user.edit", "user.delete",
		"org.create", "org.edit", "org.delete", "org.manage_users", "org.manage_devices",
		"device.view", "device.connect", "device.edit", "device.delete",
		"remote_target.view", "remote_target.connect", "remote_target.edit", "remote_target.delete", "remote_target.test",
		"guest.create", "device.ban", "device.change_id", "device.connection_mode", "audit.view", "metrics.view",
		"cdap.view", "cdap.command", "cdap.terminal", "cdap.files", "mesh.terminal", "mesh.files", "mesh.power",
		"agent.terminal", "agent.files",
		"chat.support", "chat.team", "chat.assign", "chat.canned_manage", "enrollment.manage", "enrollment.approve", "branding.edit",
		"billing.view", "billing.manage", "billing.reports", "billing.export",
	},
	"operator": {
		"device.view", "device.connect", "device.edit", "device.connection_mode",
		"remote_target.view", "remote_target.connect", "remote_target.test", "guest.create", "user.view",
		"audit.view", "metrics.view", "cdap.view", "cdap.command", "mesh.power", "chat.support", "chat.team", "chat.assign",
		"org.manage_devices", "billing.view", "billing.reports",
	},
	"viewer":       {"device.view", "remote_target.view", "audit.view", "metrics.view", "cdap.view", "chat.support", "chat.team"},
	"guest_issuer": {"device.view", "guest.create"},
	"pro":          {},
}

// Jest: rolePermissions.goParity.test.js. Every default role against every
// permission name the server knows.
func TestRoleHasPermissionForEveryRoleAndPermission(t *testing.T) {
	// 43 of the Node console, the NeoDesk agent's terminal and files (L388),
	// the two of saved sign-in, which no role but the super admins has
	// (L469), and chat.access as four chat permissions (A242)
	if len(auth.AllPermissions) != 50 {
		t.Fatalf("the server knows %d permissions, the console's contract was written for 50", len(auth.AllPermissions))
	}
	for role, granted := range consoleRoleMap {
		want := newPermissionSet(granted)
		for _, permission := range auth.AllPermissions {
			if got := RoleHasPermission(role, permission); got != want.Has(permission) {
				t.Errorf("RoleHasPermission(%q, %q) = %v, want %v", role, permission, got, want.Has(permission))
			}
		}
		for _, permission := range granted {
			if !auth.ValidPermission(permission) {
				t.Errorf("%s: %q is not a permission the server knows", role, permission)
			}
		}
	}
	for _, role := range []string{"super_admin", "admin"} {
		for _, permission := range append([]string{"made.up", ""}, auth.AllPermissions...) {
			if !RoleHasPermission(role, permission) {
				t.Errorf("%s must have %q", role, permission)
			}
		}
	}
	for _, role := range []string{"", "unknown", "device", "ADMIN", "Operator", "constructor"} {
		for _, permission := range auth.AllPermissions {
			if RoleHasPermission(role, permission) {
				t.Errorf("role %q must have nothing, has %q", role, permission)
			}
		}
	}
}

func TestProRule(t *testing.T) {
	blocked := []string{"device.view", "device.edit", "device.", "device.anything", "remote_target.view", "remote_target.", "org.manage_devices"}
	open := []string{"chat.support", "org.manage_users", "devicex.view", "device", "org.manage_devicesx", "DEVICE.VIEW"}
	for _, p := range blocked {
		if !ProRoleBlocksPermission("pro", p) {
			t.Errorf("pro must be blocked from %q", p)
		}
		if ProRoleBlocksPermission("operator", p) || ProRoleBlocksPermission("", p) {
			t.Errorf("only pro is blocked from %q", p)
		}
	}
	for _, p := range open {
		if ProRoleBlocksPermission("pro", p) {
			t.Errorf("pro must not be blocked from %q by the pro rule", p)
		}
	}
	// The block holds against overrides and against a section level.
	c := &Context{
		UserID: 8, HasUser: true, Role: "pro", Loaded: true,
		Permissions: newPermissionSet([]string{"device.view", "chat.support"}),
		Section:     "devices", Policy: policyWith("devices", LevelWrite),
	}
	if c.HasPermission("device.view") || c.HasPermission("device.edit") {
		t.Error("a pro account got a device permission")
	}
	if !c.HasPermission("chat.support") {
		t.Error("a pro account lost a permission its overrides grant")
	}
}

func TestAdminRoles(t *testing.T) {
	for role, want := range map[string][2]bool{
		"super_admin": {true, true}, "admin": {true, true}, "global_admin": {false, true}, "server_admin": {false, true},
		"operator": {false, false}, "viewer": {false, false}, "guest_issuer": {false, false}, "pro": {false, false}, "": {false, false},
	} {
		if IsSuperAdminRole(role) != want[0] || IsAdminRole(role) != want[1] {
			t.Errorf("%q: super admin %v, admin %v; want %v", role, IsSuperAdminRole(role), IsAdminRole(role), want)
		}
	}
}

func TestHasPermissionRules(t *testing.T) {
	boom := errors.New("lookup failed")
	set := func(p ...string) PermissionSet { return newPermissionSet(p) }
	cases := []struct {
		name       string
		ctx        Context
		permission string
		want       bool
	}{
		// 2. super admins
		{"admin has everything", Context{Role: "admin"}, "made.up", true},
		{"admin passes with a failed lookup", Context{Role: "admin", Loaded: true, PermissionErr: boom}, "server.config", true},
		{"admin passes in a section at none", Context{Role: "super_admin", Section: "devices", Policy: policyWith("devices", LevelNone)}, "device.edit", true},

		// 3. the role decides first: the effective set wins over the built-in map
		{"an override grants", Context{Role: "viewer", Loaded: true, Permissions: set("server.config")}, "server.config", true},
		{"an override takes away", Context{Role: "operator", Loaded: true, Permissions: set("device.view")}, "device.connect", false},
		{"an empty set grants nothing", Context{Role: "global_admin", Loaded: true, Permissions: set()}, "user.view", false},
		{"failed lookup refuses what the role has by default", Context{Role: "operator", Loaded: true, PermissionErr: boom}, "device.view", false},
		{"failed lookup refuses also at write in the section", Context{Role: "operator", Loaded: true, PermissionErr: boom, Section: "devices", Policy: policyWith("devices", LevelWrite)}, "device.view", false},
		{"not loaded: built-in map grants", Context{Role: "operator"}, "device.view", true},
		{"not loaded: built-in map refuses", Context{Role: "viewer"}, "device.edit", false},
		{"an error on a context that was never loaded is ignored", Context{Role: "operator", PermissionErr: boom}, "device.view", true},
		{"no role has nothing", Context{}, "device.view", false},

		// a section level never grants what the role lacks
		{"write level does not grant a permission the role lacks", Context{Role: "viewer", Section: "devices", Policy: policyWith("devices", LevelWrite), Permissions: set("device.view")}, "device.edit", false},
		{"read level does not grant the read permission the role lacks", Context{Role: "viewer", Section: "devices", Policy: policyWith("devices", LevelRead), Permissions: set()}, "device.view", false},
		{"write level does not grant from the built-in map either", Context{Role: "viewer", Section: "devices", Policy: policyWith("devices", LevelWrite)}, "device.edit", false},
		{"global_admin gets no server.attestation from branding.edit", Context{Role: "global_admin", Section: "server-attestation", Policy: policyWith("server-attestation", LevelWrite), Permissions: set("branding.edit")}, "server.attestation", false},
		{"server_admin gets no enrollment.approve from enrollment.manage", Context{Role: "server_admin", Section: "registrations", Policy: policyWith("registrations", LevelWrite), Permissions: set("enrollment.manage")}, "enrollment.approve", false},

		// 4. the section's own permissions the role holds follow the level
		{"read level lets the read permission through", Context{Role: "viewer", Section: "devices", Policy: policyWith("devices", LevelRead), Permissions: set("device.view")}, "device.view", true},
		{"read level refuses the write permission", Context{Role: "operator", Section: "devices", Policy: policyWith("devices", LevelRead), Permissions: set("device.edit")}, "device.edit", false},
		{"none refuses the read permission the role has", Context{Role: "operator", Section: "devices", Policy: policyWith("devices", LevelNone), Permissions: set("device.view")}, "device.view", false},
		{"a section with one permission for both lets it through at read (S14)", Context{Role: "operator", Section: "guests", Policy: policyWith("guests", LevelRead), Permissions: set("guest.create")}, "guest.create", true},
		{"and at write", Context{Role: "operator", Section: "guests", Policy: policyWith("guests", LevelWrite), Permissions: set("guest.create")}, "guest.create", true},
		{"and not at none", Context{Role: "operator", Section: "guests", Policy: policyWith("guests", LevelNone), Permissions: set("guest.create")}, "guest.create", false},
		{"other permissions are not the section's business", Context{Role: "operator", Section: "devices", Policy: policyWith("devices", LevelNone), Permissions: set("user.view")}, "user.view", true},
		{"a section without a write permission", Context{Role: "viewer", Section: "activity", Policy: policyWith("activity", LevelRead), Permissions: set("audit.view")}, "audit.view", true},
		{"toolkit's permission at read", Context{Role: "viewer", Section: "toolkit", Policy: policyWith("toolkit", LevelRead), Permissions: set("device.connect", "device.edit")}, "device.connect", true},
		{"device.edit is not the toolkit's", Context{Role: "viewer", Section: "toolkit", Policy: policyWith("toolkit", LevelNone), Permissions: set("device.connect", "device.edit")}, "device.edit", true},
		{"a level in odd spelling counts", Context{Role: "viewer", Section: "devices", Policy: policyWith("devices", " WRITE "), Permissions: set("device.edit")}, "device.edit", true},
		{"a section the policy lacks falls through", Context{Role: "viewer", Section: "devices", Policy: &Policy{}, Permissions: set("device.edit")}, "device.edit", true},
		{"a section the catalog lacks falls through", Context{Role: "viewer", Section: "nope", Policy: policyWith("nope", LevelNone), Permissions: set("device.view")}, "device.view", true},
		{"without a section the policy is not asked", Context{Role: "viewer", Policy: policyWith("devices", LevelNone), Permissions: set("device.view")}, "device.view", true},
	}
	for _, c := range cases {
		ctx := c.ctx
		if ctx.Role != "" {
			ctx.UserID, ctx.HasUser = 5, true
		}
		if got := ctx.HasPermission(c.permission); got != c.want {
			t.Errorf("%s: HasPermission(%q) = %v, want %v", c.name, c.permission, got, c.want)
		}
	}
}

// TestBootstrapListsSavedSigninForTheAccountsThatHoldIt: the console's
// permissions name Use and Manage saved sign-in after Node's list, for
// the super admins and for a role an override gives them, not for a role
// without them (L469).
func TestBootstrapListsSavedSigninForTheAccountsThatHoldIt(t *testing.T) {
	admin := &Context{UserID: 1, HasUser: true, Role: "admin"}
	if got := admin.GrantedConsolePermissions(); !reflect.DeepEqual(got, append(append([]string(nil), UIPermissions...), LaterUIPermissions...)) {
		t.Errorf("admin: %v", got)
	}
	operator := &Context{UserID: 5, HasUser: true, Role: "operator", Loaded: true,
		Permissions: newPermissionSet(append(consoleRoleMap["operator"], auth.PermDeviceSigninManage))}
	got := operator.GrantedConsolePermissions()
	if !slices.Contains(got, auth.PermDeviceSigninManage) || slices.Contains(got, auth.PermDeviceSigninUse) {
		t.Errorf("operator with Manage saved sign-in: %v", got)
	}
	viewer := &Context{UserID: 4, HasUser: true, Role: "viewer"}
	for _, p := range LaterUIPermissions {
		if slices.Contains(viewer.GrantedConsolePermissions(), p) {
			t.Errorf("viewer lists %s", p)
		}
		if !auth.ValidPermission(p) {
			t.Errorf("%s is not a permission the server knows", p)
		}
	}
}

func TestGrantedUIPermissions(t *testing.T) {
	sorted := func(in []string) []string {
		out := append([]string(nil), in...)
		sort.Strings(out)
		return out
	}
	viewer := &Context{UserID: 4, HasUser: true, Role: "viewer"}
	if got, want := sorted(viewer.GrantedUIPermissions()), sorted(consoleRoleMap["viewer"]); !reflect.DeepEqual(got, want) {
		t.Errorf("viewer: %v, want %v", got, want)
	}
	admin := &Context{UserID: 1, HasUser: true, Role: "admin"}
	if got := admin.GrantedUIPermissions(); !reflect.DeepEqual(got, UIPermissions) {
		t.Errorf("admin: %v, want every UI permission in order", got)
	}
	failed := &Context{UserID: 4, HasUser: true, Role: "viewer", Loaded: true, PermissionErr: errors.New("boom")}
	if got := failed.GrantedUIPermissions(); len(got) != 0 {
		t.Errorf("failed lookup: %v, want none", got)
	}
	// Every UI permission is one the server knows.
	for _, p := range UIPermissions {
		if !auth.ValidPermission(p) {
			t.Errorf("UI permission %q is unknown to the server", p)
		}
	}
}

// S14 (analysis C1): in a section whose read and write permission are the
// same, a row at read must let the account read: the permission check
// needs read for that permission, and the guard still refuses writes.
func TestReadLevelPassesReadsInOnePermissionSections(t *testing.T) {
	checked := 0
	for _, entry := range Catalog {
		if entry.ReadPermission == "" || entry.ReadPermission != entry.WritePermission {
			continue
		}
		checked++
		permission := string(entry.ReadPermission)
		c := &Context{UserID: 7, HasUser: true, Role: "operator", Loaded: true,
			Permissions: newPermissionSet([]string{permission}), Section: entry.Key, Policy: policyWith(entry.Key, LevelRead)}
		if !c.HasPermission(permission) {
			t.Errorf("%s at read: %s refused", entry.Key, permission)
		}
		if !AccessAllows(c.Policy, entry.Key, RequiredLevel("GET")) || AccessAllows(c.Policy, entry.Key, RequiredLevel("POST")) {
			t.Errorf("%s at read: the guard must let reads through and refuse writes", entry.Key)
		}
		c.Policy = policyWith(entry.Key, LevelNone)
		if c.HasPermission(permission) {
			t.Errorf("%s at none: %s granted", entry.Key, permission)
		}
	}
	if checked < 5 {
		t.Fatalf("only %d sections with one permission for both", checked)
	}
}

// A token is full when it carries every permission the account holds; a
// failed lookup of the role's permissions never makes it full.
func TestLimitToTokenFullness(t *testing.T) {
	viewer := consoleRoleMap["viewer"]
	cases := []struct {
		name  string
		ctx   Context
		token []string
		full  bool
	}{
		{"all of the role's permissions", Context{Role: "viewer", Loaded: true}, viewer, true},
		{"one missing", Context{Role: "viewer", Loaded: true}, viewer[1:], false},
		{"super admin with one missing", Context{Role: "super_admin", Loaded: true}, auth.AllPermissions[1:], false},
		{"super admin with all", Context{Role: "super_admin", Loaded: true}, auth.AllPermissions, true},
		{"failed lookup", Context{Role: "viewer", Loaded: true, PermissionErr: errors.New("boom")}, viewer, false},
	}
	for _, c := range cases {
		ctx := c.ctx
		ctx.LimitToToken(c.token, auth.AllPermissions)
		if ctx.CapFull != c.full {
			t.Errorf("%s: full %v, want %v", c.name, ctx.CapFull, c.full)
		}
		if !ctx.Token() || ctx.HasPermission("server.config") && !containsString(c.token, "server.config") {
			t.Errorf("%s: not capped", c.name)
		}
	}
	ctx := Context{Role: "super_admin", Loaded: true}
	ctx.LimitToToken(auth.AllPermissions, auth.AllPermissions)
	ctx.DropFromCap("server.config")
	if ctx.HasPermission("server.config") || !ctx.HasPermission("audit.view") {
		t.Error("DropFromCap took the wrong permission")
	}
}
