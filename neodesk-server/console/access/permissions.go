package access

import (
	"github.com/unitronix/betterdesk-server/auth"
)

const rolePro = auth.RolePro

// PermissionSet is the effective permission set of a role.
type PermissionSet map[string]struct{}

// Has reports whether the set holds the permission.
func (s PermissionSet) Has(permission string) bool {
	_, ok := s[permission]
	return ok
}

// clone returns a copy the caller may change.
func (s PermissionSet) clone() PermissionSet {
	out := make(PermissionSet, len(s))
	for p := range s {
		out[p] = struct{}{}
	}
	return out
}

func newPermissionSet(permissions []string) PermissionSet {
	out := make(PermissionSet, len(permissions))
	for _, p := range permissions {
		out[p] = struct{}{}
	}
	return out
}

// IsSuperAdminRole reports whether the role passes every permission check:
// super_admin and the older admin.
func IsSuperAdminRole(role string) bool { return auth.IsSuperAdminRole(role) }

// MayChangeAccount reports whether the signed-in account callerID with the
// role callerRole may change an account, its access policy included: its
// own, or one whose role the caller could give. The super admin roles may
// change every account, global_admin operator, viewer, pro and guest_issuer
// accounts, every other role only its own.
func MayChangeAccount(callerID int64, callerRole string, target PolicyUser) bool {
	return callerID != 0 && callerID == target.ID || auth.CanAssignRole(callerRole, target.Role)
}

// IsAdminRole reports whether the role is one of the four admin roles the
// older role checks let through: super_admin, admin, global_admin and
// server_admin.
func IsAdminRole(role string) bool { return auth.IsServerLevel(role) }

// ProRoleBlocksPermission reports whether the permission is one a pro
// account can never hold, whatever the role's overrides say: device and
// remote target permissions and the assignment of devices to organizations.
func ProRoleBlocksPermission(role, permission string) bool {
	return auth.IsProRole(role) && auth.ProRoleBlocksPermission(permission)
}

// RoleHasPermission reports whether a role has a permission by the built-in
// map alone, without overrides. Super admins have every permission, unknown
// roles none.
func RoleHasPermission(role, permission string) bool {
	return auth.RoleHasPermission(role, permission)
}

// Context is what a request's permission checks look at: who is signed in,
// the role's effective permissions, the account's panel access policy and
// the panel section the guard found for the path. Service.Load fills it once
// per request; Guard sets Section.
type Context struct {
	// UserID is the id of the signed-in account, 0 without a session.
	UserID int64
	// HasUser reports whether the session carries its user record.
	HasUser bool
	// Role is the role in the session, "" without one.
	Role string

	// Loaded is set once the role's permissions were looked up for this
	// request, successfully or not. Checks on a context that was never
	// loaded fall back to the built-in role map.
	Loaded bool
	// Permissions is the role's effective set, nil when it was not loaded
	// (super admins, no role, or a failed lookup).
	Permissions PermissionSet
	// PermissionErr is why the role's permissions could not be loaded.
	PermissionErr error

	// Policy is the account's effective panel access policy, nil when it
	// was not loaded.
	Policy *Policy
	// PolicyErr is why the policy could not be loaded.
	PolicyErr error

	// Section is the panel section of the request's path, "" when no guard
	// rule matches or nobody is signed in.
	Section string
}

// permissionLookupFailed reports whether the role's permissions were looked
// up and the lookup failed.
func (c *Context) permissionLookupFailed() bool {
	return c.Loaded && c.PermissionErr != nil
}

// HasPermission reports whether the request's account has the permission.
// The role decides first, and the panel section can only take away:
//
//  1. A pro account never has a device permission.
//  2. Super admins have every permission.
//  3. The role must hold the permission: in its effective permissions when
//     they were loaded, never when loading them failed, else in the
//     built-in role map.
//  4. On a path with a panel section, the section's own two permissions
//     also need the account's level there: the read permission needs
//     read, the write permission needs write. The read permission is
//     looked at first, so in a section whose two permissions are the
//     same the permission needs read; the guard asks write for writes.
func (c *Context) HasPermission(permission string) bool {
	if ProRoleBlocksPermission(c.Role, permission) {
		return false
	}
	if IsSuperAdminRole(c.Role) {
		return true
	}
	if !c.roleHolds(permission) {
		return false
	}
	if level, ok := c.sectionLevel(); ok {
		if entry, known := CatalogEntry(c.Section); known {
			if entry.ReadPermission != "" && string(entry.ReadPermission) == permission {
				return rank(level) >= rank(LevelRead)
			}
			if entry.WritePermission != "" && string(entry.WritePermission) == permission {
				return rank(level) >= rank(LevelWrite)
			}
		}
	}
	return true
}

// roleHolds reports whether the account's role has the permission: by its
// effective permissions when they were loaded, not at all when loading
// them failed, else by the built-in role map.
func (c *Context) roleHolds(permission string) bool {
	if c.Permissions != nil {
		return c.Permissions.Has(permission)
	}
	if c.permissionLookupFailed() {
		return false
	}
	return RoleHasPermission(c.Role, permission)
}

// sectionLevel returns the account's level in the request's section, when
// the policy has that section.
func (c *Context) sectionLevel() (Level, bool) {
	if c.Policy == nil || c.Section == "" {
		return LevelNone, false
	}
	entry, ok := c.Policy.Sections.Get(c.Section)
	if !ok {
		return LevelNone, false
	}
	return NormalizeLevel(string(entry.Access)), true
}

// UIPermissions lists every permission the web app checks in the browser.
// The bootstrap sends the ones the account has; the server still checks
// each request on its own.
var UIPermissions = []string{
	"server.config", "server.keys", "server.attestation", "blocklist.edit",
	"device.edit", "device.view", "device.connect", "device.ban", "device.delete",
	"device.connection_mode", "device.change_id",
	"org.manage_devices", "org.manage_users", "org.create", "org.edit", "org.delete",
	"branding.edit", "enrollment.approve", "enrollment.manage",
	"billing.view", "billing.manage", "billing.reports", "billing.export",
	"audit.view", "user.view", "user.create", "user.edit", "user.delete", "metrics.view",
	"remote_target.view", "remote_target.edit", "remote_target.delete",
	"remote_target.connect", "remote_target.test",
	"guest.create", "chat.access", "cdap.view", "cdap.command", "cdap.terminal", "cdap.files",
	"mesh.terminal", "mesh.power", "mesh.files",
}

// GrantedUIPermissions returns the permissions of UIPermissions the account
// has, in that order.
func (c *Context) GrantedUIPermissions() []string {
	out := make([]string, 0, len(UIPermissions))
	for _, p := range UIPermissions {
		if c.HasPermission(p) {
			out = append(out, p)
		}
	}
	return out
}
