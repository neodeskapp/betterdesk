// Package auth — granular permission system for RBAC (Phase 52).
//
// Each permission is a dot-separated action string: "resource.action".
// Roles map to a set of granted permissions via DefaultRolePermissions.
// Custom overrides are stored in the role_permissions DB table.
package auth

import "strings"

// Permission constants define every discrete action in the system.
const (
	// Device permissions
	PermDeviceView           = "device.view"
	PermDeviceConnect        = "device.connect"
	PermGuestCreate          = "guest.create"
	PermDeviceEdit           = "device.edit"   // notes, tags, display name
	PermDeviceDelete         = "device.delete" // soft-delete + revoke
	PermDeviceBan            = "device.ban"    // ban/unban
	PermDeviceChangeID       = "device.change_id"
	PermDeviceConnectionMode = "device.connection_mode" // normal <-> incoming-only
	// Saved sign-in (L469): use the account name and password saved for a
	// device to sign in at its lock screen, and set, replace or clear them.
	// Only the super admins hold them unless a role is given them.
	PermDeviceSigninUse     = "device.signin_use"
	PermDeviceSigninManage  = "device.signin_manage"
	PermRemoteTargetView    = "remote_target.view"
	PermRemoteTargetConnect = "remote_target.connect"
	PermRemoteTargetEdit    = "remote_target.edit"
	PermRemoteTargetDelete  = "remote_target.delete"
	PermRemoteTargetTest    = "remote_target.test"

	// User management permissions
	PermUserView   = "user.view"
	PermUserCreate = "user.create"
	PermUserEdit   = "user.edit"
	PermUserDelete = "user.delete"
	// PermUserSecurity is the sign-in security of other accounts: a
	// temporary password, a password change at the next sign-in, unlocking
	// an account locked after failed sign-ins, requiring or resetting its
	// two-factor sign-in and new recovery codes.
	PermUserSecurity = "user.security"
	// PermUserImpersonate is "Sign in as": looking at the console as an
	// account with fewer rights, read-only. Only the super admins hold it
	// unless a role is given it.
	PermUserImpersonate = "user.impersonate"

	// Server configuration
	PermServerConfig      = "server.config"      // read/write server_config
	PermServerKeys        = "server.keys"        // manage API keys
	PermServerAttestation = "server.attestation" // run/view server performance attestation

	// Organization permissions
	PermOrgCreate        = "org.create"
	PermOrgEdit          = "org.edit"
	PermOrgDelete        = "org.delete"
	PermOrgManageUsers   = "org.manage_users"
	PermOrgManageDevices = "org.manage_devices"

	// Audit + monitoring
	PermAuditView     = "audit.view"
	PermMetricsView   = "metrics.view"
	PermBlocklistEdit = "blocklist.edit"

	// CDAP
	PermCDAPView     = "cdap.view"
	PermCDAPCommand  = "cdap.command"
	PermCDAPTerminal = "cdap.terminal"
	PermCDAPFiles    = "cdap.files"

	// MeshCentral compatibility (NeoCore / MeshAgent)
	PermMeshTerminal = "mesh.terminal"
	PermMeshFiles    = "mesh.files"
	PermMeshPower    = "mesh.power"

	// NeoDesk agent console tools: the terminal and the files. The screen,
	// the input and the clipboard are remote control (device.connect), the
	// lists of services, processes and events device.view.
	PermAgentTerminal = "agent.terminal"
	PermAgentFiles    = "agent.files"

	// Enrollment
	PermEnrollmentManage  = "enrollment.manage"
	PermEnrollmentApprove = "enrollment.approve"

	// Chat (A242): Support chat with the people at computers, Team chat
	// between console accounts, assigning support conversations, the
	// shared canned replies and the teams of Team chat.
	PermChatSupport      = "chat.support"
	PermChatTeam         = "chat.team"
	PermChatAssign       = "chat.assign"
	PermChatCannedManage = "chat.canned_manage"
	PermChatTeamsManage  = "chat.teams_manage"
	// PermChatAccess is the one chat permission before A242. It is no
	// permission of its own any more: a role override or an API token that
	// still names it means Support chat and Team chat (LegacyPermissions).
	PermChatAccess = "chat.access"

	// Branding
	PermBrandingEdit = "branding.edit"

	// Billing / commercialization
	PermBillingView    = "billing.view"
	PermBillingManage  = "billing.manage"
	PermBillingReports = "billing.reports"
	PermBillingExport  = "billing.export"
)

// AllPermissions is the complete list of permission strings for validation.
var AllPermissions = []string{
	PermDeviceView, PermDeviceConnect, PermGuestCreate, PermDeviceEdit, PermDeviceDelete,
	PermDeviceBan, PermDeviceChangeID, PermDeviceConnectionMode,
	PermDeviceSigninUse, PermDeviceSigninManage,
	PermRemoteTargetView, PermRemoteTargetConnect, PermRemoteTargetEdit,
	PermRemoteTargetDelete, PermRemoteTargetTest,
	PermUserView, PermUserCreate, PermUserEdit, PermUserDelete,
	PermUserSecurity, PermUserImpersonate,
	PermServerConfig, PermServerKeys, PermServerAttestation,
	PermOrgCreate, PermOrgEdit, PermOrgDelete, PermOrgManageUsers, PermOrgManageDevices,
	PermAuditView, PermMetricsView, PermBlocklistEdit,
	PermCDAPView, PermCDAPCommand, PermCDAPTerminal, PermCDAPFiles,
	PermMeshTerminal, PermMeshFiles, PermMeshPower,
	PermAgentTerminal, PermAgentFiles,
	PermEnrollmentManage, PermEnrollmentApprove,
	PermChatSupport, PermChatTeam, PermChatAssign, PermChatCannedManage, PermChatTeamsManage,
	PermBrandingEdit,
	PermBillingView, PermBillingManage, PermBillingReports, PermBillingExport,
}

