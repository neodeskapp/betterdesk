// Role and Permission management handlers for the BetterDesk API (Phase 52 RBAC).
//
// Provides endpoints:
//
//	GET    /api/roles                                - List all roles with default permissions
//	GET    /api/roles/{role}/permissions              - Get effective permissions for a role
//	GET    /api/role-permissions                      - List all custom permission overrides
//	POST   /api/role-permissions                      - Set a custom permission override
//	DELETE /api/role-permissions/{role}/{permission}   - Delete a custom override
package api

import (
	"encoding/json"
	"log"
	"net/http"
	"sort"

	"github.com/unitronix/betterdesk-server/auth"
)

// roleInfo describes a single role for the /api/roles response.
type roleInfo struct {
	Name          string   `json:"name"`
	Level         int      `json:"level"`
	IsSuperAdmin  bool     `json:"is_super_admin"`
	IsServerLevel bool     `json:"is_server_level"`
	Permissions   []string `json:"permissions"`
}

// handleListRoles returns all built-in roles with their default permission sets.
//
//	GET /api/roles
func (s *Server) handleListRoles(w http.ResponseWriter, r *http.Request) {
	roles := []string{
		auth.RoleSuperAdmin,
		auth.RoleAdmin,
		auth.RoleServerAdmin,
		auth.RoleGlobalAdmin,
		auth.RoleOperator,
		auth.RoleViewer,
		auth.RoleGuestIssuer,
		auth.RolePro,
	}

	result := make([]roleInfo, 0, len(roles))
	for _, role := range roles {
		perms := auth.AllPermissions
		if !auth.IsSuperAdminRole(role) {
			perms = inPermissionOrder(auth.DefaultRolePermissions[role])
		}
		result = append(result, roleInfo{
			Name:          role,
			Level:         auth.RoleLevel(role),
			IsSuperAdmin:  auth.IsSuperAdminRole(role),
			IsServerLevel: auth.IsServerLevel(role),
			Permissions:   perms,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"roles":           result,
		"all_permissions": auth.AllPermissions,
	})
}

// handleGetRolePermissions returns the effective permission list for a specific role,
// merging defaults with custom DB overrides.
//
//	GET /api/roles/{role}/permissions
func (s *Server) handleGetRolePermissions(w http.ResponseWriter, r *http.Request) {
	role := r.PathValue("role")
	if !auth.ValidRole(role) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid role"})
		return
	}

	// Start with defaults
	effective := make(map[string]bool)
	if auth.IsSuperAdminRole(role) {
		for _, p := range auth.AllPermissions {
			effective[p] = true
		}
	} else {
		defMap := auth.DefaultRolePermissions[role]
		for p, v := range defMap {
			effective[p] = v
		}
	}

	// Apply custom overrides from DB. Without them the built-in set would
	// give back what an override took away, so a failed read is an error.
	if s.db != nil {
		overrides, err := s.db.ListRolePermissions(role)
		if err != nil {
			log.Printf("[api] Failed to read the permission overrides of role %s: %v", role, err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to read role permissions"})
			return
		}
		for _, o := range overrides {
			effective[o.Permission] = o.Granted
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"role":        role,
		"permissions": inPermissionOrder(effective),
	})
}

// inPermissionOrder lists the granted permissions of a set in the order of
// auth.AllPermissions, then the names the server does not know, sorted, so
// two calls answer the same list.
func inPermissionOrder(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for _, p := range auth.AllPermissions {
		if set[p] {
			out = append(out, p)
		}
	}
	known := make(map[string]bool, len(auth.AllPermissions))
	for _, p := range auth.AllPermissions {
		known[p] = true
	}
	var extra []string
	for p, granted := range set {
		if granted && !known[p] {
			extra = append(extra, p)
		}
	}
	sort.Strings(extra)
	return append(out, extra...)
}

// handleListRolePermissionOverrides returns all custom permission overrides from the DB.
//
//	GET /api/role-permissions
func (s *Server) handleListRolePermissionOverrides(w http.ResponseWriter, r *http.Request) {
	roleFilter := r.URL.Query().Get("role")

	type override struct {
		Role       string `json:"role"`
		Permission string `json:"permission"`
		Granted    bool   `json:"granted"`
	}

	var result []override

	roles := []string{
		auth.RoleSuperAdmin, auth.RoleAdmin, auth.RoleServerAdmin,
		auth.RoleGlobalAdmin, auth.RoleOperator, auth.RoleViewer, auth.RoleGuestIssuer, auth.RolePro,
	}

	for _, role := range roles {
		if roleFilter != "" && role != roleFilter {
			continue
		}
		overrides, err := s.db.ListRolePermissions(role)
		if err != nil {
			continue
		}
		for _, o := range overrides {
			result = append(result, override{
				Role:       role,
				Permission: o.Permission,
				Granted:    o.Granted,
			})
		}
	}

	if result == nil {
		result = []override{}
	}

	writeJSON(w, http.StatusOK, map[string]any{"overrides": result})
}

// requireSuperAdminForOverrides answers 403 unless the caller is a super
// admin (or legacy admin, which includes the console's API key). The routes
// need server.config, which server_admin holds; without this a server_admin
// could grant its own role any permission.
func requireSuperAdminForOverrides(w http.ResponseWriter, r *http.Request) bool {
	if auth.IsSuperAdminRole(getRoleFromCtx(r)) {
		return true
	}
	writeJSON(w, http.StatusForbidden, map[string]string{"error": "Only super admins can change role permissions"})
	return false
}

// handleSetRolePermission creates or updates a custom permission override.
//
//	POST /api/role-permissions
//	Body: {"role": "operator", "permission": "device.delete", "granted": true}
func (s *Server) handleSetRolePermission(w http.ResponseWriter, r *http.Request) {
	if !requireSuperAdminForOverrides(w, r) {
		return
	}
	var body struct {
		Role       string `json:"role"`
		Permission string `json:"permission"`
		Granted    bool   `json:"granted"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON"})
		return
	}

	if !auth.ValidRole(body.Role) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid role"})
		return
	}
	if !auth.ValidPermission(body.Permission) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid permission"})
		return
	}

	// Cannot modify super_admin/admin permissions
	if auth.IsSuperAdminRole(body.Role) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Cannot override super admin permissions"})
		return
	}

	if err := s.db.SetRolePermission(body.Role, body.Permission, body.Granted); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to set permission"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleDeleteRolePermission removes a custom permission override, reverting to defaults.
//
//	DELETE /api/role-permissions/{role}/{permission}
func (s *Server) handleDeleteRolePermission(w http.ResponseWriter, r *http.Request) {
	if !requireSuperAdminForOverrides(w, r) {
		return
	}
	role := r.PathValue("role")
	permission := r.PathValue("permission")

	if !auth.ValidRole(role) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid role"})
		return
	}
	if !auth.ValidPermission(permission) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid permission"})
		return
	}

	if err := s.db.DeleteRolePermission(role, permission); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to delete override"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