// DefaultRolePermissions maps each built-in role to its default set of permissions.
// Custom overrides from the DB take precedence.
//
// Role scoping:
//
//	super_admin  — all permissions, manages other super admins
//	server_admin — server infrastructure only, read-only user list
//	global_admin — all-org user/device/org management, NO server access
//	admin        — legacy alias, equivalent to super_admin
//	operator     — day-to-day device ops + chat
//	viewer       — read-only dashboards
//	pro          — API-only RustDesk PRO activation; no device or org device access
var DefaultRolePermissions = map[string]map[string]bool{
	RoleSuperAdmin: buildPermMap(AllPermissions),
	RoleAdmin:      buildPermMap(AllPermissions), // legacy admin = super_admin

	// Server Admin: infrastructure + monitoring, read-only user visibility.
	// Cannot create/edit/delete users, cannot manage orgs.
	RoleServerAdmin: buildPermMap([]string{
		PermServerConfig, PermServerKeys, PermServerAttestation,
		PermBlocklistEdit,
		PermUserView,   // read-only
		PermDeviceView, // read-only
		PermRemoteTargetView,
		PermAuditView,
		PermMetricsView,
		PermEnrollmentManage,
	}),

	// Global Admin: all user/org management, NO server config/keys.
	RoleGlobalAdmin: buildPermMap([]string{
		PermUserView, PermUserCreate, PermUserEdit, PermUserDelete, PermUserSecurity,
		PermOrgCreate, PermOrgEdit, PermOrgDelete, PermOrgManageUsers, PermOrgManageDevices,
		PermDeviceView, PermDeviceConnect, PermDeviceEdit, PermDeviceDelete,
		PermGuestCreate,
		PermDeviceBan, PermDeviceChangeID, PermDeviceConnectionMode,
		PermRemoteTargetView, PermRemoteTargetConnect, PermRemoteTargetEdit,
		PermRemoteTargetDelete, PermRemoteTargetTest,
		PermAuditView, PermMetricsView,
		PermCDAPView, PermCDAPCommand, PermCDAPTerminal, PermCDAPFiles,
		PermMeshTerminal, PermMeshFiles, PermMeshPower,
		PermAgentTerminal, PermAgentFiles,
		PermChatSupport, PermChatTeam, PermChatAssign, PermChatCannedManage, PermChatTeamsManage,
		PermEnrollmentManage, PermEnrollmentApprove,
		PermBrandingEdit,
		PermBillingView, PermBillingManage, PermBillingReports, PermBillingExport,
	}),

	RoleOperator: buildPermMap([]string{
		PermDeviceView, PermDeviceConnect, PermDeviceEdit, PermDeviceConnectionMode,
		PermRemoteTargetView, PermRemoteTargetConnect, PermRemoteTargetTest,
		PermGuestCreate,
		PermUserView,
		PermAuditView, PermMetricsView,
		PermCDAPView, PermCDAPCommand,
		PermMeshPower,
		PermChatSupport, PermChatTeam, PermChatAssign,
		PermOrgManageDevices,
		PermBillingView, PermBillingReports,
	}),
	RoleViewer: buildPermMap([]string{
		PermDeviceView,
		PermRemoteTargetView,
		PermAuditView, PermMetricsView,
		PermCDAPView,
		PermChatSupport, PermChatTeam,
	}),
	RolePro:         buildPermMap([]string{}),
	RoleGuestIssuer: buildPermMap([]string{PermDeviceView, PermGuestCreate}),
	// Device credentials authenticate an agent to its own transport only.
	// They never confer panel/API permissions.
	RoleDevice: buildPermMap([]string{}),
}

// buildPermMap converts a slice of permission strings into a lookup map.
func buildPermMap(perms []string) map[string]bool {
	m := make(map[string]bool, len(perms))
	for _, p := range perms {
		m[p] = true
	}
	return m
}

// IsProRole returns true for the API-only RustDesk PRO activation role.
func IsProRole(role string) bool {
	return role == RolePro
}

// ProRoleBlocksPermission reports permissions the pro role must never hold,
// even via DB overrides (device inventory and org device assignment).
func ProRoleBlocksPermission(permission string) bool {
	return strings.HasPrefix(permission, "device.") ||
		strings.HasPrefix(permission, "remote_target.") ||
		permission == PermOrgManageDevices
}

// RoleHasPermission checks whether a role (by name) has a specific permission
// according to default role mappings. Returns true for super_admin and legacy admin.
// For DB-overridden permissions, use the Database.HasRolePermission method instead.
func RoleHasPermission(role, permission string) bool {
	if IsDeviceRole(role) {
		return false
	}
	if IsProRole(role) && ProRoleBlocksPermission(permission) {
		return false
	}
	if IsSuperAdminRole(role) {
		return true
	}
	perms, ok := DefaultRolePermissions[role]
	if !ok {
		return false
	}
	return perms[permission]
}

// LegacyPermissions are permission names of older versions, each with the
// permissions it stands for now. A stored role override or an API token
// that still names one is read as those (ExpandLegacy); the migrations
// write the new names.
var LegacyPermissions = map[string][]string{
	PermChatAccess: {PermChatSupport, PermChatTeam},
}

// ExpandLegacy is the permission's current names: the ones a legacy name
// stands for, else the name itself.
func ExpandLegacy(permission string) []string {
	if now, ok := LegacyPermissions[permission]; ok {
		return now
	}
	return []string{permission}
}

// ValidPermission returns true if the given string is a recognized permission.
func ValidPermission(p string) bool {
	for _, v := range AllPermissions {
		if v == p {
			return true
		}
	}
	return false
}
